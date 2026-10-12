export type XScienceBillingRecord = {
  id: string;
  issuedAt: string;
  amount: number;
  currency: string;
};

export type XScienceBillingOverview = {
  plan: 'local-free';
  metering: 'credit-draft';
  credits: {
    initial: number;
    used: number;
    remaining: number;
  };
  modelRates: readonly XScienceModelRate[];
  bills: readonly XScienceBillingRecord[];
  invoices: readonly XScienceBillingRecord[];
};

export type XScienceModelRate = {
  id: 'light' | 'standard' | 'reasoning';
  multiplier: number;
  tokensPerCredit: number;
  capacityTokens: number;
};

export const X_SCIENCE_INITIAL_CREDITS = 10_000;
export const X_SCIENCE_BASE_TOKENS_PER_CREDIT = 1_000;

const MODEL_RATE_CONFIG = [
  { id: 'light', multiplier: 0.5 },
  { id: 'standard', multiplier: 1 },
  { id: 'reasoning', multiplier: 2 },
] as const;

function buildModelRate(id: XScienceModelRate['id'], multiplier: number): XScienceModelRate {
  return Object.freeze({
    id,
    multiplier,
    tokensPerCredit: X_SCIENCE_BASE_TOKENS_PER_CREDIT / multiplier,
    capacityTokens: Math.floor((X_SCIENCE_INITIAL_CREDITS * X_SCIENCE_BASE_TOKENS_PER_CREDIT) / multiplier),
  });
}

const MODEL_RATES = Object.freeze(MODEL_RATE_CONFIG.map(({ id, multiplier }) => buildModelRate(id, multiplier)));

export function calculateXScienceCredits(totalTokens: number, multiplier: number): number {
  if (!Number.isFinite(totalTokens) || !Number.isFinite(multiplier) || totalTokens <= 0 || multiplier <= 0) return 0;
  return Math.ceil((Math.floor(totalTokens) / X_SCIENCE_BASE_TOKENS_PER_CREDIT) * multiplier);
}

const LOCAL_BILLING_OVERVIEW: XScienceBillingOverview = Object.freeze({
  plan: 'local-free',
  metering: 'credit-draft',
  credits: Object.freeze({
    initial: X_SCIENCE_INITIAL_CREDITS,
    used: 0,
    remaining: X_SCIENCE_INITIAL_CREDITS,
  }),
  modelRates: MODEL_RATES,
  bills: Object.freeze([]),
  invoices: Object.freeze([]),
});

export function getXScienceBillingOverview(): XScienceBillingOverview {
  return LOCAL_BILLING_OVERVIEW;
}
