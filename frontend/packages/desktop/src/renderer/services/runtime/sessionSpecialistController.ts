import { ipcBridge } from '@/common';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import { changeSessionSpecialist, freezeSpecialistCommand, loadSessionSpecialist } from './sessionSpecialist';
import {
  sameSpecialistBranch,
  sameSpecialistScope,
  sameSpecialistSelection,
  SpecialistScopeMismatch,
  SpecialistPreparationRequired,
  type SessionSpecialistIdentity,
  type SessionSpecialistCommand,
  type SessionSpecialistSnapshot,
  type SpecialistFailure,
  type SpecialistSendAuthority,
} from './sessionSpecialistTypes';

export type SessionSpecialistView = {
  snapshot: SessionSpecialistSnapshot | null;
  loading: boolean;
  writing: boolean;
  error: SpecialistFailure;
  retryable: boolean;
  available: boolean;
};
export const EMPTY_SPECIALIST_VIEW: SessionSpecialistView = {
  snapshot: null,
  loading: false,
  writing: false,
  error: null,
  retryable: false,
  available: false,
};
let ownerSequence = 0;

/** One request/presentation owner; persisted definitions and claims remain server authority. */
export class SessionSpecialistController {
  private readonly account = getRendererAccountScopeToken();
  private readonly resetKey = `session-specialist-${++ownerSequence}`;
  private listeners = new Set<() => void>();
  private observers = new Set<symbol>();
  private epoch = 0;
  private read: AbortController | null = null;
  private readPromise: Promise<void> | null = null;
  private writePromise: Promise<void> | null = null;
  private release: (() => void) | null = null;
  private dirty = false;
  private command: SessionSpecialistCommand | null = null;
  private intentUnconfirmed = false;
  private view = { ...EMPTY_SPECIALIST_VIEW, available: true };
  constructor(readonly identity: SessionSpecialistIdentity) {}
  getSnapshot = (): SessionSpecialistView => this.view;
  current = () =>
    this.account === getRendererAccountScopeToken() && this.identity.ownerId === getRendererAccountOwnerId();
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private active(epoch = this.epoch) {
    return this.current() && this.observers.size > 0 && this.epoch === epoch;
  }
  private publish(patch: Partial<SessionSpecialistView>) {
    this.view = { ...this.view, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  attach(): () => void {
    if (!this.current()) return () => {};
    const observer = Symbol();
    this.observers.add(observer);
    if (this.observers.size === 1) {
      this.epoch++;
      const unregister = registerRendererAccountReset(this.resetKey, () => {
        this.stop();
        this.command = null;
        this.intentUnconfirmed = false;
        this.publish({ ...EMPTY_SPECIALIST_VIEW });
      });
      const releases = [
        ipcBridge.conversation.listChanged.on(this.onEvent),
        ipcBridge.realtime.reconnected.on(this.invalidate),
      ];
      if (typeof window !== 'undefined') window.addEventListener('focus', this.invalidate);
      this.release = () => {
        unregister();
        releases.forEach((release) => release());
        if (typeof window !== 'undefined') window.removeEventListener('focus', this.invalidate);
      };
      if (this.writePromise) this.dirty = true;
      else void this.refresh();
    }
    return () => {
      if (this.observers.delete(observer) && !this.observers.size) this.stop();
    };
  }
  private stop() {
    this.observers.clear();
    this.epoch++;
    this.read?.abort();
    this.read = null;
    this.readPromise = null;
    this.release?.();
    this.release = null;
    this.dirty = false;
    this.publish({
      loading: false,
      writing: Boolean(this.writePromise),
      ...(this.command ? { error: 'uncertain', retryable: true } : {}),
    });
  }
  private onEvent = (event: unknown) => {
    if (!event || typeof event !== 'object') return;
    const row = event as Record<string, unknown>,
      scope = this.view.snapshot?.scope;
    if (
      row.source !== 'xscience' ||
      row.action !== 'updated' ||
      row.specialist_changed !== true ||
      row.conversation_id !== this.identity.frameId ||
      row.frame_incarnation_id !== this.identity.frameIncarnationId ||
      (scope && row.root_incarnation_id !== undefined && row.root_incarnation_id !== scope.root_incarnation_id) ||
      (typeof row.specialist_revision === 'number' &&
        this.view.snapshot &&
        row.specialist_revision < this.view.snapshot.revision)
    )
      return;
    this.invalidate();
  };
  private invalidate = () => {
    if (!this.active()) return;
    if (this.readPromise || this.writePromise) this.dirty = true;
    else void this.refresh();
  };
  private accept(snapshot: SessionSpecialistSnapshot) {
    const old = this.view.snapshot;
    if (old && (!sameSpecialistScope(old.scope, snapshot.scope) || snapshot.revision < old.revision)) return;
    if (old && old.revision === snapshot.revision && !sameSpecialistSelection(old.desired, snapshot.desired))
      throw new Error('Contradictory specialist revision');
    this.publish({ snapshot });
  }
  refresh = (): Promise<void> => {
    if (!this.active() || !this.view.available || this.writePromise) return Promise.resolve();
    if (this.readPromise) return this.readPromise;
    const epoch = this.epoch,
      read = new AbortController();
    this.read = read;
    const promise = Promise.resolve()
      .then(() => loadSessionSpecialist(this.identity, this.view.snapshot?.scope, read.signal))
      .then((snapshot) => {
        if (this.active(epoch) && this.read === read) this.accept(snapshot);
      })
      .catch((error: unknown) => {
        if (this.active(epoch) && this.read === read) this.fail(error);
      })
      .finally(() => {
        if (this.read !== read) return;
        this.read = null;
        this.readPromise = null;
        if (this.active(epoch)) {
          this.publish({ loading: false });
          this.refreshDirty();
        }
      });
    this.readPromise = promise;
    this.publish({ loading: true, ...(!this.intentUnconfirmed && !this.command ? { error: null } : {}) });
    return promise;
  };
  change = (name: string | null): Promise<void> => {
    if (this.writePromise) return this.writePromise;
    if (!this.active() || !this.view.available || !this.view.snapshot || this.view.loading || this.command)
      return Promise.resolve();
    const snapshot = this.view.snapshot,
      desired = snapshot.revision ? snapshot.desired : snapshot.applied;
    if (
      snapshot.revision > 0 &&
      !this.intentUnconfirmed &&
      !snapshot.applied_unavailable &&
      !snapshot.preparation_error &&
      sameSpecialistBranch(snapshot.branch, snapshot.prepared_branch) &&
      (name === null ? desired.is_default : desired.selection_name === name)
    )
      return Promise.resolve();
    this.command = freezeSpecialistCommand(snapshot, name);
    this.intentUnconfirmed = true;
    return this.submit();
  };
  retry = (): Promise<void> => this.writePromise ?? (this.command ? this.submit() : this.refresh());
  private submit(): Promise<void> {
    if (!this.command || !this.active() || !this.view.available) return Promise.resolve();
    const command = this.command,
      epoch = this.epoch;
    this.read?.abort();
    this.read = null;
    this.readPromise = null;
    const promise = Promise.resolve()
      .then(() => changeSessionSpecialist(this.identity, command))
      .then((result) => {
        if (!this.active(epoch) || this.command !== command) return;
        this.accept(result.snapshot);
        this.command = null;
        this.intentUnconfirmed = false;
        this.publish({ error: null, retryable: false });
      })
      .catch((error: unknown) => {
        if (!this.active(epoch) || this.command !== command) return;
        const code = isBackendHttpError(error) ? error.code : '';
        if (code === 'SESSION_SPECIALIST_REVISION_CONFLICT') {
          this.command = null;
          this.publish({ error: 'conflict', retryable: false });
          this.dirty = true;
        } else this.fail(error);
      })
      .finally(() => {
        if (this.writePromise !== promise) return;
        this.writePromise = null;
        if (this.active()) {
          this.publish({ writing: false });
          this.refreshDirty();
        }
      });
    this.writePromise = promise;
    this.publish({ writing: true, loading: false, error: null, retryable: false });
    return promise;
  }
  private refreshDirty() {
    if (this.dirty) {
      this.dirty = false;
      void this.refresh();
    }
  }
  private fail(error: unknown) {
    const code = isBackendHttpError(error) ? error.code : '';
    if (
      error instanceof SpecialistScopeMismatch ||
      ['AUTH_REQUIRED', 'SESSION_SPECIALIST_SCOPE_STALE'].includes(code)
    ) {
      this.command = null;
      this.publish({ error: 'scope', available: false, retryable: false });
    } else if (
      [
        'SESSION_SPECIALIST_TARGET_UNAVAILABLE',
        'SESSION_SPECIALIST_INVALID',
        'SESSION_SPECIALIST_COMMAND_CONFLICT',
      ].includes(code)
    ) {
      this.command = null;
      this.publish({
        error: code === 'SESSION_SPECIALIST_TARGET_UNAVAILABLE' ? 'unavailable' : 'invalid',
        retryable: false,
      });
    } else this.publish({ error: this.command ? 'uncertain' : 'read', retryable: Boolean(this.command) });
  }
  /** Synchronous: the ordinary-send owner freezes this before its first yield. */
  capture = (targetBranchId?: string | null): SpecialistSendAuthority => {
    if (!this.current() || this.command || this.intentUnconfirmed || this.view.writing)
      throw new SpecialistPreparationRequired();
    const snapshot = this.view.snapshot;
    if (!snapshot || snapshot.phase === 'legacy') return Object.freeze({});
    if (
      snapshot.preparation_error ||
      !sameSpecialistBranch(snapshot.branch, snapshot.prepared_branch) ||
      (targetBranchId && targetBranchId !== snapshot.branch.id)
    )
      throw new SpecialistPreparationRequired();
    return Object.freeze({
      reference: Object.freeze({
        scope: snapshot.scope,
        branch: snapshot.prepared_branch,
        revision: snapshot.revision,
        snapshot_id: snapshot.desired.snapshot_id,
      }),
      targetAgent: snapshot.desired.selection_name,
    });
  };
}
