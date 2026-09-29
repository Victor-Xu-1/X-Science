import type { RoundSummary } from '@/common/chat/roundSummary';

type RoundUsageBreakdown = {
  inputTotal: number | null;
  output: number | null;
  total: number | null;
  state: 'balanced' | 'empty' | 'unavailable' | 'inconsistent';
  inputPercent: number | null;
};

/** The wire format normalizes input to uncached tokens across providers.
 * Cached reads/writes are children of the display's inclusive input total,
 * never additional peers of that total. Keep provider totals authoritative. */
export function projectRoundUsage(tokens: RoundSummary['tokens']): RoundUsageBreakdown {
  if (!tokens) return { inputTotal: null, output: null, total: null, state: 'unavailable', inputPercent: null };
  const input = tokens.input + tokens.cache_read + tokens.cache_write;
  const safe = Number.isSafeInteger(input) && Number.isSafeInteger(input + tokens.output);
  const balanced = safe && input + tokens.output === tokens.total;
  return {
    inputTotal: safe ? input : null,
    output: tokens.output,
    total: tokens.total,
    state: !balanced ? 'inconsistent' : tokens.total === 0 ? 'empty' : 'balanced',
    inputPercent: balanced && tokens.total > 0 ? (input / tokens.total) * 100 : null,
  };
}
