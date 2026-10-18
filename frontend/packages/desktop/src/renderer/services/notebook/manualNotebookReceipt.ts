import {
  freezeManualNotebookRequest,
  normalizeManualNotebookTarget,
  recordValue,
  safePositive,
  sameManualNotebookRequest,
  sameNotebookIdentity,
  sameNotebookTarget,
  validIdentity,
  validRequestId,
} from './manualNotebookContract';
import type {
  ManualNotebookDisposition,
  ManualNotebookPendingPage,
  ManualNotebookReceipt,
  ManualNotebookRequest,
  ManualNotebookScope,
  ManualNotebookState,
  ManualNotebookTarget,
} from './manualNotebookTypes';

const states = new Set(['queued', 'started', 'cancel_requested', 'outcome_unknown', 'terminal']);
const timestamp = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const fail = (): never => {
  throw new Error('invalid_manual_notebook_receipt');
};

export function decodeManualNotebookReceipt(
  value: unknown,
  target: ManualNotebookTarget,
  requestId: string,
  expected?: ManualNotebookRequest
): ManualNotebookReceipt {
  if (
    !recordValue(value) ||
    value.schema !== 'x-science.manual-kernel-receipt.v1' ||
    !['typed-v1', 'legacy'].includes(String(value.request_kind)) ||
    !recordValue(value.durable) ||
    !recordValue(value.physical)
  )
    return fail();
  const durable = value.durable;
  const stored = durable.request;
  if (!recordValue(stored)) return fail();
  const actual = normalizeManualNotebookTarget({
    ...durable,
    schema: 'x-science.manual-kernel-target.v1',
    root_frame_incarnation_id: durable.root_incarnation_id,
    language: stored.language,
    environment: stored.environment,
  });
  if (
    !actual ||
    !sameNotebookTarget(actual, target) ||
    stored.request_id !== requestId ||
    !validRequestId(requestId) ||
    durable.version !== 1 ||
    durable.origin !== 'user' ||
    !validIdentity(durable.owner_id) ||
    !validIdentity(durable.execution_id) ||
    !safePositive(durable.state_version) ||
    !states.has(String(durable.state)) ||
    !timestamp(durable.accepted_at) ||
    typeof stored.source !== 'string' ||
    typeof durable.request_sha256 !== 'string' ||
    !/^[0-9a-f]{64}$/.test(durable.request_sha256)
  )
    return fail();
  let request: ManualNotebookRequest | null = null;
  if (value.request_kind === 'typed-v1') {
    if (!recordValue(value.request)) return fail();
    if (!safePositive(stored.timeout_ms)) return fail();
    request = freezeManualNotebookRequest(actual, stored.source, stored.timeout_ms, requestId);
    if (
      !sameManualNotebookRequest(request, value.request as ManualNotebookRequest) ||
      stored.kernel_id !== actual.kernelId ||
      stored.expected_kernel_generation !== actual.kernelGeneration ||
      (expected && !sameManualNotebookRequest(request, expected))
    )
      return fail();
  } else if ('request' in value || expected || (stored.kernel_id !== undefined && stored.kernel_id !== ''))
    return fail();
  const startedAt = timestamp(durable.started_at) ? durable.started_at : null;
  const finishedAt = timestamp(durable.finished_at) ? durable.finished_at : null;
  const disposition = typeof durable.disposition === 'string' ? durable.disposition : null;
  if (
    durable.state === 'terminal' &&
    (!finishedAt ||
      !['completed', 'failed', 'cancelled', 'cancelled_before_start', 'rejected_before_start'].includes(
        disposition ?? ''
      ))
  )
    return fail();
  if (durable.state !== 'terminal' && finishedAt) return fail();
  if (durable.state === 'started' && !startedAt) return fail();
  if (durable.state === 'cancel_requested' && !timestamp(durable.cancel_requested_at)) return fail();
  if (durable.state === 'terminal') {
    if (typeof durable.result_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(durable.result_sha256)) return fail();
    const prestart = disposition === 'cancelled_before_start' || disposition === 'rejected_before_start';
    if (
      prestart
        ? Boolean(startedAt || durable.execution_log_id)
        : !startedAt || durable.execution_log_id !== durable.execution_id
    )
      return fail();
  }
  const physical = value.physical;
  if (
    !['queued', 'running', 'persisting', 'unavailable'].includes(String(physical.status)) ||
    !timestamp(physical.sampled_at) ||
    (physical.status === 'running' && !startedAt)
  )
    return fail();
  const rejection =
    recordValue(durable.rejection) &&
    typeof durable.rejection.stdout === 'string' &&
    typeof durable.rejection.stderr === 'string'
      ? { stdout: durable.rejection.stdout, stderr: durable.rejection.stderr }
      : null;
  return {
    requestId,
    executionId: durable.execution_id,
    ownerId: durable.owner_id,
    requestKind: value.request_kind as 'typed-v1' | 'legacy',
    request,
    target: actual,
    state: durable.state as ManualNotebookState,
    stateVersion: durable.state_version,
    acceptedAt: durable.accepted_at,
    startedAt,
    finishedAt,
    disposition: disposition as ManualNotebookDisposition | null,
    source: stored.source,
    rejection,
    physical: {
      status: physical.status as ManualNotebookReceipt['physical']['status'],
      sampledAt: physical.sampled_at,
      reason: typeof physical.reason === 'string' ? physical.reason : null,
    },
  };
}

export function decodeManualNotebookPending(value: unknown, scope: ManualNotebookScope): ManualNotebookPendingPage {
  if (
    !recordValue(value) ||
    value.schema !== 'x-science.manual-kernel-pending.v1' ||
    !Array.isArray(value.pending) ||
    value.pending.length > 32 ||
    typeof value.scan_complete !== 'boolean' ||
    !Number.isSafeInteger(value.through_sequence) ||
    Number(value.through_sequence) < 0 ||
    (typeof value.next_cursor !== 'string' && value.next_cursor !== null)
  )
    return fail();
  const nextCursor = typeof value.next_cursor === 'string' ? value.next_cursor || null : null;
  if (nextCursor && nextCursor.length > 8192) return fail();
  if (value.scan_complete !== (nextCursor === null)) return fail();
  const seen = new Set<string>();
  const pending = value.pending.map((item) => {
    if (
      !recordValue(item) ||
      !validRequestId(item.request_id) ||
      !validIdentity(item.execution_id) ||
      !['typed-v1', 'legacy'].includes(String(item.request_kind)) ||
      !states.has(String(item.state)) ||
      item.state === 'terminal' ||
      !safePositive(item.state_version) ||
      !timestamp(item.accepted_at) ||
      'source' in item ||
      'code' in item
    )
      return fail();
    const target = normalizeManualNotebookTarget(item.target);
    if (!target || !sameNotebookIdentity(scope, target) || seen.has(item.request_id)) return fail();
    seen.add(item.request_id);
    return {
      requestId: item.request_id,
      executionId: item.execution_id,
      requestKind: item.request_kind as 'typed-v1' | 'legacy',
      target,
      state: item.state as ManualNotebookState,
      stateVersion: item.state_version,
      acceptedAt: item.accepted_at,
    };
  });
  return { pending, nextCursor, scanComplete: value.scan_complete, throughSequence: value.through_sequence as number };
}
