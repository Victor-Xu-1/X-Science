import { ipcBridge } from '@/common';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import {
  changeSessionDelegationPolicy,
  freezeSessionDelegationCommand,
  loadSessionDelegationPolicy,
  sameSessionDelegationScope,
  SessionDelegationRevisionConflict,
  SessionDelegationScopeMismatch,
  type SessionDelegationCommand,
  type SessionDelegationIdentity,
  type SessionDelegationSnapshot,
} from './sessionDelegationPolicy';

export type SessionDelegationView = {
  snapshot: SessionDelegationSnapshot | null;
  loading: boolean;
  writing: boolean;
  error: 'read' | 'uncertain' | 'conflict' | 'scope' | 'invalid' | null;
  retryable: boolean;
  available: boolean;
};
export const EMPTY_SESSION_DELEGATION_VIEW: SessionDelegationView = {
  snapshot: null,
  loading: false,
  writing: false,
  error: null,
  retryable: false,
  available: false,
};
let nextOwner = 0;

/** Presentation/request ownership only; the durable backend policy is authoritative. */
export class SessionDelegationPolicyController {
  private readonly account = getRendererAccountScopeToken();
  private readonly resetKey = `session-delegation-${++nextOwner}`;
  private observers = new Set<symbol>();
  private listeners = new Set<() => void>();
  private epoch = 0;
  private read: AbortController | null = null;
  private readPromise: Promise<void> | null = null;
  private writePromise: Promise<void> | null = null;
  private release: (() => void) | null = null;
  private dirty = false;
  private command: SessionDelegationCommand | null = null;
  private view: SessionDelegationView = { ...EMPTY_SESSION_DELEGATION_VIEW, available: true };
  readonly presentationKey: string;
  constructor(readonly identity: SessionDelegationIdentity) {
    this.presentationKey = JSON.stringify([
      this.account,
      identity.ownerId,
      identity.frameId,
      identity.frameIncarnationId,
      identity.rootFrameId,
    ]);
  }
  getSnapshot = (): SessionDelegationView => this.view;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  current = (): boolean =>
    this.account === getRendererAccountScopeToken() && this.identity.ownerId === getRendererAccountOwnerId();
  private active(epoch = this.epoch): boolean {
    return this.current() && this.observers.size > 0 && this.epoch === epoch;
  }
  private publish(patch: Partial<SessionDelegationView>): void {
    this.view = { ...this.view, ...patch };
    for (const listener of this.listeners) listener();
  }
  attach(): () => void {
    if (!this.current()) return () => {};
    const token = Symbol();
    this.observers.add(token);
    if (this.observers.size === 1) {
      this.epoch += 1;
      const unregister = registerRendererAccountReset(this.resetKey, () => {
        this.stop();
        this.command = null;
        this.publish({ ...EMPTY_SESSION_DELEGATION_VIEW });
      });
      const releases = [
        ipcBridge.conversation.listChanged.on(this.onEvent),
        ipcBridge.realtime.reconnected.on(this.invalidate),
      ];
      if (typeof window !== 'undefined') window.addEventListener('focus', this.invalidate);
      if (typeof document !== 'undefined') document.addEventListener('visibilitychange', this.onVisibility);
      this.release = () => {
        unregister();
        releases.forEach((release) => release());
        if (typeof window !== 'undefined') window.removeEventListener('focus', this.invalidate);
        if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', this.onVisibility);
      };
      void this.refresh();
    }
    return () => {
      if (this.observers.delete(token) && !this.observers.size) this.stop();
    };
  }
  private stop(): void {
    this.observers.clear();
    this.epoch += 1;
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
  private onVisibility = (): void => {
    if (document.visibilityState === 'visible') this.invalidate();
  };
  private onEvent = (event: unknown): void => {
    if (!event || typeof event !== 'object') return;
    const row = event as Record<string, unknown>;
    if (
      row.action === 'updated' &&
      row.source === 'xscience' &&
      row.delegation_policy_changed === true &&
      row.conversation_id === this.identity.rootFrameId &&
      row.root_frame_id === this.identity.rootFrameId
    )
      this.invalidate();
  };
  private invalidate = (): void => {
    if (!this.active()) return;
    if (this.read || this.writePromise) this.dirty = true;
    else void this.refresh();
  };
  private accept(snapshot: SessionDelegationSnapshot): void {
    const previous = this.view.snapshot;
    if (
      previous &&
      (!sameSessionDelegationScope(previous.scope, snapshot.scope) || snapshot.revision < previous.revision)
    )
      return;
    if (previous && snapshot.revision === previous.revision && snapshot.policy !== previous.policy)
      throw new Error('Contradictory session delegation revision');
    this.publish({ snapshot });
  }
  refresh = (): Promise<void> => {
    if (!this.active() || !this.view.available || this.writePromise) return Promise.resolve();
    if (this.readPromise) return this.readPromise;
    const epoch = this.epoch,
      read = new AbortController();
    this.read = read;
    const promise = loadSessionDelegationPolicy(this.identity, this.view.snapshot?.scope, read.signal)
      .then((snapshot) => {
        if (this.active(epoch) && this.read === read) this.accept(snapshot);
      })
      .catch((error: unknown) => {
        if (this.active(epoch) && this.read === read) this.applyError(error);
      })
      .finally(() => {
        if (this.read === read) {
          this.read = null;
          this.readPromise = null;
          if (this.active(epoch)) {
            this.publish({ loading: false });
            this.refreshDirty();
          }
        }
      });
    this.readPromise = promise;
    this.publish({ loading: true, error: this.command ? 'uncertain' : null });
    return promise;
  };
  change = (enabled: boolean): Promise<void> => {
    if (this.writePromise) return this.writePromise;
    if (
      !this.active() ||
      !this.view.available ||
      !this.view.snapshot ||
      this.view.loading ||
      this.command ||
      this.view.error
    )
      return Promise.resolve();
    const desired = enabled ? 'allow' : 'deny';
    if (this.view.snapshot.policy === desired) return Promise.resolve();
    this.command = freezeSessionDelegationCommand(this.view.snapshot, desired);
    return this.submit();
  };
  retry = (): Promise<void> => {
    if (this.writePromise) return this.writePromise;
    return this.command && this.view.retryable ? this.submit() : this.refresh();
  };
  private submit(): Promise<void> {
    if (!this.command || !this.active() || !this.view.available) return Promise.resolve();
    const command = this.command,
      epoch = this.epoch;
    this.publish({ writing: true, loading: false, error: null, retryable: false });
    const promise = changeSessionDelegationPolicy(this.identity, command)
      .then((result) => {
        if (!this.active(epoch) || this.command !== command) return;
        this.accept(result.snapshot);
        this.command = null;
        this.publish({ error: null, retryable: false });
      })
      .catch((error: unknown) => {
        if (!this.active(epoch) || this.command !== command) return;
        if (error instanceof SessionDelegationRevisionConflict) {
          this.accept(error.snapshot);
          this.command = null;
          this.publish({ error: 'conflict', retryable: false });
        } else this.applyError(error);
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
    return promise;
  }
  private refreshDirty(): void {
    if (this.dirty) {
      this.dirty = false;
      void this.refresh();
    }
  }
  private applyError(error: unknown): void {
    const code = isBackendHttpError(error) ? error.code : '';
    if (
      error instanceof SessionDelegationScopeMismatch ||
      ['AUTH_REQUIRED', 'SESSION_DELEGATION_TARGET_UNAVAILABLE', 'SESSION_DELEGATION_SCOPE_STALE'].includes(code)
    ) {
      this.command = null;
      this.publish({ error: 'scope', available: false, retryable: false });
    } else if (['SESSION_DELEGATION_INVALID', 'SESSION_DELEGATION_COMMAND_CONFLICT'].includes(code)) {
      this.publish({ error: 'invalid', available: false, retryable: false });
    } else this.publish({ error: this.command ? 'uncertain' : 'read', retryable: Boolean(this.command) });
  }
}
