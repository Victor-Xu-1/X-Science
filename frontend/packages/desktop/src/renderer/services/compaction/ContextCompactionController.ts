import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '../rendererAccountScope';
import { observeContextCompaction } from './contextCompactionObservation';
import {
  readContextCompaction,
  lookupContextCompaction,
  submitContextCompaction,
  cancelContextCompaction,
} from './contextCompactionService';
import {
  compactionUnresolved,
  compactionTerminal,
  snapshotReleasesCompaction,
  type CompactionIdentity,
  type CompactionReceipt,
  type CompactionView,
} from './contextCompactionTypes';

export const compactionDependencies = {
  read: readContextCompaction,
  lookup: lookupContextCompaction,
  submit: submitContextCompaction,
  cancel: cancelContextCompaction,
  observe: observeContextCompaction,
  uuid: (): string => crypto.randomUUID(),
};
export class ContextCompactionController {
  private view: CompactionView = {
    snapshot: null,
    receipt: null,
    request: null,
    loading: false,
    mutation: null,
    uncertain: null,
    error: null,
    waitingForPrompt: false,
  };
  private listeners = new Set<() => void>();
  private account = getRendererAccountScopeToken();
  private epoch = 0;
  private releaseObservation?: () => void;
  private requests = new Set<AbortController>();
  private readPromise: Promise<void> | null = null;
  private writePromise: Promise<void> | null = null;
  private dirty = false;
  private targetMissing = false;
  private waiters = new Set<(safe: boolean) => void>();
  constructor(
    readonly identity: CompactionIdentity,
    private dependencies = compactionDependencies
  ) {}
  get enabled() {
    return Boolean(
      this.identity.ownerId && this.identity.frameId && this.identity.frameIncarnationId && this.identity.rootFrameId
    );
  }
  get retained() {
    return compactionUnresolved(this.view);
  }
  get attached() {
    return this.listeners.size > 0;
  }
  getSnapshot = () => this.view;
  current = (epoch = this.epoch) =>
    this.enabled &&
    this.attached &&
    epoch === this.epoch &&
    this.account === getRendererAccountScopeToken() &&
    this.identity.ownerId === getRendererAccountOwnerId();
  private publish(patch: Partial<CompactionView>) {
    this.view = { ...this.view, ...patch };
    this.listeners.forEach((listener) => listener());
    if (!compactionUnresolved(this.view) && !this.view.error) this.finishWaiters(true);
  }
  private finishWaiters(safe: boolean) {
    if (!this.waiters.size) return;
    const waiters = [...this.waiters];
    this.waiters.clear();
    this.view = { ...this.view, waitingForPrompt: false };
    waiters.forEach((resolve) => resolve(safe));
    this.listeners.forEach((listener) => listener());
  }
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1 && this.current()) {
      this.releaseObservation = this.dependencies.observe(this.identity, this.invalidate);
      void this.refresh();
    }
    return () => {
      this.listeners.delete(listener);
      if (!this.listeners.size) this.suspend();
    };
  };
  suspend = () => {
    this.epoch++;
    this.releaseObservation?.();
    this.releaseObservation = undefined;
    this.requests.forEach((request) => request.abort());
    this.requests.clear();
    this.readPromise = null;
    this.writePromise = null;
    this.dirty = false;
    this.finishWaiters(false);
    // Aborting an HTTP observation never proves that its mutation was withdrawn.
    this.view = { ...this.view, loading: false, uncertain: this.view.mutation ?? this.view.uncertain, mutation: null };
  };
  private controller() {
    const controller = new AbortController();
    this.requests.add(controller);
    return controller;
  }
  private receive(receipt: CompactionReceipt) {
    this.publish({ receipt, request: receipt.request, uncertain: null, error: null });
  }
  invalidate = () => {
    if (!this.current()) return;
    this.dirty = true;
    if (!this.readPromise && !this.writePromise) void this.refresh();
  };
  refresh = (): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.readPromise) return this.readPromise;
    if (this.writePromise) {
      this.dirty = true;
      return this.writePromise;
    }
    this.dirty = false;
    const epoch = this.epoch,
      controller = this.controller();
    this.publish({ loading: true });
    const operation = Promise.resolve().then(async () => {
      try {
        const snapshot = await this.dependencies.read(this.identity, controller.signal);
        if (!this.current(epoch)) return;
        const previous = this.view.snapshot?.target;
        if (previous && previous.root_frame_incarnation_id !== snapshot.target.root_frame_incarnation_id) {
          this.publish({ error: 'scope' });
          return;
        }
        this.targetMissing = false;
        const original = this.view.request;
        if (original && compactionUnresolved(this.view)) {
          const receipt = await this.dependencies.lookup(this.identity, original, controller.signal);
          if (this.current(epoch)) {
            const cancelUncertain =
              this.view.uncertain === 'cancel' &&
              receipt.cancellation_status === 'not_requested' &&
              !receipt.can_submit_next_input;
            const replacement =
              !cancelUncertain &&
              snapshot.operation?.request.request_id !== receipt.request.request_id &&
              snapshotReleasesCompaction(snapshot, receipt)
                ? snapshot.operation
                : null;
            const presented = replacement ?? receipt;
            this.publish({
              snapshot,
              receipt: presented,
              request: presented.request,
              uncertain: cancelUncertain ? 'cancel' : null,
              error: cancelUncertain ? 'cancel' : null,
            });
            if (replacement) this.finishWaiters(false);
          }
        } else {
          const presented = snapshot.operation ?? this.view.receipt;
          this.publish({ snapshot, receipt: presented, request: presented?.request ?? null, error: null });
        }
      } catch (error) {
        if (this.current(epoch)) {
          this.targetMissing = isBackendHttpError(error) && error.code === 'CONTEXT_COMPACTION_TARGET_UNAVAILABLE';
          this.publish({ error: isBackendHttpError(error) && error.status === 409 ? 'scope' : 'read' });
          this.finishWaiters(false);
        }
      } finally {
        this.requests.delete(controller);
        if (this.current(epoch) && this.readPromise === operation) {
          this.readPromise = null;
          this.publish({ loading: false });
          if (this.dirty) void this.refresh();
        }
      }
    });
    this.readPromise = operation;
    return operation;
  };
  start = async (): Promise<void> => {
    if (!this.current() || this.view.loading || this.view.error || compactionUnresolved(this.view)) return;
    // A new explicit run needs today's target, never the revision retained from
    // before a previous admission. Refreshing does not allocate a request UUID.
    if (this.view.receipt) await this.refresh();
    const snapshot = this.view.snapshot;
    if (
      !this.current() ||
      !snapshot?.availability.available ||
      this.view.loading ||
      this.view.error ||
      compactionUnresolved(this.view)
    )
      return;
    const request = Object.freeze({
      schema: 'x-science.context-compaction-request.v1' as const,
      request_id: this.dependencies.uuid(),
      expected: Object.freeze({ ...snapshot.target }),
    });
    this.publish({ request, receipt: null, uncertain: null });
    return this.mutate('submit');
  };
  retry = () => (this.view.uncertain ? this.mutate(this.view.uncertain) : this.refresh());
  private mutate(kind: 'submit' | 'cancel'): Promise<void> {
    if (this.writePromise) return this.writePromise;
    const request = this.view.request;
    if (!this.current() || !request || this.view.error === 'scope') return Promise.resolve();
    const epoch = this.epoch,
      controller = this.controller();
    this.publish({ mutation: kind, error: null });
    const operation = Promise.resolve().then(async () => {
      try {
        const receipt = await this.dependencies[kind](this.identity, request, controller.signal);
        if (this.current(epoch)) this.receive(receipt);
      } catch (error) {
        if (!this.current(epoch)) return;
        this.publish({ uncertain: kind, error: isBackendHttpError(error) && error.status === 409 ? 'scope' : kind });
        // Read back only this UUID. A missing receipt is not proof of withdrawal.
        try {
          const receipt = await this.dependencies.lookup(this.identity, request, controller.signal);
          if (this.current(epoch)) {
            this.receive(receipt);
            if (kind === 'cancel' && !receipt.can_submit_next_input && receipt.cancellation_status === 'not_requested')
              this.publish({ uncertain: 'cancel', error: 'cancel' });
          }
        } catch {
          /* Keep the original command and explicit same-body retry. */
        }
      } finally {
        this.requests.delete(controller);
        if (this.current(epoch) && this.writePromise === operation) {
          this.writePromise = null;
          this.publish({ mutation: null });
          if (this.view.uncertain || this.view.error === 'scope') this.finishWaiters(false);
          if (this.dirty) void this.refresh();
        }
      }
    });
    this.writePromise = operation;
    return operation;
  }
  /** Returns true when this Stop belongs exclusively to maintenance. */
  stop = async (): Promise<boolean> => {
    if (!this.enabled) return false;
    const epoch = this.epoch;
    if (!this.current(epoch)) return true;
    if (!this.view.request) return false;
    if (this.readPromise) await this.readPromise;
    if (!this.current(epoch)) return true;
    if (!compactionUnresolved(this.view)) return false;
    if (this.writePromise) await this.writePromise;
    if (
      this.current(epoch) &&
      compactionUnresolved(this.view) &&
      !this.view.receipt?.can_submit_next_input &&
      (!this.view.receipt || this.view.receipt.cancellation_status === 'not_requested')
    )
      await this.mutate('cancel');
    return true;
  };
  prepareForPrompt = async (): Promise<boolean> => {
    if (!this.enabled) return true;
    const epoch = this.epoch;
    if (!this.current(epoch)) return false;
    // Bootstrap is optional for ordinary input. The ordinary POST/queue backend
    // still preempts exact maintenance; only an observed local request gates here.
    if (!this.view.request) return true;
    if (this.readPromise) await this.readPromise;
    if (!this.current(epoch)) return false;
    if (
      this.view.receipt &&
      compactionTerminal(this.view.receipt) &&
      !this.view.receipt.can_submit_next_input &&
      !this.view.uncertain
    )
      await this.refresh();
    if (!this.current(epoch)) return false;
    if (this.view.error && !(this.targetMissing && !this.view.request)) return false;
    if (!compactionUnresolved(this.view)) return true;
    const requestId = this.view.request?.request_id;
    await this.stop();
    if (!this.current(epoch) || this.view.uncertain || this.view.error) return false;
    if (this.view.receipt && compactionTerminal(this.view.receipt) && !this.view.receipt.can_submit_next_input)
      await this.refresh();
    if (!this.current(epoch) || this.view.uncertain || this.view.error || this.view.request?.request_id !== requestId)
      return false;
    if (!compactionUnresolved(this.view)) return true;
    return new Promise((resolve) => {
      this.waiters.add(resolve);
      this.publish({ waitingForPrompt: true });
    });
  };
  preparePromptIntent = async (): Promise<{ safe: boolean; preempted: boolean }> => {
    const epoch = this.epoch;
    if (!this.view.request) return { safe: this.current(epoch), preempted: false };
    this.publish({ waitingForPrompt: true });
    try {
      if (this.readPromise) await this.readPromise;
      const requestId = this.view.request?.request_id;
      const ownedAtIntent = compactionUnresolved(this.view);
      const safe = await this.prepareForPrompt();
      const receipt = this.view.receipt;
      const preempted = Boolean(
        ownedAtIntent &&
        this.view.request?.request_id === requestId &&
        receipt?.can_submit_next_input &&
        !snapshotReleasesCompaction(this.view.snapshot, receipt)
      );
      return { safe: safe && this.current(epoch) && !compactionUnresolved(this.view), preempted };
    } finally {
      if (this.current(epoch)) this.publish({ waitingForPrompt: false });
    }
  };
}
