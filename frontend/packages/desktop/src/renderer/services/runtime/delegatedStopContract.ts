import type {
  DelegatedStopBranch,
  DelegatedStopReceipt,
  DelegatedStopRequest,
  DelegatedStopScope,
  DelegatedStopSnapshot,
  DelegatedStopTarget,
} from './delegatedStopTypes';

function invalid(): never {
  throw new Error('Invalid delegated Stop response');
}
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid();
  return value as Record<string, unknown>;
}
function identity(value: unknown): string {
  if (
    typeof value !== 'string' ||
    !value ||
    value.trim() !== value ||
    value.length > 4096 ||
    [...value].some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)
  )
    return invalid();
  return value;
}
function integer(value: unknown, minimum = 0): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < minimum) return invalid();
  return value;
}
function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') return invalid();
  return value;
}
function optionalText(value: unknown): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== 'string' || value.length > 4096) return invalid();
  return value;
}
function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T {
  if (typeof value !== 'string' || !allowed.includes(value as T)) return invalid();
  return value as T;
}
function array(value: unknown): unknown[] {
  if (!Array.isArray(value)) return invalid();
  return value;
}
function exact(value: Record<string, unknown>, keys: string[]): void {
  if (Object.keys(value).some((key) => !keys.includes(key))) invalid();
}
function scope(value: unknown): DelegatedStopScope {
  const raw = record(value);
  exact(raw, ['parent_frame_id', 'parent_frame_incarnation_id', 'root_frame_id', 'root_frame_incarnation_id']);
  return {
    parent_frame_id: identity(raw.parent_frame_id),
    parent_frame_incarnation_id: identity(raw.parent_frame_incarnation_id),
    root_frame_id: identity(raw.root_frame_id),
    root_frame_incarnation_id: identity(raw.root_frame_incarnation_id),
  };
}
function branch(value: unknown): DelegatedStopBranch {
  const raw = record(value);
  exact(raw, ['branch_id', 'generation']);
  const branchId = identity(raw.branch_id);
  if (!/^br_[0-9a-f]{8}$/.test(branchId)) return invalid();
  return { branch_id: branchId, generation: integer(raw.generation, 1) };
}
function target(value: unknown): DelegatedStopTarget {
  const raw = record(value);
  exact(raw, [
    'frame_id',
    'frame_incarnation_id',
    'stream_uid',
    'stream_epoch',
    'input_message_id',
    'input_revision',
    'runner_attempt',
  ]);
  return {
    frame_id: identity(raw.frame_id),
    frame_incarnation_id: identity(raw.frame_incarnation_id),
    stream_uid: identity(raw.stream_uid),
    stream_epoch: integer(raw.stream_epoch, 1),
    input_message_id: identity(raw.input_message_id),
    input_revision: integer(raw.input_revision, 1),
    runner_attempt: integer(raw.runner_attempt),
  };
}
export function decodeDelegatedStopRequest(value: unknown): DelegatedStopRequest {
  const raw = record(value);
  exact(raw, [
    'schema',
    'request_id',
    'scope',
    'branch',
    'parent_input_revision',
    'expected_fence_generation',
    'mode',
    'target',
  ]);
  if (raw.schema !== 'x-science.delegated-stop.v1') return invalid();
  const requestId = identity(raw.request_id);
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(requestId)) return invalid();
  const mode = oneOf(raw.mode, ['branch', 'child'] as const);
  if (mode === 'branch' && raw.target !== undefined) return invalid();
  return {
    schema: 'x-science.delegated-stop.v1',
    request_id: requestId,
    scope: scope(raw.scope),
    branch: branch(raw.branch),
    parent_input_revision: integer(raw.parent_input_revision),
    expected_fence_generation: integer(raw.expected_fence_generation),
    mode,
    ...(mode === 'child' ? { target: target(raw.target) } : {}),
  };
}
export function decodeDelegatedStopSnapshot(value: unknown): DelegatedStopSnapshot {
  const raw = record(value);
  if (raw.schema !== 'x-science.delegated-stop-snapshot.v1') return invalid();
  const targets = array(raw.targets).map((item) => {
    const row = record(item);
    const parsed = row.target === null ? null : target(row.target);
    const frameId = identity(row.frame_id);
    const canStop = boolean(row.can_stop);
    if ((parsed && parsed.frame_id !== frameId) || (canStop && !parsed)) return invalid();
    return {
      target: parsed,
      frame_id: frameId,
      origin: oneOf(row.origin, ['verified', 'legacy-unavailable'] as const),
      in_selected_branch: boolean(row.in_selected_branch),
      can_stop: canStop,
      status: oneOf(row.status, ['running', 'awaiting_input', 'terminal', 'unavailable'] as const),
      reason: optionalText(row.reason),
    };
  });
  if (new Set(targets.map((row) => row.frame_id)).size !== targets.length) return invalid();
  return {
    schema: 'x-science.delegated-stop-snapshot.v1',
    scope: scope(raw.scope),
    branch: branch(raw.branch),
    parent_input_revision: integer(raw.parent_input_revision),
    fence_generation: integer(raw.fence_generation),
    can_stop_branch: boolean(raw.can_stop_branch),
    targets,
  };
}
export function sameDelegatedStopCoordinates(
  rendered: DelegatedStopSnapshot,
  latest: DelegatedStopSnapshot,
  childFrameId?: string
): boolean {
  const left = rendered.scope;
  const right = latest.scope;
  if (
    left.parent_frame_id !== right.parent_frame_id ||
    left.parent_frame_incarnation_id !== right.parent_frame_incarnation_id ||
    left.root_frame_id !== right.root_frame_id ||
    left.root_frame_incarnation_id !== right.root_frame_incarnation_id ||
    rendered.branch.branch_id !== latest.branch.branch_id ||
    rendered.branch.generation !== latest.branch.generation ||
    rendered.parent_input_revision !== latest.parent_input_revision ||
    rendered.fence_generation !== latest.fence_generation
  )
    return false;
  if (!childFrameId) return true;
  const before = rendered.targets.find((row) => row.frame_id === childFrameId)?.target;
  const current = latest.targets.find((row) => row.frame_id === childFrameId)?.target;
  return Boolean(
    before &&
    current &&
    before.frame_id === current.frame_id &&
    before.frame_incarnation_id === current.frame_incarnation_id &&
    before.stream_uid === current.stream_uid &&
    before.stream_epoch === current.stream_epoch &&
    before.input_message_id === current.input_message_id &&
    before.input_revision === current.input_revision &&
    before.runner_attempt === current.runner_attempt
  );
}

export function sameDelegatedStopRequest(left: DelegatedStopRequest, right: DelegatedStopRequest): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}
export function decodeDelegatedStopReceipt(value: unknown, expected: DelegatedStopRequest): DelegatedStopReceipt {
  const raw = record(value);
  if (raw.schema !== 'x-science.delegated-stop-receipt.v1') return invalid();
  const request = decodeDelegatedStopRequest(raw.request);
  if (!sameDelegatedStopRequest(request, expected)) return invalid();
  const acceptedAt = identity(raw.accepted_at);
  if (!Number.isFinite(Date.parse(acceptedAt))) return invalid();
  const targets = array(raw.targets).map((item) => {
    const row = record(item);
    const parsed = target(row.target);
    return {
      target: parsed,
      outcome: oneOf(row.outcome, ['cancel_requested', 'cancelled', 'already_terminal'] as const),
      execution_observation: oneOf(row.execution_observation, ['running', 'stopped', 'unavailable'] as const),
      diagnostic: optionalText(row.diagnostic),
    };
  });
  if (request.mode === 'child') {
    const original = targets.filter((row) => row.target.frame_id === request.target?.frame_id);
    if (original.length !== 1 || JSON.stringify(original[0].target) !== JSON.stringify(request.target))
      return invalid();
  }
  if (new Set(targets.map((row) => row.target.frame_id)).size !== targets.length) return invalid();
  return {
    schema: 'x-science.delegated-stop-receipt.v1',
    request,
    accepted_at: acceptedAt,
    state: oneOf(raw.state, ['stopping', 'settled'] as const),
    replayed: boolean(raw.replayed),
    targets,
  };
}
