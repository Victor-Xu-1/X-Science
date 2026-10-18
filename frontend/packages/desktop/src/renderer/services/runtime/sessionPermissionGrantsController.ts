import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { ipcBridge } from '@/common';
import type { IConversationListChangedEvent } from '@/common/adapter/ipcBridgeTypes';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import {
  freezeSessionGrantRevoke,
  loadSessionPermissionGrants,
  revokeSessionPermissionGrants,
  sameSessionGrantScope,
  sessionGrantIdentifier,
  type SessionGrantIdentity,
  type SessionGrantRevokeBody,
  type SessionGrantRevokeResult,
  type SessionGrantScope,
  type SessionGrantsSnapshot,
} from './sessionPermissionGrants';

export type SessionGrantsView = {
  snapshot: SessionGrantsSnapshot | null;
  loading: boolean;
  writing: boolean;
  error: 'read' | 'uncertain' | 'scope' | 'invalid' | null;
  retryable: boolean;
  results: SessionGrantRevokeResult[];
  available: boolean;
};
export const EMPTY_SESSION_GRANTS_VIEW: SessionGrantsView = {
  snapshot: null,
  loading: false,
  writing: false,
  error: null,
  retryable: false,
  results: [],
  available: false,
};
let nextControllerId = 0;

/** Presentation state only. The backend snapshot and exact observed grant IDs are the authority. */
export class SessionPermissionGrantsController {
  private readonly account = getRendererAccountScopeToken();
  readonly presentationKey: string;
  private readonly resetKey = `session-grants-${++nextControllerId}`;
  private listeners = new Set<() => void>();
  private observers = new Set<symbol>();
  private observationEpoch = 0;
  private unregisterAccount: (() => void) | null = null;
  private openings = new Set<symbol>();
  private openedBefore = false;
  private epoch = 0;
  private read: AbortController | null = null;
  private releaseInvalidation: (() => void) | null = null;
  private dirty = false;
  private command: { body: SessionGrantRevokeBody; scope: SessionGrantScope } | null = null;
  private writeSequence = 0;
  private view: SessionGrantsView = { ...EMPTY_SESSION_GRANTS_VIEW, available: true };

  constructor(readonly identity: SessionGrantIdentity) {
    this.presentationKey = JSON.stringify([
      this.account,
      identity.ownerId,
      identity.frameId,
      identity.frameIncarnationId,
      identity.rootFrameId,
    ]);
  }
  getSnapshot = (): SessionGrantsView => this.view;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  isCurrent = (): boolean =>
    this.account === getRendererAccountScopeToken() && this.identity.ownerId === getRendererAccountOwnerId();
  /** Composer lifetime owns read-only observation, never permission to submit a mutation. */
  attach(): () => void {
    if (!this.isCurrent()) return () => {};
    const observer = Symbol();
    this.observers.add(observer);
    if (this.observers.size === 1) {
      this.observationEpoch += 1;
      this.unregisterAccount = registerRendererAccountReset(this.resetKey, () => {
        this.stopObservation();
        this.command = null;
        this.publish({ ...EMPTY_SESSION_GRANTS_VIEW });
      });
      const release = [
        ipcBridge.conversation.listChanged.on(this.onPermissionGrantsChanged),
        ipcBridge.realtime.reconnected.on(this.invalidate),
      ];
      this.releaseInvalidation = () => release.forEach((dispose) => dispose());
      void this.refresh();
    }
    return () => {
      if (!this.observers.delete(observer)) return;
      if (!this.observers.size) this.stopObservation();
    };
  }
  private publish(patch: Partial<SessionGrantsView>): void {
    this.view = { ...this.view, ...patch };
    for (const listener of this.listeners) listener();
  }
  private stopObservation(): void {
    this.observers.clear();
    this.observationEpoch += 1;
    this.read?.abort();
    this.read = null;
    this.releaseInvalidation?.();
    this.releaseInvalidation = null;
    this.unregisterAccount?.();
    this.unregisterAccount = null;
    this.dirty = false;
    this.closePresentation();
    this.publish({ loading: false });
  }
  private closePresentation(): void {
    this.openings.clear();
    this.epoch += 1;
    this.writeSequence += 1;
    this.publish({
      writing: false,
      ...(this.command && this.view.available ? { error: 'uncertain', retryable: true } : {}),
    });
    this.refreshDirty();
  }
  open = (): (() => void) => {
    if (!this.isCurrent() || !this.view.available) return () => {};
    const opening = Symbol();
    this.openings.add(opening);
    if (this.openings.size === 1) {
      this.epoch += 1;
      if (this.openedBefore || !this.view.snapshot) void this.refresh();
      this.openedBefore = true;
    }
    return () => {
      if (!this.openings.delete(opening)) return;
      if (!this.openings.size) this.closePresentation();
    };
  };
  private current(epoch: number): boolean {
    return this.observing() && this.openings.size > 0 && this.epoch === epoch;
  }
  private observing(): boolean {
    return this.isCurrent() && this.view.available && this.observers.size > 0;
  }
  private onPermissionGrantsChanged = (event: IConversationListChangedEvent): void => {
    if (
      !event ||
      event.permission_grants_changed !== true ||
      event.action !== 'updated' ||
      event.source !== 'xscience' ||
      event.conversation_id !== this.identity.rootFrameId ||
      event.root_frame_id !== this.identity.rootFrameId ||
      !sessionGrantIdentifier(event.root_frame_incarnation_id)
    )
      return;
    // This is only an invalidation hint. Even an unlike incarnation triggers a
    // read under our original scope, never adoption of the event's identity.
    // During bootstrap, invalidate records one dirty read until its real scope arrives.
    this.invalidate();
  };
  private invalidate = (): void => {
    if (!this.observing()) return;
    if (this.read || this.view.writing) this.dirty = true;
    else void this.refresh();
  };
  refresh = async (): Promise<void> => {
    if (!this.observing() || this.read || this.view.writing) return;
    const epoch = this.observationEpoch;
    const read = new AbortController();
    this.read = read;
    this.publish({ loading: true, error: this.command ? 'uncertain' : null });
    try {
      const snapshot = await loadSessionPermissionGrants(this.identity, this.view.snapshot?.scope, read.signal);
      if (this.observing() && this.observationEpoch === epoch && this.read === read) this.publish({ snapshot });
    } catch (error) {
      if (this.observing() && this.observationEpoch === epoch && this.read === read) this.applyError(error, false);
    } finally {
      if (this.read === read) {
        this.read = null;
        if (this.observing() && this.observationEpoch === epoch) {
          this.publish({ loading: false });
          this.refreshDirty();
        }
      }
    }
  };
  revoke = (snapshot: SessionGrantsSnapshot, categoryKey?: string): Promise<void> => {
    if (
      !this.observing() ||
      !this.openings.size ||
      !this.view.available ||
      this.view.writing ||
      this.command ||
      !this.view.snapshot ||
      !sameSessionGrantScope(snapshot.scope, this.view.snapshot.scope)
    )
      return Promise.resolve();
    this.read?.abort();
    this.read = null;
    this.command = { body: freezeSessionGrantRevoke(snapshot, categoryKey), scope: { ...snapshot.scope } };
    return this.submit();
  };
  retry = (): Promise<void> => {
    if (!this.command || !this.view.retryable || this.view.writing) return Promise.resolve();
    return this.submit();
  };
  private submit = async (): Promise<void> => {
    if (!this.command || !this.observing() || !this.openings.size) return;
    const command = this.command;
    const epoch = this.epoch;
    const sequence = ++this.writeSequence;
    this.read?.abort();
    this.read = null;
    this.publish({ writing: true, loading: false, error: null, retryable: false, results: [] });
    try {
      const result = await revokeSessionPermissionGrants(this.identity, command.scope, command.body);
      if (!this.current(epoch) || sequence !== this.writeSequence) return;
      this.command = null;
      this.publish({ snapshot: result.snapshot, results: result.results, error: null, retryable: false });
    } catch (error) {
      if (this.current(epoch) && sequence === this.writeSequence) this.applyError(error, true);
    } finally {
      if (this.current(epoch) && sequence === this.writeSequence) {
        this.publish({ writing: false });
        this.refreshDirty();
      }
    }
  };
  private refreshDirty(): void {
    if (!this.dirty) return;
    this.dirty = false;
    void this.refresh();
  }
  private applyError(error: unknown, mutation: boolean): void {
    const code = isBackendHttpError(error) ? error.code : '';
    if (['AUTH_REQUIRED', 'SESSION_GRANT_TARGET_UNAVAILABLE', 'SESSION_GRANT_SCOPE_STALE'].includes(code)) {
      this.publish({ error: 'scope', available: false, retryable: false, loading: false, writing: false });
    } else if (code === 'SESSION_GRANT_REQUEST_INVALID') {
      this.publish({ error: 'invalid', available: false, retryable: false, loading: false, writing: false });
    } else {
      this.publish({ error: mutation || this.command ? 'uncertain' : 'read', retryable: Boolean(this.command) });
    }
  }
}
