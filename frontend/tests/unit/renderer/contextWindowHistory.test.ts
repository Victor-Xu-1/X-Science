/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import { describe, expect, it } from 'vitest';
import {
  CONTEXT_USAGE_CATEGORIES,
  parseContextUsage,
  type ContextUsageSnapshot,
  type ContextUsageHistory,
} from '@/renderer/services/contextUsage';
import {
  contextWindowChart,
  lastConfirmedContextUsage,
  summarizeContextWindowHistory,
} from '@/renderer/services/contextWindowHistory';

const sample = (requestId: string, usedTokens: number, limitTokens = 1000): ContextUsageSnapshot => ({
  sessionId: 'session',
  requestId,
  model: 'model',
  state: 'complete',
  source: 'provider',
  observedAt: '2026-10-04T00:00:00Z',
  usedTokens,
  limitTokens,
  limitSource: limitTokens ? 'model_profile' : 'unknown',
  outputTokens: 0,
  hasMedia: false,
  inputEstimates: CONTEXT_USAGE_CATEGORIES.map((key) => ({ key, tokens: 1 })),
});
const history = (): ContextUsageHistory => ({
  sessionId: 'session',
  totalObserved: 3,
  coverage: 'recorded',
  samples: [sample('one', 850), sample('two', 950), sample('three', 200)],
});

describe('context window history', () => {
  it('shows the real near-full window and post-rebuild decrease, not cumulative tokens', () => {
    const value = history();
    expect(summarizeContextWindowHistory(value).peak?.usedTokens).toBe(950);
    expect(contextWindowChart(value).map((point) => point.percent)).toEqual([85, 95, 20]);
    expect(contextWindowChart(value)[2].height).toBeCloseTo(200 / 950);
    const parsed = parseContextUsage({ status: 'available', snapshot: value.samples[2], history: value }, 'session');
    expect(parsed.status).toBe('available');
  });

  it('retains an observed peak after sample truncation with its own model capacity', () => {
    const value = history();
    value.totalObserved = 150;
    value.peak = { ...sample('old-peak', 990, 2000), model: 'earlier-model' };
    expect(summarizeContextWindowHistory(value)).toMatchObject({
      truncated: true,
      peak: { usedTokens: 990, limitTokens: 2000, model: 'earlier-model' },
    });
    expect(() =>
      parseContextUsage({ status: 'available', snapshot: value.samples[2], history: value }, 'session')
    ).not.toThrow();
  });

  it('keeps a current local estimate separate from the last provider receipt and model switch', () => {
    const value = history();
    const current = { ...sample('current', 300), source: 'estimated' as const, state: 'request' as const };
    expect(lastConfirmedContextUsage(value, current)?.requestId).toBe('three');
    expect(lastConfirmedContextUsage(value, { ...current, model: 'new-model' })).toBeUndefined();
    expect(lastConfirmedContextUsage(value, { ...current, limitTokens: 0, limitSource: 'unknown' })).toBeUndefined();
    value.samples.push({
      ...current,
      limitTokens: 0,
      limitSource: 'unknown',
      progress: {
        phase: 'compacting',
        observedAt: '2026-10-04T00:00:01Z',
        usedTokens: 300,
        outputTokens: 0,
      },
    });
    expect(contextWindowChart(value).at(-1)?.percent).toBeUndefined();
    expect(summarizeContextWindowHistory(value).peak?.usedTokens).toBe(950);
  });

  it.each(['foreign', 'duplicate', 'order', 'capacity', 'count', 'peak', 'oversized', 'latest-model'])(
    'rejects invalid history: %s',
    (kind) => {
      const value = history();
      if (kind === 'foreign') value.samples[0].sessionId = 'foreign';
      if (kind === 'duplicate') value.samples[0].requestId = 'three';
      if (kind === 'order') value.samples.reverse();
      if (kind === 'capacity') value.samples[0].limitSource = 'unknown';
      if (kind === 'count') value.totalObserved = 0;
      if (kind === 'peak') value.peak = { ...sample('peak', 1), source: 'estimated' };
      if (kind === 'oversized') value.samples = Array(129).fill(sample('oversized', 1));
      if (kind === 'latest-model') value.samples[2].model = 'mismatched-model';
      expect(() =>
        parseContextUsage({ status: 'available', snapshot: sample('three', 200), history: value }, 'session')
      ).toThrow();
    }
  );
});
