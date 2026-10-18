export type DelegatedStopScope = {
  parent_frame_id: string;
  parent_frame_incarnation_id: string;
  root_frame_id: string;
  root_frame_incarnation_id: string;
};
export type DelegatedStopBranch = { branch_id: string; generation: number };
export type DelegatedStopTarget = {
  frame_id: string;
  frame_incarnation_id: string;
  stream_uid: string;
  stream_epoch: number;
  input_message_id: string;
  input_revision: number;
  runner_attempt: number;
};
export type DelegatedStopRequest = {
  schema: 'x-science.delegated-stop.v1';
  request_id: string;
  scope: DelegatedStopScope;
  branch: DelegatedStopBranch;
  parent_input_revision: number;
  expected_fence_generation: number;
  mode: 'branch' | 'child';
  target?: DelegatedStopTarget;
};
export type DelegatedStopSnapshot = {
  schema: 'x-science.delegated-stop-snapshot.v1';
  scope: DelegatedStopScope;
  branch: DelegatedStopBranch;
  parent_input_revision: number;
  fence_generation: number;
  can_stop_branch: boolean;
  targets: Array<{
    target: DelegatedStopTarget | null;
    frame_id: string;
    origin: 'verified' | 'legacy-unavailable';
    in_selected_branch: boolean;
    can_stop: boolean;
    status: 'running' | 'awaiting_input' | 'terminal' | 'unavailable';
    reason?: string;
  }>;
};
export type DelegatedStopReceipt = {
  schema: 'x-science.delegated-stop-receipt.v1';
  request: DelegatedStopRequest;
  accepted_at: string;
  state: 'stopping' | 'settled';
  replayed: boolean;
  targets: Array<{
    target: DelegatedStopTarget;
    outcome: 'cancel_requested' | 'cancelled' | 'already_terminal';
    execution_observation: 'running' | 'stopped' | 'unavailable';
    diagnostic?: string;
  }>;
};
export type DelegatedStopIdentity = { ownerId: string; parentId: string; frameIncarnationId: string };
export type DelegatedStopIssue =
  | 'unavailable'
  | 'stale'
  | 'conflict'
  | 'invalid'
  | 'readFailed'
  | 'unconfirmed'
  | 'capacity';
export type DelegatedStopOperation = {
  request: DelegatedStopRequest;
  phase: 'sending' | 'checking' | 'uncertain' | 'stopping' | 'settled' | 'rejected';
  receipt: DelegatedStopReceipt | null;
  issue: DelegatedStopIssue | null;
};
export type DelegatedStopView = {
  snapshot: DelegatedStopSnapshot | null;
  loading: boolean;
  issue: DelegatedStopIssue | null;
  operation: DelegatedStopOperation | null;
  recoveryLimited: boolean;
};
