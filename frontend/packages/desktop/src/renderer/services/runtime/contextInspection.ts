import type { ContextCallUsage, ContextUsageSnapshot } from '../contextUsage';

export type ContextCallFailureKind = 'cancelled' | 'deadline';

export const CALL_PHASE_OFFSETS = [
  'firstPrivateActivity',
  'firstNativeContent',
  'firstToolBoundary',
  'firstPublicProgressReady',
  'firstDurablePublication',
  'delegateFinished',
] as const;
export const TRANSPORT_PHASE_OFFSETS = [
  'attemptEntry',
  'doEntered',
  'headersReady',
  'firstBodyRead',
  'firstDecodedGrowth',
] as const;
export type ContextCallPhaseTiming = {
  version: 1;
  origin: 'observer_entry';
  offsetsUs: Partial<Record<(typeof CALL_PHASE_OFFSETS)[number], number>>;
  preparation?: { stage: 'resolve' | 'directory_prepare' | 'context_budget'; beginUs: number; endUs?: number }[];
  transportAttempts?: {
    attempt: number;
    protocol: string;
    offsetsUs: Partial<Record<(typeof TRANSPORT_PHASE_OFFSETS)[number], number>>;
  }[];
  truncated: boolean;
  saturated: boolean;
};

/** A damaged optional diagnostic never removes the original call or usage. */
export function parseContextCallPhaseTiming(value: unknown, state: unknown): ContextCallPhaseTiming | undefined {
  if (
    !object(value) ||
    !['complete', 'failed'].includes(String(state)) ||
    value.version !== 1 ||
    value.origin !== 'observer_entry' ||
    typeof value.truncated !== 'boolean' ||
    typeof value.saturated !== 'boolean' ||
    !object(value.offsetsUs)
  )
    return;
  const offsets = (record: unknown, keys: readonly string[]) =>
    object(record) && Object.entries(record).every(([key, offset]) => keys.includes(key) && count(offset));
  if (!offsets(value.offsetsUs, CALL_PHASE_OFFSETS)) return;
  if (
    value.preparation !== undefined &&
    (!Array.isArray(value.preparation) ||
      value.preparation.length > 64 ||
      value.preparation.some(
        (span) =>
          !object(span) ||
          !['resolve', 'directory_prepare', 'context_budget'].includes(String(span.stage)) ||
          !count(span.beginUs) ||
          (span.endUs !== undefined && (!count(span.endUs) || span.endUs < span.beginUs))
      ))
  )
    return;
  if (
    value.transportAttempts !== undefined &&
    (!Array.isArray(value.transportAttempts) ||
      value.transportAttempts.length > 64 ||
      value.transportAttempts.some((attempt) => {
        if (
          !object(attempt) ||
          !count(attempt.attempt) ||
          attempt.attempt === 0 ||
          ![
            'openai-compatible',
            'openai-responses',
            'azure-openai',
            'azure-openai-responses',
            'anthropic',
            'gemini',
          ].includes(String(attempt.protocol)) ||
          !offsets(attempt.offsetsUs, TRANSPORT_PHASE_OFFSETS) ||
          !object(attempt.offsetsUs) ||
          !count(attempt.offsetsUs.attemptEntry)
        )
          return true;
        let previous = attempt.offsetsUs.attemptEntry;
        for (const key of TRANSPORT_PHASE_OFFSETS) {
          const offset = attempt.offsetsUs[key];
          if (offset !== undefined) {
            if (!count(offset) || offset < previous) return true;
            previous = offset;
          }
        }
        return false;
      }))
  )
    return;
  return value as ContextCallPhaseTiming;
}

/** Only failed requests can carry an explicitly recorded transport reason. */
export function validContextCallFailureKind(value: unknown, state: unknown): value is ContextCallFailureKind {
  return state === 'failed' && (value === 'cancelled' || value === 'deadline');
}

export type ContextInspectionScope = {
  conversationId: string;
  frameIncarnationId: string;
  streamUid: string;
  streamEpoch: number;
  branchId: string;
  branchGeneration: number;
  throughPublicationSequence: number;
};
export type ContextInspectionCall = {
  id: string;
  inputMessageId?: string;
  inputEventId?: number;
  inputRevision?: number;
  runnerAttempt: number;
  model: string;
  state: 'request' | 'complete' | 'failed';
  failureKind?: ContextCallFailureKind;
  observedAt: string;
  usedTokens: number;
  limitTokens: number;
  limitSource: 'configured' | 'model_profile' | 'unknown';
  outputTokens: number;
  callUsage?: ContextCallUsage;
  phaseTiming?: ContextCallPhaseTiming;
};
export type ContextInspectionOutcome = {
  id: string;
  status: 'completed' | 'failed' | 'cancelled' | 'reclaimed';
  terminalEventId: number;
  completedAt: string;
  runnerAttempt: number;
  callIds: string[];
  sample?: ContextUsageSnapshot;
};
export type ContextInspectionTurn = {
  id: string;
  inputMessageId: string;
  inputRevision: number;
  status: string;
  terminalEventId?: number;
  completedAt?: string;
  runnerAttempts: number[];
  callIds: string[];
  outcomes: ContextInspectionOutcome[];
  sample?: ContextUsageSnapshot;
};
export type ContextInspection = {
  version: 1;
  scope: ContextInspectionScope;
  coverage: {
    association: 'complete' | 'partial' | 'unavailable';
    retainedCallCount: number;
    observedSampleCount: number;
    observedCallCount?: number;
    unassociatedCallCount: number;
    truncated: boolean;
  };
  calls: ContextInspectionCall[];
  turns: ContextInspectionTurn[];
};

const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const count = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) >= 0;
const identity = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 512;
const date = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const unique = (values: unknown[]) => new Set(values).size === values.length;
// The bounded backend projection retains at most 2000 current attempt terminals
// plus 2000 retired reclaimed terminals, including when all belong to one input.
const MAX_CONTEXT_INSPECTION_OUTCOMES_PER_TURN = 4000;

/** Identity comes from the transcript projection, never inferred from flat history or time. */
export function contextInspectionIdentity(scope: ContextInspectionScope): string {
  const { throughPublicationSequence: _through, ...identityScope } = scope;
  return JSON.stringify(identityScope);
}

export function parseContextInspection(
  value: unknown,
  conversationId: string,
  parseSample: (sample: unknown) => ContextUsageSnapshot,
  validCallUsage: (usage: unknown) => boolean
): ContextInspection | null {
  if (value === null) return null;
  if (!object(value) || value.version !== 1 || !object(value.scope) || !object(value.coverage))
    throw new Error('Invalid context inspection');
  const scope = value.scope;
  if (
    scope.conversationId !== conversationId ||
    !['frameIncarnationId', 'streamUid', 'branchId'].every((key) => identity(scope[key])) ||
    !['streamEpoch', 'branchGeneration', 'throughPublicationSequence'].every((key) => count(scope[key])) ||
    Number(scope.streamEpoch) === 0 ||
    Number(scope.branchGeneration) === 0
  )
    throw new Error('Invalid context inspection scope');
  const coverage = value.coverage;
  if (
    !['complete', 'partial', 'unavailable'].includes(String(coverage.association)) ||
    !['retainedCallCount', 'observedSampleCount', 'unassociatedCallCount'].every((key) => count(coverage[key])) ||
    (coverage.observedCallCount !== undefined && !count(coverage.observedCallCount)) ||
    typeof coverage.truncated !== 'boolean' ||
    !Array.isArray(value.calls) ||
    !Array.isArray(value.turns) ||
    value.calls.length > 512 ||
    value.turns.length > 512 ||
    coverage.retainedCallCount !== value.calls.length
  )
    throw new Error('Invalid context inspection coverage');
  const calls = value.calls.map((call) => {
    if (
      !object(call) ||
      !identity(call.id) ||
      typeof call.model !== 'string' ||
      call.model.length > 512 ||
      !['request', 'complete', 'failed'].includes(String(call.state)) ||
      (Object.hasOwn(call, 'failureKind') && !validContextCallFailureKind(call.failureKind, call.state)) ||
      !date(call.observedAt) ||
      !['runnerAttempt', 'usedTokens', 'limitTokens', 'outputTokens'].every((key) => count(call[key])) ||
      !['configured', 'model_profile', 'unknown'].includes(String(call.limitSource)) ||
      (call.limitSource === 'unknown' ? call.limitTokens !== 0 : call.limitTokens === 0) ||
      (call.inputMessageId !== undefined && !identity(call.inputMessageId)) ||
      ['inputEventId', 'inputRevision'].some((key) => call[key] !== undefined && !count(call[key])) ||
      (call.callUsage !== undefined && (call.state !== 'complete' || !validCallUsage(call.callUsage)))
    )
      throw new Error('Invalid context inspection call');
    const { phaseTiming: rawTiming, ...original } = call;
    const phaseTiming = parseContextCallPhaseTiming(rawTiming, call.state);
    return { ...original, ...(phaseTiming ? { phaseTiming } : {}) } as ContextInspectionCall;
  });
  const turns = value.turns.map((turn) => {
    if (
      !object(turn) ||
      !identity(turn.id) ||
      !identity(turn.inputMessageId) ||
      !count(turn.inputRevision) ||
      typeof turn.status !== 'string' ||
      !/^[a-z_]{1,40}$/.test(turn.status) ||
      (turn.terminalEventId !== undefined && !count(turn.terminalEventId)) ||
      (turn.completedAt !== undefined && !date(turn.completedAt)) ||
      !Array.isArray(turn.runnerAttempts) ||
      turn.runnerAttempts.some((attempt) => !count(attempt)) ||
      !unique(turn.runnerAttempts) ||
      !Array.isArray(turn.callIds) ||
      turn.callIds.some((id) => !identity(id)) ||
      !unique(turn.callIds) ||
      !Array.isArray(turn.outcomes) ||
      turn.outcomes.length > MAX_CONTEXT_INSPECTION_OUTCOMES_PER_TURN
    )
      throw new Error('Invalid context inspection turn');
    const callIds = turn.callIds;
    const outcomes = turn.outcomes.map((outcome) => {
      if (
        !object(outcome) ||
        !identity(outcome.id) ||
        !['completed', 'failed', 'cancelled', 'reclaimed'].includes(String(outcome.status)) ||
        !count(outcome.terminalEventId) ||
        outcome.terminalEventId === 0 ||
        !date(outcome.completedAt) ||
        !count(outcome.runnerAttempt) ||
        !Array.isArray(outcome.callIds) ||
        outcome.callIds.some((id) => !identity(id) || !callIds.includes(id)) ||
        !unique(outcome.callIds)
      )
        throw new Error('Invalid context inspection outcome');
      return {
        ...outcome,
        ...(outcome.sample === undefined ? {} : { sample: parseSample(outcome.sample) }),
      } as ContextInspectionOutcome;
    });
    return {
      ...turn,
      outcomes,
      ...(turn.sample === undefined ? {} : { sample: parseSample(turn.sample) }),
    } as ContextInspectionTurn;
  });
  if (!unique(calls.map((call) => call.id)) || !unique(turns.map((turn) => turn.id)))
    throw new Error('Duplicate context inspection identity');
  const callById = new Map(calls.map((call) => [call.id, call]));
  const assigned = new Set<string>();
  const outcomeIds = new Set<string>();
  for (const turn of turns) {
    for (const id of turn.callIds) {
      const call = callById.get(id);
      if (
        assigned.has(id) ||
        (call?.inputMessageId !== undefined && call.inputMessageId !== turn.inputMessageId) ||
        (call?.inputRevision !== undefined && call.inputRevision !== turn.inputRevision) ||
        (!call && coverage.association === 'complete' && !coverage.truncated)
      )
        throw new Error('Invalid context inspection association');
      assigned.add(id);
    }
    if (turn.sample) {
      const call = callById.get(turn.sample.requestId);
      if (!call || !turn.callIds.includes(call.id) || call.state !== 'complete' || turn.sample.state !== 'complete')
        throw new Error('Invalid context inspection sample association');
    }
    for (const outcome of turn.outcomes) {
      if (
        outcomeIds.has(outcome.id) ||
        outcome.callIds.some((id) => callById.get(id)?.runnerAttempt !== outcome.runnerAttempt)
      )
        throw new Error('Invalid context inspection outcome association');
      outcomeIds.add(outcome.id);
      if (
        outcome.sample &&
        (!outcome.callIds.includes(outcome.sample.requestId) ||
          outcome.sample.state !== 'complete' ||
          callById.get(outcome.sample.requestId)?.state !== 'complete')
      )
        throw new Error('Invalid context inspection outcome sample');
    }
  }
  if (calls.some((call) => !assigned.has(call.id))) throw new Error('Unassociated context inspection call');
  return {
    version: 1,
    scope: scope as ContextInspectionScope,
    coverage: coverage as ContextInspection['coverage'],
    calls,
    turns,
  };
}
