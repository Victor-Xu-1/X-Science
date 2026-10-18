import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '../rendererAccountScope';
import { sameNotebookIdentity, sameNotebookTarget } from './manualNotebookContract';
import type { XScienceExecutionRecord } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import type {
  ManualNotebookPendingSummary,
  ManualNotebookReceipt,
  ManualNotebookRequest,
  ManualNotebookScope,
  ManualNotebookTarget,
} from './manualNotebookTypes';

export type NotebookError =
  | 'loadFailed'
  | 'pendingFailed'
  | 'targetChanged'
  | 'invalidCode'
  | 'sendUncertain'
  | 'readFailed'
  | 'notAccepted'
  | 'cancelUncertain'
  | 'sessionLimit';
export type NotebookRequestEntry = {
  id: string;
  target: ManualNotebookTarget;
  request: ManualNotebookRequest | null;
  receipt: ManualNotebookReceipt | null;
  summary: ManualNotebookPendingSummary | null;
  phase: 'submitting' | 'uncertain' | 'accepted';
  error: NotebookError | null;
  cancelIntent: boolean;
  cancelling: boolean;
  cancelIssued: boolean;
};
export type NotebookSessionSnapshot = {
  status: 'loading' | 'ready' | 'error' | 'scopeChanged';
  scope: ManualNotebookScope | null;
  targets: ManualNotebookTarget[];
  busyKernelIds: string[];
  selectedKey: string | null;
  draft: string;
  requests: NotebookRequestEntry[];
  error: NotebookError | null;
  commandBusy: boolean;
  history: XScienceExecutionRecord[];
  historyBefore: string | null;
  historyLoading: boolean;
  historyError: boolean;
  scanComplete: boolean;
  nextCursor: string | null;
  throughSequence: number | null;
  scanning: boolean;
};
export const notebookTargetKey = (target: ManualNotebookTarget): string =>
  JSON.stringify([
    target.frameId,
    target.frameIncarnationId,
    target.rootFrameIncarnationId,
    target.kernelId,
    target.kernelGeneration,
  ]);
export class NotebookSessionStore {
  readonly account = getRendererAccountScopeToken();
  readonly ownerId = getRendererAccountOwnerId();
  readonly entries = new Map<string, NotebookRequestEntry>();
  readonly listeners = new Set<() => void>();
  readonly reads = new Set<AbortController>();
  snapshot: NotebookSessionSnapshot = {
    status: 'loading',
    scope: null,
    targets: [],
    busyKernelIds: [],
    selectedKey: null,
    draft: '',
    requests: [],
    error: null,
    commandBusy: false,
    history: [],
    historyBefore: null,
    historyLoading: false,
    historyError: false,
    scanComplete: false,
    nextCursor: null,
    throughSequence: null,
    scanning: false,
  };
  disposed = false;
  opening = 0;
  refreshPromise: Promise<void> | null = null;
  inventoryPromise: Promise<boolean> | null = null;
  historyBusy = false;
  scanBusy = false;
  readRequests = new Set<string>();
  preferredKernelId?: string;
  constructor(
    readonly frameId: string,
    readonly rootFrameId: string
  ) {}
  current = () => !this.disposed && this.account === getRendererAccountScopeToken();
  visible = () => this.current() && this.listeners.size > 0;
  getSnapshot = () => this.snapshot;
  publish(patch: Partial<NotebookSessionSnapshot> = {}) {
    if (!this.current()) return;
    this.snapshot = { ...this.snapshot, ...patch, requests: [...this.entries.values()] };
    this.listeners.forEach((listener) => listener());
  }
  selected() {
    return this.snapshot.targets.find((target) => notebookTargetKey(target) === this.snapshot.selectedKey) ?? null;
  }
  isLive(target: ManualNotebookTarget) {
    return (
      this.snapshot.status === 'ready' &&
      this.snapshot.targets.some((candidate) => sameNotebookTarget(candidate, target))
    );
  }
  select(key: string) {
    if (!this.visible() || !this.snapshot.targets.some((target) => notebookTargetKey(target) === key)) return;
    this.publish({ selectedKey: key, draft: '' });
  }
  setDraft(code: string) {
    if (this.visible()) this.publish({ draft: code });
  }
  unresolved(target?: ManualNotebookTarget) {
    return [...this.entries.values()].some(
      (entry) => entry.receipt?.state !== 'terminal' && (!target || sameNotebookTarget(target, entry.target))
    );
  }
  update(id: string, patch: Partial<NotebookRequestEntry>) {
    const entry = this.entries.get(id);
    if (!entry || !this.current()) return;
    this.entries.set(id, { ...entry, ...patch });
    this.publish();
  }
  receive(id: string, receipt: ManualNotebookReceipt) {
    const entry = this.entries.get(id);
    if (
      !this.current() ||
      !entry ||
      !sameNotebookTarget(entry.target, receipt.target) ||
      (this.ownerId && receipt.ownerId !== this.ownerId) ||
      !this.snapshot.scope ||
      !sameNotebookIdentity(this.snapshot.scope, receipt.target)
    )
      throw new Error('manual_notebook_receipt_scope_changed');
    if (
      entry.summary &&
      (entry.summary.executionId !== receipt.executionId ||
        entry.summary.requestKind !== receipt.requestKind ||
        receipt.stateVersion < entry.summary.stateVersion)
    )
      throw new Error('manual_notebook_receipt_summary_mismatch');
    if (
      entry.receipt &&
      (entry.receipt.executionId !== receipt.executionId || entry.receipt.stateVersion > receipt.stateVersion)
    )
      return;
    this.update(id, { receipt, request: receipt.request, phase: 'accepted', error: null });
  }
  retireView() {
    this.opening += 1;
    this.reads.forEach((read) => read.abort());
    this.reads.clear();
  }
  dispose() {
    this.disposed = true;
    this.retireView();
    this.listeners.clear();
  }
  async read<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T | null> {
    if (!this.visible()) return null;
    const opening = this.opening,
      controller = new AbortController();
    this.reads.add(controller);
    try {
      const result = await operation(controller.signal);
      return this.visible() && opening === this.opening && !controller.signal.aborted ? result : null;
    } finally {
      this.reads.delete(controller);
    }
  }
}
