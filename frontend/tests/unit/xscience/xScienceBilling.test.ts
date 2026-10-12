import { describe, expect, it } from 'vitest';
import {
  calculateXScienceCredits,
  getXScienceBillingOverview,
  X_SCIENCE_BASE_TOKENS_PER_CREDIT,
  X_SCIENCE_INITIAL_CREDITS,
} from '@/renderer/services/xScienceBilling';

describe('X-Science credit draft', () => {
  it('uses 10,000 credits and 1,000 tokens per credit as the standard baseline', () => {
    const overview = getXScienceBillingOverview();

    expect(overview.credits).toEqual({
      initial: 10_000,
      used: 0,
      remaining: 10_000,
    });
    expect(X_SCIENCE_INITIAL_CREDITS * X_SCIENCE_BASE_TOKENS_PER_CREDIT).toBe(10_000_000);
    expect(overview.modelRates.map((rate) => rate.tokensPerCredit)).toEqual([2_000, 1_000, 500]);
  });

  it('rounds each request up and rejects invalid model inputs', () => {
    expect(calculateXScienceCredits(1_000, 1)).toBe(1);
    expect(calculateXScienceCredits(1_001, 1)).toBe(2);
    expect(calculateXScienceCredits(1_000, 0.5)).toBe(1);
    expect(calculateXScienceCredits(1_000, 2)).toBe(2);
    expect(calculateXScienceCredits(-1, 1)).toBe(0);
    expect(calculateXScienceCredits(Number.NaN, 1)).toBe(0);
    expect(calculateXScienceCredits(1_000, 0)).toBe(0);
  });
});
