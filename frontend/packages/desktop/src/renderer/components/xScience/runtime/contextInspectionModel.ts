import { CONTEXT_CALL_FIELDS, type ContextCallField } from '@/renderer/services/contextUsage';
import type {
  ContextInspection,
  ContextInspectionCall,
  ContextInspectionOutcome,
} from '@/renderer/services/runtime/contextInspection';

export type ContextInspectionTurnPoint = ContextInspectionOutcome & { turnId: string; turnNumber: number };

/** Reference visibleMessageSamples semantics, ordered by durable terminal identity rather than wall-clock guesses. */
export function inspectionTurnPoints(inspection: ContextInspection): ContextInspectionTurnPoint[] {
  return inspection.turns
    .flatMap((turn, turnIndex) => {
      const latestCompleted = turn.outcomes.reduce<ContextInspectionOutcome | undefined>(
        (latest, outcome) =>
          outcome.status === 'completed' && (!latest || latest.terminalEventId < outcome.terminalEventId)
            ? outcome
            : latest,
        undefined
      );
      return turn.outcomes
        .filter((outcome) => outcome.status !== 'completed' || outcome.id === latestCompleted?.id)
        .map((outcome) => ({ ...outcome, turnId: turn.id, turnNumber: turnIndex + 1 }));
    })
    .sort((left, right) => left.terminalEventId - right.terminalEventId);
}

export const INSPECTION_COLORS = {
  systemPrompt: '#657c91',
  tools: '#c4a878',
  messages: '#8eaa98',
  mcp: '#8b93ad',
  skills: '#aa8c9d',
};
export const CALL_COLORS = {
  input: '#657c91',
  cache_read: '#a9b9c7',
  cache_write: '#c4a878',
  output: '#8eaa98',
  total: '#a3a2a0',
};

/** Token presence is provider evidence. A missing field never becomes a zero. */
export function reportedCallToken(call: ContextInspectionCall, field: ContextCallField): number | undefined {
  const usage = call.callUsage;
  if (!usage?.reported?.[field] || usage.invalid_fields?.[field]) return undefined;
  const value =
    field === 'input'
      ? usage.input_tokens
      : field === 'output'
        ? usage.output_tokens
        : field === 'total'
          ? usage.total_tokens
          : field === 'cache_read'
            ? usage.cache_read_tokens
            : usage.cache_write_tokens;
  // Explicitly reported cache zero may be omitted by the server's wire format.
  return value ?? (field === 'cache_read' || field === 'cache_write' ? 0 : undefined);
}

export function callSegments(call: ContextInspectionCall) {
  const input = reportedCallToken(call, 'input');
  const output = reportedCallToken(call, 'output');
  const read = reportedCallToken(call, 'cache_read');
  const write = reportedCallToken(call, 'cache_write');
  if (input === undefined || output === undefined) {
    const total = reportedCallToken(call, 'total');
    return total === undefined ? [] : [{ field: 'total' as const, tokens: total }];
  }
  const cache = (read ?? 0) + (write ?? 0);
  const uncached = call.callUsage?.cache_tokens_additive ? input : Math.max(0, input - cache);
  return [
    { field: 'input' as const, tokens: uncached },
    ...(read === undefined ? [] : [{ field: 'cache_read' as const, tokens: read }]),
    ...(write === undefined ? [] : [{ field: 'cache_write' as const, tokens: write }]),
    { field: 'output' as const, tokens: output },
  ];
}

export function summarizeInspectionCalls(inspection: ContextInspection) {
  const totals = inspection.calls
    .map((call) => reportedCallToken(call, 'total'))
    .filter((value) => value !== undefined);
  const known = totals.reduce((sum, value) => sum + BigInt(value), BigInt(0));
  const peak = inspection.calls.reduce<ContextInspectionCall | undefined>(
    (current, call) => (!current || current.usedTokens <= call.usedTokens ? call : current),
    undefined
  );
  return {
    count: inspection.calls.length,
    reported: totals.length,
    knownTotal: known <= BigInt(Number.MAX_SAFE_INTEGER) && totals.length ? Number(known) : undefined,
    peak,
  };
}

export function inspectionCallBands(inspection: ContextInspection) {
  const byId = new Map(inspection.calls.map((call) => [call.id, call]));
  return inspection.turns
    .map((turn, turnIndex) => ({
      turnId: turn.id,
      turnIndex,
      calls: turn.callIds.map((id) => byId.get(id)).filter((call) => call !== undefined),
    }))
    .filter((band) => band.calls.length > 0);
}

export { CONTEXT_CALL_FIELDS };
