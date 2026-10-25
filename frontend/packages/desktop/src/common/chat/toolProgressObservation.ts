export type ResourceAdmissionProgress = {
  decisionAt: string;
  reason: 'insufficient_capacity' | 'earlier_waiter' | 'admitted';
  capacityAvailableBytes: number;
  unusedPromisedBytes: number;
  netAvailableBytes: number;
  requestedBytes: number;
  requiredBytes: number;
  reservedBytes: number;
  precedingWaiters: number;
};

export type NormalizedToolProgress = {
  phase: string;
  message?: string;
  process?: string;
  phasePercent?: number;
  bytesPerSecond?: number;
  bytesCompleted?: number;
  bytesTotal?: number;
  completedItems?: number;
  totalItems?: number;
  elapsedMs?: number;
  indeterminate: boolean;
  resourceAdmission?: ResourceAdmissionProgress;
};

// Legacy wire progress may omit the determinate flag; normalization retains
// its existing default rather than fabricating a producer observation.
export type ToolProgressWire = Omit<NormalizedToolProgress, 'indeterminate'> & { indeterminate?: boolean };

export const normalizeResourceAdmissionProgress = (value: unknown): ResourceAdmissionProgress | undefined => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const record = value as Record<string, unknown>;
  if (
    typeof record.decisionAt !== 'string' ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/u.test(record.decisionAt)
  )
    return undefined;
  const decisionTime = new Date(record.decisionAt);
  if (
    !Number.isFinite(decisionTime.getTime()) ||
    decisionTime.toISOString().slice(0, 19) !== record.decisionAt.slice(0, 19)
  )
    return undefined;
  const fields = [
    'capacityAvailableBytes',
    'unusedPromisedBytes',
    'netAvailableBytes',
    'requestedBytes',
    'requiredBytes',
    'reservedBytes',
    'precedingWaiters',
  ] as const;
  if (fields.some((key) => !Number.isSafeInteger(record[key]) || Number(record[key]) < 0)) return undefined;
  const snapshot = {
    decisionAt: record.decisionAt,
    reason: record.reason,
    ...Object.fromEntries(fields.map((key) => [key, record[key]])),
  } as ResourceAdmissionProgress;
  if (
    snapshot.requiredBytes <= 0 ||
    snapshot.netAvailableBytes !== Math.max(0, snapshot.capacityAvailableBytes - snapshot.unusedPromisedBytes) ||
    (snapshot.requestedBytes > 0 && snapshot.requestedBytes !== snapshot.requiredBytes)
  )
    return undefined;
  switch (snapshot.reason) {
    case 'earlier_waiter':
      if (snapshot.precedingWaiters === 0 || snapshot.reservedBytes !== 0) return undefined;
      break;
    case 'insufficient_capacity':
      if (
        snapshot.precedingWaiters !== 0 ||
        snapshot.reservedBytes !== 0 ||
        snapshot.requiredBytes <= snapshot.netAvailableBytes
      )
        return undefined;
      break;
    case 'admitted':
      if (
        snapshot.precedingWaiters !== 0 ||
        snapshot.reservedBytes !== snapshot.requiredBytes ||
        snapshot.reservedBytes > snapshot.netAvailableBytes
      )
        return undefined;
      break;
    default:
      return undefined;
  }
  return snapshot;
};

export type ToolObservationScope = {
  identity?: string;
  conversationId?: string;
  branchId?: string;
  attempt?: unknown;
  revision?: unknown;
  status?: unknown;
  input?: unknown;
};

export const toolObservationCallKey = (callId: string, attempt: unknown, operationId?: string): string =>
  Number.isSafeInteger(attempt) && Number(attempt) >= 1 ? `${Number(attempt)}:${operationId || callId}` : callId;

export const toolObservationBranch = (branch: unknown): string | undefined =>
  typeof branch === 'string' && /^br_[0-9a-f]{8}$/u.test(branch) ? branch : undefined;

export const toolObservationOwnerKey = (
  conversationId: string | undefined,
  branchId: string | undefined,
  identity: string
): string => JSON.stringify([conversationId, branchId, identity]);

export const isToolObservationTerminal = (status: unknown): boolean =>
  status === 'completed' ||
  status === 'error' ||
  status === 'canceled' ||
  status === 'interrupted' ||
  status === 'unknown';

export const isToolObservationActive = (status: unknown): boolean =>
  status === 'pending' || status === 'running' || status === 'waiting' || status === 'blocked';

export const toolObservationInputsCompatible = (existing: unknown, incoming: unknown): boolean => {
  if (existing === undefined || incoming === undefined) return true;
  try {
    return JSON.stringify(existing) === JSON.stringify(incoming);
  } catch {
    return false;
  }
};

export const toolObservationOwnersCompatible = (
  existing: ToolObservationScope,
  incoming: ToolObservationScope
): boolean =>
  (!existing.conversationId || !incoming.conversationId || existing.conversationId === incoming.conversationId) &&
  (!existing.branchId || !incoming.branchId || existing.branchId === incoming.branchId);

const sameObservationScope = (existing: ToolObservationScope, incoming: ToolObservationScope): boolean =>
  Boolean(existing.identity) &&
  existing.identity === incoming.identity &&
  existing.conversationId === incoming.conversationId &&
  existing.branchId === incoming.branchId &&
  existing.attempt === incoming.attempt;

const knownRevision = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) >= 1;

const progressRecord = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;

export const resourceAdmissionMatchesPhase = (
  snapshot: ResourceAdmissionProgress | undefined,
  phase: unknown
): boolean =>
  Boolean(snapshot) &&
  ((phase === 'waiting_for_resources' && snapshot!.reason !== 'admitted') ||
    (phase === 'starting' && snapshot!.reason === 'admitted'));

const mergeProgressObservation = (
  previous: unknown,
  next: unknown,
  newProgress: boolean,
  existing: ToolObservationScope,
  incoming: ToolObservationScope
): unknown => {
  const nextRecord = progressRecord(next);
  if (!nextRecord) return next;
  const result = { ...nextRecord };
  delete result.resourceAdmission;
  if (isToolObservationTerminal(existing.status) || isToolObservationTerminal(incoming.status)) return result;
  const fresh = newProgress ? normalizeResourceAdmissionProgress(nextRecord.resourceAdmission) : undefined;
  if (resourceAdmissionMatchesPhase(fresh, result.phase)) {
    result.resourceAdmission = fresh;
    return result;
  }
  const oldRecord = progressRecord(previous);
  const retained = normalizeResourceAdmissionProgress(oldRecord?.resourceAdmission);
  if (
    sameObservationScope(existing, incoming) &&
    toolObservationInputsCompatible(existing.input, incoming.input) &&
    isToolObservationActive(existing.status) &&
    isToolObservationActive(incoming.status) &&
    oldRecord?.phase === result.phase &&
    resourceAdmissionMatchesPhase(retained, result.phase)
  )
    result.resourceAdmission = retained;
  return result;
};

// Both history normalization and the live same-ID upsert call this pure helper.
// It owns no list, cache, timer, transport, request, or lifecycle authority.
export function mergeToolObservationRecords<T extends object>(
  existing: T,
  incoming: T,
  oldScope: ToolObservationScope,
  newScope: ToolObservationScope,
  mode: 'live' | 'normalized' = 'live'
): T {
  const ignoreUndefined = mode === 'normalized';
  if (
    sameObservationScope(oldScope, newScope) &&
    knownRevision(oldScope.revision) &&
    knownRevision(newScope.revision) &&
    newScope.revision < oldScope.revision
  )
    return existing;
  const previous = existing as Record<string, unknown>;
  const update = incoming as Record<string, unknown>;
  const merged = { ...previous };
  for (const [key, value] of Object.entries(update)) {
    if (value !== undefined || !ignoreUndefined) {
      if (key === 'revision' && value === undefined && knownRevision(oldScope.revision)) continue;
      merged[key] = value;
    }
  }
  if (isToolObservationTerminal(oldScope.status)) {
    const immutable = ignoreUndefined || knownRevision(oldScope.revision) || knownRevision(newScope.revision);
    for (const key of immutable ? Object.keys(previous) : ['status', 'output', 'error']) {
      if (previous[key] !== undefined) merged[key] = previous[key];
    }
  }
  const ownsProgress =
    Object.prototype.hasOwnProperty.call(update, 'progress') && !(ignoreUndefined && update.progress === undefined);
  const progress = merged.progress;
  if (ownsProgress || previous.progress !== undefined) {
    merged.progress = mergeProgressObservation(previous.progress, progress, ownsProgress, oldScope, newScope);
  }
  if (mode === 'live' && (isToolObservationTerminal(oldScope.status) || isToolObservationTerminal(newScope.status))) {
    delete merged.streaming;
    delete merged.streamingRecoveryError;
  }
  return merged as T;
}
