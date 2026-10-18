import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '../rendererAccountScope';
import { decodeDelegatedStopRequest, sameDelegatedStopCoordinates } from './delegatedStopContract';
import { DELEGATED_STOP_HINT_LIMIT, readDelegatedStopHints, saveDelegatedStopHints } from './delegatedStopHints';
import {
  delegatedStopDefinitelyRejected,
  delegatedStopIssue,
  readDelegatedStopReceipt,
  readDelegatedStopSnapshot,
  submitDelegatedStop,
} from './delegatedStopTransport';
import type {
  DelegatedStopIdentity,
  DelegatedStopOperation,
  DelegatedStopRequest,
  DelegatedStopScope,
  DelegatedStopSnapshot,
  DelegatedStopView,
} from './delegatedStopTypes';

const initialView: DelegatedStopView = {
  snapshot: null,
  loading: false,
  issue: null,
  operation: null,
  recoveryLimited: false,
};
export class DelegatedStopController {
  readonly token = getRendererAccountScopeToken();
  private listeners = new Set<() => void>();
  private operations: DelegatedStopOperation[] = [];
  private view: DelegatedStopView = initialView;
  private branchId: string | null = null;
  private branchRevision = 0;
  private epoch = 0;
  private disposed = false;
  private scope: DelegatedStopScope | null = null;
  private hintsWritable = true;
  private readFlight: { epoch: number; dirty: boolean; promise: Promise<void> } | null = null;

  constructor(readonly identity: DelegatedStopIdentity) {
    const hints = readDelegatedStopHints(identity, this.token);
    this.hintsWritable = !hints.limited;
    this.operations = hints.requests.map((request): DelegatedStopOperation => ({
      request,
      phase: 'uncertain',
      receipt: null,
      issue: 'unconfirmed',
    }));
    this.view = { ...initialView, recoveryLimited: hints.limited };
  }
  getSnapshot = (): DelegatedStopView => this.view;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private current(): boolean {
    return (
      !this.disposed &&
      getRendererAccountOwnerId() === this.identity.ownerId &&
      getRendererAccountScopeToken() === this.token
    );
  }
  private update(next: Partial<DelegatedStopView> = {}): void {
    if (!this.current()) return;
    const selected =
      this.branchId ??
      next.snapshot?.branch.branch_id ??
      this.view.snapshot?.branch.branch_id ??
      this.operations.at(-1)?.request.branch.branch_id;
    this.view = {
      ...this.view,
      ...next,
      operation: this.operations.findLast((operation) => operation.request.branch.branch_id === selected) ?? null,
    };
    this.listeners.forEach((listener) => listener());
  }
  private saveHints(): void {
    if (!this.hintsWritable) return;
    const requests = this.operations
      .filter((operation) => operation.phase !== 'settled' && operation.phase !== 'rejected')
      .map((operation) => operation.request);
    if (!saveDelegatedStopHints(this.identity, this.token, requests)) this.update({ recoveryLimited: true });
  }
  selectBranch(branchId: string | null, revision: number): void {
    if (this.branchId === branchId && this.branchRevision === revision) return;
    this.branchId = branchId;
    this.branchRevision = revision;
    this.epoch += 1;
    this.update({ snapshot: null, loading: false, issue: null });
  }
  async refresh(): Promise<void> {
    if (!this.current()) return;
    const epoch = this.epoch;
    if (this.readFlight?.epoch === epoch) {
      this.readFlight.dirty = true;
      return this.readFlight.promise;
    }
    const branchId = this.branchId;
    this.update({ loading: true });
    const flight = { epoch, dirty: false, promise: Promise.resolve() };
    this.readFlight = flight;
    flight.promise = (async () => {
      try {
        const snapshot = await readDelegatedStopSnapshot(this.identity.parentId, branchId);
        if (!this.current() || epoch !== this.epoch) return;
        if (
          snapshot.scope.parent_frame_id !== this.identity.parentId ||
          snapshot.scope.parent_frame_incarnation_id !== this.identity.frameIncarnationId ||
          (branchId !== null && snapshot.branch.branch_id !== branchId) ||
          (this.scope && JSON.stringify(this.scope) !== JSON.stringify(snapshot.scope))
        ) {
          this.update({ snapshot: null, issue: 'stale' });
          return;
        }
        this.scope = snapshot.scope;
        this.update({ snapshot, issue: null });
      } catch (error) {
        if (this.current() && epoch === this.epoch) this.update({ snapshot: null, issue: delegatedStopIssue(error) });
      } finally {
        if (this.readFlight === flight) this.readFlight = null;
        if (this.current() && epoch === this.epoch) {
          this.update({ loading: false });
          if (flight.dirty) void this.refresh();
        }
      }
    })();
    return flight.promise;
  }
  async observe(): Promise<void> {
    await this.refresh();
    if (!this.current()) return;
    const operation = this.view.operation;
    if (operation && !['sending', 'checking', 'settled', 'rejected'].includes(operation.phase)) {
      await this.check(operation.request.request_id);
    }
  }
  start(snapshot: DelegatedStopSnapshot | null, childFrameId?: string): void {
    if (!this.current()) return;
    const latest = this.view.snapshot;
    if (!latest || this.view.loading || this.view.issue) return;
    if (!snapshot || !sameDelegatedStopCoordinates(snapshot, latest, childFrameId)) {
      this.update({ issue: 'stale' });
      return;
    }
    // Never create another intent while this branch still owns an unresolved one.
    if (
      this.operations.some(
        (operation) =>
          operation.request.branch.branch_id === snapshot.branch.branch_id &&
          operation.phase !== 'settled' &&
          operation.phase !== 'rejected'
      )
    )
      return;
    const child = childFrameId ? snapshot.targets.find((row) => row.frame_id === childFrameId) : undefined;
    const currentChild = childFrameId ? latest.targets.find((row) => row.frame_id === childFrameId) : undefined;
    if (
      childFrameId
        ? !child?.can_stop || !child.target || !currentChild?.can_stop
        : !snapshot.can_stop_branch || !latest.can_stop_branch
    )
      return;
    const retained = this.operations.filter(
      (operation) => operation.phase !== 'settled' && operation.phase !== 'rejected'
    );
    if (retained.length >= DELEGATED_STOP_HINT_LIMIT) {
      this.update({ issue: 'capacity' });
      return;
    }
    let request: DelegatedStopRequest;
    try {
      request = decodeDelegatedStopRequest({
        schema: 'x-science.delegated-stop.v1',
        request_id: globalThis.crypto.randomUUID(),
        scope: snapshot.scope,
        branch: snapshot.branch,
        parent_input_revision: snapshot.parent_input_revision,
        expected_fence_generation: snapshot.fence_generation,
        mode: childFrameId ? 'child' : 'branch',
        ...(child?.target ? { target: child.target } : {}),
      });
    } catch {
      this.update({ issue: 'invalid' });
      return;
    }
    const operation: DelegatedStopOperation = { request, phase: 'sending', receipt: null, issue: null };
    this.operations = [...retained, operation];
    this.saveHints();
    this.update();
    void this.send(operation, true);
  }
  private async send(operation: DelegatedStopOperation, firstSubmission = false): Promise<void> {
    try {
      const receipt = await submitDelegatedStop(operation.request);
      if (!this.current()) return;
      operation.receipt = receipt;
      operation.phase = receipt.state;
      operation.issue = null;
      this.saveHints();
      if ((this.branchId ?? this.view.snapshot?.branch.branch_id) === operation.request.branch.branch_id) {
        void this.refresh();
      }
    } catch (error) {
      if (!this.current()) return;
      const rejected = delegatedStopDefinitelyRejected(error);
      operation.phase = firstSubmission && rejected ? 'rejected' : 'uncertain';
      operation.issue = rejected ? delegatedStopIssue(error) : 'unconfirmed';
      this.saveHints();
      if (
        operation.phase === 'rejected' &&
        (this.branchId ?? this.view.snapshot?.branch.branch_id) === operation.request.branch.branch_id
      ) {
        this.update({ snapshot: null });
        void this.refresh();
      }
    } finally {
      if (this.current()) this.update();
    }
  }
  async check(requestId: string): Promise<void> {
    const operation = this.operations.find((entry) => entry.request.request_id === requestId);
    if (!this.current() || !operation || ['sending', 'checking'].includes(operation.phase)) return;
    operation.phase = 'checking';
    this.update();
    try {
      const receipt = await readDelegatedStopReceipt(operation.request);
      if (!this.current()) return;
      operation.receipt = receipt;
      operation.phase = receipt.state;
      operation.issue = null;
      this.saveHints();
    } catch (error) {
      if (!this.current()) return;
      operation.phase = 'uncertain';
      operation.issue = delegatedStopIssue(error);
    } finally {
      if (this.current()) this.update();
    }
  }
  retry(requestId: string): void {
    const operation = this.operations.find((entry) => entry.request.request_id === requestId);
    if (!this.current() || !operation || !['uncertain', 'stopping'].includes(operation.phase)) return;
    operation.phase = 'sending';
    operation.issue = null;
    this.update();
    void this.send(operation);
  }
  canRelease(): boolean {
    return (
      this.listeners.size === 0 &&
      this.operations.every((operation) => ['settled', 'rejected'].includes(operation.phase))
    );
  }
  dispose(): void {
    this.disposed = true;
    this.epoch += 1;
    this.listeners.clear();
  }
}
