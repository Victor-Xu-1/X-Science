import { describe, expect, it } from 'vitest';
import { projectRoundUsage } from '@/renderer/pages/conversation/Messages/roundUsageBreakdown';

describe('round token inclusion accounting', () => {
  it('includes cached read and write in the input parent exactly once', () => {
    const tokens = { input: 20, cache_read: 70, cache_write: 10, output: 25, total: 125 };
    const result = projectRoundUsage(tokens);
    expect(result).toEqual({ inputTotal: 100, output: 25, total: 125, state: 'balanced', inputPercent: 80 });
    expect(tokens.input).toBe(20);
  });
  it('handles all-cache, no-cache and reported zero without inventing progress', () => {
    expect(projectRoundUsage({ input: 0, cache_read: 100, cache_write: 0, output: 0, total: 100 }).inputPercent).toBe(
      100
    );
    expect(projectRoundUsage({ input: 10, cache_read: 0, cache_write: 0, output: 90, total: 100 }).inputPercent).toBe(
      10
    );
    expect(projectRoundUsage({ input: 0, cache_read: 0, cache_write: 0, output: 0, total: 0 })).toMatchObject({
      state: 'empty',
      inputPercent: null,
    });
    expect(projectRoundUsage(null)).toMatchObject({ state: 'unavailable', inputTotal: null, inputPercent: null });
  });
  it('retains reported totals without manufacturing a reconciled chart', () => {
    expect(projectRoundUsage({ input: 20, cache_read: 30, cache_write: 0, output: 10, total: 80 })).toMatchObject({
      inputTotal: 50,
      total: 80,
      state: 'inconsistent',
      inputPercent: null,
    });
    expect(projectRoundUsage({ input: 20, cache_read: 30, cache_write: 0, output: 10, total: 40 })).toMatchObject({
      inputTotal: 50,
      total: 40,
      state: 'inconsistent',
      inputPercent: null,
    });
  });
  it('does not round unsafe sums into apparently valid totals', () => {
    expect(
      projectRoundUsage({
        input: Number.MAX_SAFE_INTEGER,
        cache_read: 1,
        cache_write: 0,
        output: 0,
        total: Number.MAX_SAFE_INTEGER,
      })
    ).toMatchObject({ inputTotal: null, state: 'inconsistent', inputPercent: null });
  });
});
