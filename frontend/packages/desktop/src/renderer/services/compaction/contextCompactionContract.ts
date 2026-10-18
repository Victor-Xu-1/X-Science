import {
  sameCompactionScope,
  type CompactionIdentity,
  type CompactionReceipt,
  type CompactionRequest,
  type CompactionSnapshot,
  type CompactionTarget,
} from './contextCompactionTypes';

const invalid = (): never => {
  throw new Error('Invalid context compaction response');
};
const record = (value: unknown): Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid();
  return value as Record<string, unknown>;
};
const text = (value: unknown): string =>
  typeof value === 'string' && value.length > 0 && value.length <= 1024 ? value : invalid();
const integer = (value: unknown, minimum = 0): number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum ? value : invalid();
const boolean = (value: unknown): boolean => (typeof value === 'boolean' ? value : invalid());
const oneOf = <T extends string>(value: unknown, choices: readonly T[]): T =>
  choices.includes(value as T) ? (value as T) : invalid();
export function decodeCompactionTarget(value: unknown, identity: CompactionIdentity): CompactionTarget {
  const item = record(value);
  const target = Object.freeze({
    frame_incarnation_id: text(item.frame_incarnation_id),
    root_frame_id: text(item.root_frame_id),
    root_frame_incarnation_id: text(item.root_frame_incarnation_id),
    stream_uid: text(item.stream_uid),
    stream_epoch: integer(item.stream_epoch, 1),
    branch_id: text(item.branch_id),
    branch_generation: integer(item.branch_generation, 1),
    input_revision: integer(item.input_revision),
    through_publication_seq: integer(item.through_publication_seq),
  });
  if (target.frame_incarnation_id !== identity.frameIncarnationId || target.root_frame_id !== identity.rootFrameId)
    return invalid();
  return target;
}
export function sameCompactionRequest(left: CompactionRequest, right: CompactionRequest): boolean {
  return (
    left.request_id === right.request_id &&
    (Object.keys(left.expected) as Array<keyof CompactionTarget>).every(
      (key) => left.expected[key] === right.expected[key]
    )
  );
}
export function decodeCompactionRequest(value: unknown, identity: CompactionIdentity): CompactionRequest {
  const item = record(value);
  if (
    item.schema !== 'x-science.context-compaction-request.v1' ||
    typeof item.request_id !== 'string' ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(item.request_id)
  )
    return invalid();
  return Object.freeze({
    schema: item.schema,
    request_id: item.request_id,
    expected: decodeCompactionTarget(item.expected, identity),
  });
}
export function decodeCompactionReceipt(
  value: unknown,
  identity: CompactionIdentity,
  expected?: CompactionRequest
): CompactionReceipt {
  const item = record(value);
  if (item.schema !== 'x-science.context-compaction-receipt.v1' || item.frame_id !== identity.frameId) return invalid();
  const request = decodeCompactionRequest(item.request, identity);
  if (expected && !sameCompactionRequest(expected, request)) return invalid();
  const receipt: CompactionReceipt = {
    schema: item.schema,
    request,
    frame_id: identity.frameId,
    accepted: boolean(item.accepted),
    state: oneOf(item.state, ['accepted', 'compacting', 'committed', 'cancelled', 'failed', 'recovery_needed']),
    cancellation_status: oneOf(item.cancellation_status, [
      'not_requested',
      'requested',
      'cancelled_before_acceptance',
      'cancelled',
      'already_terminal',
    ]),
    execution_observation: oneOf(item.execution_observation, [
      'not_started',
      'active',
      'draining',
      'settled',
      'unavailable',
    ]),
    can_submit_next_input: boolean(item.can_submit_next_input),
    replayed: boolean(item.replayed),
  };
  if (receipt.accepted) {
    receipt.input_message_id = text(item.input_message_id);
    receipt.input_revision = integer(item.input_revision, 1);
  } else if (
    ['input_message_id', 'input_revision', 'attempt', 'boundary_event_id', 'terminal_event_id'].some(
      (key) => item[key] !== undefined
    )
  )
    return invalid();
  if (item.attempt !== undefined) receipt.attempt = integer(item.attempt, 1);
  if (item.boundary_event_id !== undefined) receipt.boundary_event_id = integer(item.boundary_event_id, 1);
  if (item.terminal_event_id !== undefined) receipt.terminal_event_id = integer(item.terminal_event_id, 1);
  if (item.archive_status !== undefined) {
    if (receipt.state !== 'committed') return invalid();
    receipt.archive_status = oneOf(item.archive_status, ['available', 'pending']);
  }
  return receipt;
}
export function decodeCompactionSnapshot(value: unknown, identity: CompactionIdentity): CompactionSnapshot {
  const item = record(value),
    availability = record(item.availability);
  if (item.schema !== 'x-science.context-compaction-snapshot.v1') return invalid();
  const target = decodeCompactionTarget(item.target, identity);
  const operation = item.operation === null ? null : decodeCompactionReceipt(item.operation, identity);
  if (operation && !sameCompactionScope(operation.request.expected, target)) return invalid();
  return {
    schema: item.schema,
    target,
    operation,
    availability: {
      available: boolean(availability.available),
      reason: oneOf(availability.reason, ['idle', 'active', 'queued', 'waiting', 'archived', 'unavailable']),
    },
  };
}
