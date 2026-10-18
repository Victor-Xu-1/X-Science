export type CompactionTarget = Readonly<{
  frame_incarnation_id: string;
  root_frame_id: string;
  root_frame_incarnation_id: string;
  stream_uid: string;
  stream_epoch: number;
  branch_id: string;
  branch_generation: number;
  input_revision: number;
  through_publication_seq: number;
}>;
export type CompactionRequest = Readonly<{
  schema: 'x-science.context-compaction-request.v1';
  request_id: string;
  expected: CompactionTarget;
}>;
export type CompactionReceipt = {
  schema: 'x-science.context-compaction-receipt.v1';
  request: CompactionRequest;
  frame_id: string;
  accepted: boolean;
  input_message_id?: string;
  input_revision?: number;
  state: 'accepted' | 'compacting' | 'committed' | 'cancelled' | 'failed' | 'recovery_needed';
  attempt?: number;
  boundary_event_id?: number;
  terminal_event_id?: number;
  cancellation_status: 'not_requested' | 'requested' | 'cancelled_before_acceptance' | 'cancelled' | 'already_terminal';
  execution_observation: 'not_started' | 'active' | 'draining' | 'settled' | 'unavailable';
  can_submit_next_input: boolean;
  archive_status?: 'available' | 'pending';
  replayed: boolean;
};
export type CompactionSnapshot = {
  schema: 'x-science.context-compaction-snapshot.v1';
  target: CompactionTarget;
  availability: { available: boolean; reason: 'idle' | 'active' | 'queued' | 'waiting' | 'archived' | 'unavailable' };
  operation: CompactionReceipt | null;
};
export type CompactionIdentity = Readonly<{
  ownerId: string;
  frameId: string;
  frameIncarnationId: string;
  rootFrameId: string;
}>;
export type CompactionView = {
  snapshot: CompactionSnapshot | null;
  receipt: CompactionReceipt | null;
  request: CompactionRequest | null;
  loading: boolean;
  mutation: 'submit' | 'cancel' | null;
  uncertain: 'submit' | 'cancel' | null;
  error: 'read' | 'submit' | 'cancel' | 'scope' | null;
  waitingForPrompt: boolean;
};
export const compactionTerminal = (receipt: CompactionReceipt): boolean =>
  receipt.state === 'committed' || receipt.state === 'cancelled' || receipt.state === 'failed';

/** Matches the backend's immutable frame/root/stream identity, not mutable branch coordinates. */
export const sameCompactionScope = (left: CompactionTarget, right: CompactionTarget): boolean =>
  left.frame_incarnation_id === right.frame_incarnation_id &&
  left.root_frame_id === right.root_frame_id &&
  left.root_frame_incarnation_id === right.root_frame_incarnation_id &&
  left.stream_uid === right.stream_uid &&
  left.stream_epoch === right.stream_epoch;

/** Releases only presentation ownership; ordinary queue/runtime gates remain authoritative. */
export function snapshotReleasesCompaction(snapshot: CompactionSnapshot | null, receipt: CompactionReceipt): boolean {
  if (
    !snapshot ||
    !compactionTerminal(receipt) ||
    receipt.execution_observation === 'active' ||
    receipt.execution_observation === 'draining' ||
    !sameCompactionScope(snapshot.target, receipt.request.expected)
  )
    return false;
  const preacceptTombstone =
    !receipt.accepted &&
    receipt.state === 'cancelled' &&
    receipt.cancellation_status === 'cancelled_before_acceptance' &&
    receipt.execution_observation === 'not_started';
  const next = snapshot.operation;
  if (preacceptTombstone)
    return (
      snapshot.target.input_revision >= receipt.request.expected.input_revision &&
      !(next?.request.request_id === receipt.request.request_id && next.accepted)
    );
  const revision = receipt.input_revision;
  if (!receipt.accepted || revision === undefined || snapshot.target.input_revision < revision) return false;
  if (next && next.request.request_id !== receipt.request.request_id) {
    return Boolean(
      next.accepted &&
      next.input_revision &&
      sameCompactionScope(snapshot.target, next.request.expected) &&
      next.input_revision > revision &&
      next.input_revision <= snapshot.target.input_revision
    );
  }
  if (
    next &&
    (!compactionTerminal(next) || next.execution_observation === 'active' || next.execution_observation === 'draining')
  )
    return false;
  if (snapshot.availability.reason === 'queued' && receipt.terminal_event_id !== undefined) return true;
  return next === null && snapshot.target.input_revision > revision;
}

export const compactionUnresolved = (view: CompactionView): boolean =>
  Boolean(
    view.request &&
    (view.uncertain ||
      view.mutation ||
      !view.receipt ||
      (!view.receipt.can_submit_next_input && !snapshotReleasesCompaction(view.snapshot, view.receipt)))
  );
