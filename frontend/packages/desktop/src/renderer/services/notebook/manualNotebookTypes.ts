export type NotebookIdentity = {
  frameId: string;
  rootFrameId: string;
  frameIncarnationId: string;
  rootFrameIncarnationId: string;
};
export type ManualNotebookScope = NotebookIdentity & { schema: 'x-science.manual-kernel-scope.v1' };
export type ManualNotebookTarget = NotebookIdentity & {
  schema: 'x-science.manual-kernel-target.v1';
  kernelId: string;
  kernelGeneration: number;
  language: 'python' | 'r';
  environment: string;
};
export type ManualNotebookRequest = Readonly<{
  schema: 'x-science.manual-kernel-request.v1';
  request_id: string;
  expected_frame_incarnation_id: string;
  expected_root_frame_id: string;
  expected_root_frame_incarnation_id: string;
  kernel_id: string;
  expected_kernel_generation: number;
  language: 'python' | 'r';
  environment: string;
  code: string;
  timeout_ms: number;
}>;
export type ManualNotebookState = 'queued' | 'started' | 'cancel_requested' | 'outcome_unknown' | 'terminal';
export type ManualNotebookRequestKind = 'typed-v1' | 'legacy';
export type ManualNotebookDisposition =
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'cancelled_before_start'
  | 'rejected_before_start';
export type ManualNotebookReceipt = {
  requestId: string;
  executionId: string;
  ownerId: string;
  requestKind: ManualNotebookRequestKind;
  request: ManualNotebookRequest | null;
  target: ManualNotebookTarget;
  state: ManualNotebookState;
  stateVersion: number;
  acceptedAt: string;
  startedAt: string | null;
  finishedAt: string | null;
  disposition: ManualNotebookDisposition | null;
  source: string;
  rejection: { stdout: string; stderr: string } | null;
  physical: { status: 'queued' | 'running' | 'persisting' | 'unavailable'; sampledAt: string; reason: string | null };
};
export type ManualNotebookPendingSummary = Pick<
  ManualNotebookReceipt,
  'requestId' | 'executionId' | 'requestKind' | 'target' | 'state' | 'stateVersion' | 'acceptedAt'
>;
export type ManualNotebookPendingPage = {
  pending: ManualNotebookPendingSummary[];
  nextCursor: string | null;
  scanComplete: boolean;
  throughSequence: number;
};
export type NotebookSessionNavigation = {
  frameId: string;
  rootFrameId: string;
  preferredKernelId?: string;
  focusRequest?: string;
};
