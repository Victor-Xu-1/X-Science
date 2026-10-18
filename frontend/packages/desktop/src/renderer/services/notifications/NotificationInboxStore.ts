import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import { observeNotificationInbox } from './notificationInboxObservation';
import { readNotificationInbox } from './notificationInboxService';
import { openInboxTarget, retryInboxMutation, startInboxMutation } from './notificationInboxActions';
import type { InboxMutation, InboxSnapshot, InboxView } from './notificationInboxTypes';

export class NotificationInboxStore {
  readonly listeners = new Set<() => void>();
  readonly requests = new Set<AbortController>();
  command: InboxMutation | null = null;
  openingVersion = 0;
  snapshot: InboxView = this.empty();
  readPromise: Promise<void> | null = null;
  writePromise: Promise<void> | null = null;
  private releaseObservation: (() => void) | null = null;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private needsRefresh = false;
  private readGeneration = 0;
  constructor(private observe = observeNotificationInbox) {}
  private empty(): InboxView {
    return {
      account: getRendererAccountScopeToken(),
      enabled: Boolean(getRendererAccountOwnerId()),
      data: null,
      status: 'idle',
      readError: false,
      writeStatus: 'idle',
      writeError: false,
      targetError: false,
      openingTarget: null,
      openBellId: null,
    };
  }
  current(account = this.snapshot.account) {
    return account === this.snapshot.account && account === getRendererAccountScopeToken();
  }
  getSnapshot = () => this.snapshot;
  publish(patch: Partial<InboxView>) {
    this.snapshot = { ...this.snapshot, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  receive(data: InboxSnapshot) {
    if (this.snapshot.data && data.revision < this.snapshot.data.revision) {
      this.publish({ status: 'ready', readError: false });
      return;
    }
    this.publish({ data, status: 'ready', readError: false });
  }
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1) this.start();
    return () => {
      this.listeners.delete(listener);
      if (!this.listeners.size) {
        this.openingVersion++;
        this.releaseObservation?.();
        this.releaseObservation = null;
        clearTimeout(this.timer);
        this.timer = undefined;
        this.publish({ openBellId: null });
      }
    };
  };
  private start() {
    if (!this.snapshot.enabled) return;
    this.releaseObservation = this.observe(this.invalidate);
    void this.refresh();
  }
  reset = () => {
    this.openingVersion++;
    this.readGeneration++;
    this.releaseObservation?.();
    this.releaseObservation = null;
    clearTimeout(this.timer);
    this.timer = undefined;
    this.requests.forEach((request) => request.abort());
    this.requests.clear();
    this.command = null;
    this.readPromise = null;
    this.writePromise = null;
    this.needsRefresh = false;
    this.snapshot = this.empty();
    this.listeners.forEach((listener) => listener());
    if (this.listeners.size) this.start();
  };
  invalidate = () => {
    if (!this.snapshot.enabled || !this.listeners.size) return;
    this.needsRefresh = true;
    if (this.timer || this.readPromise || this.writePromise) return;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      if (this.needsRefresh && this.listeners.size) {
        this.needsRefresh = false;
        void this.refresh();
      }
    }, 150);
  };
  requestController() {
    const controller = new AbortController();
    this.requests.add(controller);
    return controller;
  }
  releaseRequest(controller: AbortController) {
    this.requests.delete(controller);
  }
  settleInvalidation() {
    if (this.needsRefresh) this.invalidate();
  }
  refresh = (cursor: string | null = null): Promise<void> => {
    if (!this.current() || !this.snapshot.enabled || this.readPromise) return this.readPromise ?? Promise.resolve();
    const account = this.snapshot.account,
      generation = ++this.readGeneration,
      controller = this.requestController();
    this.publish({ status: 'loading', readError: false });
    const operation = (async () => {
      try {
        const data = await readNotificationInbox(cursor, { signal: controller.signal });
        if (this.current(account) && generation === this.readGeneration) this.receive(data);
      } catch {
        if (this.current(account) && generation === this.readGeneration)
          this.publish({ status: 'error', readError: true });
      } finally {
        this.releaseRequest(controller);
        if (this.current(account) && this.readPromise === operation) {
          this.readPromise = null;
          this.settleInvalidation();
        }
      }
    })();
    this.readPromise = operation;
    return operation;
  };
  loadMore = () => (this.snapshot.data?.nextCursor ? this.refresh(this.snapshot.data.nextCursor) : Promise.resolve());
  open = (id: string) => {
    if (!this.snapshot.enabled) return;
    this.openingVersion++;
    this.publish({ openBellId: id, targetError: false });
    void this.refresh();
  };
  close = (id: string) => {
    if (this.snapshot.openBellId === id) {
      this.openingVersion++;
      this.publish({ openBellId: null });
    }
  };
  markRead = (ids: readonly string[]) =>
    startInboxMutation(this, Object.freeze({ kind: 'read', ids: Object.freeze([...new Set(ids)]) }));
  markAllRead = (presentedReceipt: string) => {
    const data = this.snapshot.data;
    if (!data || data.unreadCount === 0) return Promise.resolve(false);
    return startInboxMutation(this, Object.freeze({ kind: 'read-all', receipt: presentedReceipt }));
  };
  retryWrite = () => retryInboxMutation(this);
  openTarget = (id: string, navigate: (path: string) => void | Promise<void>) => openInboxTarget(this, id, navigate);
}
export const notificationInboxStore = new NotificationInboxStore();
registerRendererAccountReset('notification-inbox', notificationInboxStore.reset);
