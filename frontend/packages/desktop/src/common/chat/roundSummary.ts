/** A completed logical round, projected from durable server receipts. */
export type RoundSummary = {
  attempt: number;
  input_revision: number;
  completed_at: number;
  elapsed_ms: number;
  call_count: number;
  reported_call_count: number;
  usage_state: 'complete' | 'partial' | 'unavailable';
  // input is normalized uncached input, not the inclusive input parent shown in the UI.
  tokens: { input: number; cache_read: number; cache_write: number; output: number; total: number } | null;
  models: string[];
};

const nonNegative = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) >= 0;
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

// Invalid optional telemetry must never erase a valid answer or keep a task
// running. Both live and history decode it through this same boundary.
export function decodeRoundSummary(value: unknown): RoundSummary | undefined {
  if (!object(value)) return undefined;
  for (const key of ['attempt', 'input_revision', 'completed_at', 'elapsed_ms', 'call_count', 'reported_call_count']) {
    if (!nonNegative(value[key])) return undefined;
  }
  if (
    Number(value.attempt) < 1 ||
    Number(value.input_revision) < 1 ||
    Number(value.completed_at) > 8640000000000000 ||
    Number(value.reported_call_count) > Number(value.call_count)
  )
    return undefined;
  if (!['complete', 'partial', 'unavailable'].includes(String(value.usage_state))) return undefined;
  if (
    !Array.isArray(value.models) ||
    value.models.length > 256 ||
    value.models.some(
      (model) =>
        typeof model !== 'string' ||
        !model.trim() ||
        model.length > 256 ||
        Array.from(model).some((character) => character.charCodeAt(0) <= 31 || character.charCodeAt(0) === 127)
    )
  )
    return undefined;
  if (value.tokens !== null) {
    if (!object(value.tokens)) return undefined;
    for (const key of ['input', 'cache_read', 'cache_write', 'output', 'total'])
      if (!nonNegative(value.tokens[key])) return undefined;
  }
  if (value.usage_state === 'unavailable' ? value.tokens !== null : value.tokens === null) return undefined;
  return value as RoundSummary;
}
