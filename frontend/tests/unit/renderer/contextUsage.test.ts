/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  fetchContextUsage,
  parseContextUsage,
  projectContextUsage,
  reconcileContextUsageBreakdown,
  type ContextUsageSnapshot,
} from '@/renderer/services/contextUsage';

const snapshot = (): ContextUsageSnapshot => ({
  sessionId: 'one',
  requestId: 'request',
  model: 'model',
  observedAt: '2026-01-01T00:00:00Z',
  state: 'complete',
  source: 'provider',
  usedTokens: 20,
  limitTokens: 100,
  limitSource: 'configured',
  outputTokens: 3,
  hasMedia: false,
  inputEstimates: [
    { key: 'systemPrompt', tokens: 4 },
    { key: 'tools', tokens: 2 },
    { key: 'messages', tokens: 8 },
    { key: 'mcp', tokens: 1 },
    { key: 'skills', tokens: 1 },
  ],
});

describe('context usage contract', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keeps unknown capacity unknown and retires historical defaults without mutating receipts', () => {
    const legacy = {
      ...snapshot(),
      usedTokens: 52733,
      outputTokens: 969,
      limitTokens: 1000000,
      limitSource: 'runner_default',
    };
    const result = parseContextUsage(
      {
        status: 'available',
        snapshot: legacy,
        autoCompaction: {
          enabled: true,
          windowTokens: 1000000,
          thresholdTokens: 800000,
          percent: 80,
          source: 'window_percent',
        },
      },
      'one'
    );
    expect(result.status).toBe('available');
    if (result.status !== 'available') throw new Error('missing usage');
    expect(result.snapshot).toMatchObject({
      usedTokens: 52733,
      outputTokens: 969,
      limitTokens: 0,
      limitSource: 'unknown',
    });
    expect(result.autoCompaction).toMatchObject({ windowTokens: 0, thresholdTokens: 0, percent: 0, source: 'unknown' });
    expect(legacy.limitTokens).toBe(1000000);
    expect(parseContextUsage(result, 'one')).toEqual(result);
    expect(() =>
      parseContextUsage({ status: 'available', snapshot: { ...legacy, limitSource: 'unknown' } }, 'one')
    ).toThrow();
  });

  it('keeps stream estimates additive and validates the actual server compaction policy', () => {
    const value = {
      ...snapshot(),
      state: 'request' as const,
      source: 'estimated' as const,
      outputTokens: 0,
      progress: { phase: 'generating' as const, observedAt: '2026-01-01T00:00:01Z', usedTokens: 28, outputTokens: 8 },
    };
    const policy = { enabled: true, windowTokens: 100, thresholdTokens: 80, percent: 80, source: 'window_percent' };
    const result = parseContextUsage({ status: 'available', snapshot: value, autoCompaction: policy }, 'one');
    expect(result.status).toBe('available');
    const projected = projectContextUsage(value);
    expect(projected.usedTokens).toBe(28);
    expect(projected.outputTokens).toBe(8);
    expect(value.usedTokens).toBe(20);
    expect(reconcileContextUsageBreakdown(projected).reduce((total, row) => total + row.tokens, 0)).toBe(28);
    for (const progress of [
      { ...value.progress, usedTokens: 99 },
      { ...value.progress, phase: 'invented' },
      { ...value.progress, observedAt: 'invalid' },
    ]) {
      expect(() => parseContextUsage({ status: 'available', snapshot: { ...value, progress } }, 'one')).toThrow();
    }
    expect(() =>
      parseContextUsage({ status: 'available', snapshot: { ...value, source: 'provider' } }, 'one')
    ).toThrow();
    expect(() =>
      parseContextUsage({ status: 'available', snapshot: value, autoCompaction: { ...policy, percent: 50 } }, 'one')
    ).toThrow();
    expect(() =>
      parseContextUsage(
        { status: 'available', snapshot: value, autoCompaction: { ...policy, windowTokens: 1000 } },
        'one'
      )
    ).toThrow();
  });

  it('keeps provider total separate from estimated components and accepts zero', () => {
    const value = snapshot();
    expect(parseContextUsage({ status: 'available', snapshot: value }, 'one')).toEqual({
      status: 'available',
      snapshot: value,
    });
    value.usedTokens = 0;
    value.outputTokens = 0;
    value.inputEstimates.forEach((row) => {
      row.tokens = 0;
    });
    expect(parseContextUsage({ status: 'available', snapshot: value }, 'one').status).toBe('available');
    expect(reconcileContextUsageBreakdown(value).every((row) => row.tokens === 0)).toBe(true);
  });

  it('reconciles actual input weights to provider usage with deterministic largest remainders', () => {
    const value = snapshot();
    value.usedTokens = 10;
    value.outputTokens = 3;
    value.inputEstimates.forEach((row) => {
      row.tokens = 1;
    });
    expect(reconcileContextUsageBreakdown(value)).toEqual([
      { key: 'systemPrompt', tokens: 2 },
      { key: 'tools', tokens: 2 },
      { key: 'messages', tokens: 4 },
      { key: 'mcp', tokens: 1 },
      { key: 'skills', tokens: 1 },
    ]);
  });

  it('retains a small MCP segment and all five screenshot percentages above budget', () => {
    const value = snapshot();
    value.usedTokens = 348_200;
    value.outputTokens = 0;
    value.limitTokens = 300_000;
    value.inputEstimates = [
      { key: 'systemPrompt', tokens: 17_400 },
      { key: 'tools', tokens: 34_200 },
      { key: 'messages', tokens: 290_600 },
      { key: 'mcp', tokens: 300 },
      { key: 'skills', tokens: 5_700 },
    ];
    const rows = reconcileContextUsageBreakdown(value);
    expect(rows.map((row) => ((row.tokens / value.limitTokens) * 100).toFixed(1))).toEqual([
      '5.8',
      '11.4',
      '96.9',
      '0.1',
      '1.9',
    ]);
    expect(rows.reduce((total, row) => total + row.tokens, 0)).toBe(value.usedTokens);
  });

  it('does not lose tokens when the sum of input weights exceeds Number.MAX_SAFE_INTEGER', () => {
    const value = snapshot();
    value.usedTokens = Number.MAX_SAFE_INTEGER;
    value.outputTokens = 1;
    value.inputEstimates.forEach((row) => {
      row.tokens = Number.MAX_SAFE_INTEGER;
    });
    const rows = reconcileContextUsageBreakdown(value);
    expect(rows.reduce((total, row) => total + row.tokens, 0)).toBe(value.usedTokens);
    expect(rows[2].tokens).toBeGreaterThan(rows[0].tokens);
  });

  it('makes missing telemetry explicit instead of loading or zero', () => {
    expect(parseContextUsage({ status: 'unavailable' }, 'one')).toEqual({ status: 'unavailable' });
  });

  it.each([
    { usedTokens: -1 },
    { usedTokens: Number.NaN },
    { usedTokens: '20' },
    { limitTokens: 0 },
    { usedTokens: Number.MAX_SAFE_INTEGER + 1 },
    { outputTokens: 21 },
    { source: 'cumulative' },
    { state: 'invented' },
    { state: 'request', source: 'provider' },
    { state: 'failed', outputTokens: 3 },
    { limitSource: 'model_default' },
    { observedAt: 'bad date' },
    { sessionId: 'other' },
    { inputEstimates: [] },
    {
      inputEstimates: [
        { key: 'systemPrompt', tokens: 1 },
        { key: 'messages', tokens: 1 },
        { key: 'toolDefinitions', tokens: 1 },
      ],
    },
    { inputEstimates: [{ key: 'invented', tokens: 20 }] },
    {
      inputEstimates: [
        { key: 'systemPrompt', tokens: 0 },
        { key: 'tools', tokens: 0 },
        { key: 'messages', tokens: 0 },
        { key: 'mcp', tokens: 0 },
        { key: 'skills', tokens: 0 },
      ],
    },
  ])('rejects invalid or cross-conversation data: %j', (change) => {
    expect(() => parseContextUsage({ status: 'available', snapshot: { ...snapshot(), ...change } }, 'one')).toThrow();
  });

  it('only requests the authenticated uncached context endpoint', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'unavailable' }))));
    const signal = new AbortController().signal;
    await expect(fetchContextUsage('one / two', signal)).resolves.toEqual({ status: 'unavailable' });
    expect(fetch).toHaveBeenCalledWith(
      '/api/conversations/one%20%2F%20two/context-usage',
      expect.objectContaining({
        credentials: 'include',
        cache: 'no-store',
        signal,
      })
    );
  });

  it('rejects transport and invalid JSON failures', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(new Response('{}', { status: 503 }))
        .mockResolvedValueOnce(new Response('invalid'))
    );
    await expect(fetchContextUsage('one', new AbortController().signal)).rejects.toThrow();
    await expect(fetchContextUsage('one', new AbortController().signal)).rejects.toThrow();
  });
});
