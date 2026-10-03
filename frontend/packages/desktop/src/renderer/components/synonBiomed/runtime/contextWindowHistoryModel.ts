/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  projectContextUsage,
  type ContextUsageHistory,
  type ContextUsageSnapshot,
} from '@/renderer/services/contextUsage';

export function formatContextTokens(count: number): string {
  if (Math.abs(count) >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
  if (Math.abs(count) >= 1_000) return `${(count / 1_000).toFixed(1)}K`;
  return String(count);
}

/** A peak is one observed call, never the sum of tokens charged across calls. */
export function summarizeContextWindowHistory(history: ContextUsageHistory) {
  const providerSamples = history.samples.filter((sample) => sample.source === 'provider');
  const peak =
    history.peak ??
    providerSamples.reduce<ContextUsageSnapshot | undefined>(
      (largest, sample) => (!largest || sample.usedTokens >= largest.usedTokens ? sample : largest),
      undefined
    );
  return {
    peak,
    providerCount: providerSamples.length,
    retainedCount: history.samples.length,
    truncated: history.totalObserved > history.samples.length,
  };
}

/** Never rebind an older receipt to the capacity of a different active model. */
export function lastConfirmedContextUsage(history: ContextUsageHistory | undefined, current: ContextUsageSnapshot) {
  return history?.samples.findLast(
    (sample) =>
      sample.source === 'provider' &&
      sample.model === current.model &&
      sample.limitTokens === current.limitTokens &&
      sample.limitSource === current.limitSource
  );
}

export function contextWindowChart(history: ContextUsageHistory) {
  const projected = history.samples.map(projectContextUsage);
  const maximum = Math.max(1, ...projected.map((sample) => sample.usedTokens));
  return projected.map((sample, index) => ({
    sample,
    index,
    height: sample.usedTokens / maximum,
    percent: sample.limitTokens > 0 ? (sample.usedTokens * 100) / sample.limitTokens : undefined,
  }));
}
