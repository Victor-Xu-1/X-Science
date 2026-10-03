/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

export const CONTEXT_USAGE_CATEGORIES = ['systemPrompt', 'tools', 'messages', 'mcp', 'skills'] as const;
export type ContextUsageCategory = (typeof CONTEXT_USAGE_CATEGORIES)[number];
export type ContextUsageBreakdownRow = { key: ContextUsageCategory; tokens: number };
export type ContextUsageProgress = {
  phase: 'generating' | 'thinking' | 'compacting';
  observedAt: string;
  usedTokens: number;
  outputTokens: number;
};
export type ContextCompactionPolicy = {
  enabled: boolean;
  windowTokens: number;
  thresholdTokens: number;
  percent: number;
  source: 'window_percent' | 'token_override' | 'unknown';
};
export type ContextUsageSnapshot = {
  sessionId: string;
  requestId: string;
  model: string;
  observedAt: string;
  state: 'request' | 'complete' | 'failed';
  source: 'provider' | 'estimated';
  usedTokens: number;
  limitTokens: number;
  limitSource: 'configured' | 'model_profile' | 'unknown';
  outputTokens: number;
  hasMedia: boolean;
  inputEstimates: ContextUsageBreakdownRow[];
  progress?: ContextUsageProgress;
};
export type ContextUsageHistory = {
  sessionId: string;
  totalObserved: number;
  coverage: 'recorded' | 'latest_only';
  samples: ContextUsageSnapshot[];
  peak?: ContextUsageSnapshot;
};
export type ContextUsageResult =
  | { status: 'unavailable' }
  | {
      status: 'available';
      snapshot: ContextUsageSnapshot;
      autoCompaction?: ContextCompactionPolicy;
      history?: ContextUsageHistory;
    };

export class ContextUsageRequestError extends Error {
  constructor(
    message: string,
    readonly retryable: boolean
  ) {
    super(message);
  }
}

export function isContextUsageRetryable(error: unknown): boolean {
  return error instanceof ContextUsageRequestError ? error.retryable : error instanceof TypeError;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function isTokenCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

/** Validate the server contract; absent telemetry is not zero or "loading". */
export function parseContextUsage(payload: unknown, conversationId: string): ContextUsageResult {
  if (!isObject(payload)) throw new Error('Invalid context usage response');
  if (payload.status === 'unavailable') return { status: 'unavailable' };
  const value = payload.snapshot;
  if (
    payload.status !== 'available' ||
    !isObject(value) ||
    value.sessionId !== conversationId ||
    typeof value.requestId !== 'string' ||
    !value.requestId ||
    typeof value.model !== 'string' ||
    typeof value.observedAt !== 'string' ||
    !Number.isFinite(Date.parse(value.observedAt)) ||
    !['request', 'complete', 'failed'].includes(String(value.state)) ||
    !['provider', 'estimated'].includes(String(value.source)) ||
    !['configured', 'model_profile', 'unknown', 'runner_default'].includes(String(value.limitSource)) ||
    !isTokenCount(value.usedTokens) ||
    !isTokenCount(value.limitTokens) ||
    (value.limitSource === 'unknown' ? value.limitTokens !== 0 : value.limitTokens === 0) ||
    !isTokenCount(value.outputTokens) ||
    (isTokenCount(value.outputTokens) && isTokenCount(value.usedTokens) && value.outputTokens > value.usedTokens) ||
    (value.state !== 'complete' && (value.source !== 'estimated' || value.outputTokens !== 0)) ||
    typeof value.hasMedia !== 'boolean' ||
    !Array.isArray(value.inputEstimates) ||
    value.inputEstimates.length !== CONTEXT_USAGE_CATEGORIES.length
  ) {
    throw new Error('Invalid context usage record');
  }
  for (const [index, row] of value.inputEstimates.entries()) {
    if (!isObject(row) || row.key !== CONTEXT_USAGE_CATEGORIES[index] || !isTokenCount(row.tokens)) {
      throw new Error('Invalid context usage breakdown');
    }
  }
  const inputEstimateTotal = value.inputEstimates.reduce((sum, row) => sum + BigInt(row.tokens), BigInt(0));
  if (inputEstimateTotal + BigInt(value.outputTokens as number) > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new Error('Invalid context usage estimate total');
  }
  if (
    value.usedTokens > value.outputTokens &&
    value.inputEstimates.every((row) => (row as ContextUsageBreakdownRow).tokens === 0)
  ) {
    throw new Error('Missing context usage input weights');
  }
  const progress = value.progress;
  if (
    progress !== undefined &&
    (!isObject(progress) ||
      !['generating', 'thinking', 'compacting'].includes(String(progress.phase)) ||
      typeof progress.observedAt !== 'string' ||
      !Number.isFinite(Date.parse(progress.observedAt)) ||
      !isTokenCount(progress.usedTokens) ||
      !isTokenCount(progress.outputTokens) ||
      progress.usedTokens - value.usedTokens !== progress.outputTokens ||
      value.source !== 'estimated' ||
      value.state === 'complete' ||
      (progress.phase === 'compacting' && progress.outputTokens !== 0))
  )
    throw new Error('Invalid context usage progress');
  if (
    isObject(progress) &&
    isTokenCount(progress.outputTokens) &&
    inputEstimateTotal + BigInt(progress.outputTokens) > BigInt(Number.MAX_SAFE_INTEGER)
  ) {
    throw new Error('Invalid context usage progress');
  }
  const policy = payload.autoCompaction;
  if (
    policy !== undefined &&
    (!isObject(policy) ||
      typeof policy.enabled !== 'boolean' ||
      policy.windowTokens !== value.limitTokens ||
      !isTokenCount(policy.thresholdTokens) ||
      (policy.source === 'unknown'
        ? policy.thresholdTokens !== 0 || value.limitTokens !== 0
        : policy.thresholdTokens === 0) ||
      typeof policy.percent !== 'number' ||
      !Number.isFinite(policy.percent) ||
      Math.abs(policy.percent - (value.limitTokens > 0 ? (policy.thresholdTokens * 100) / value.limitTokens : 0)) >
        0.000001 ||
      (policy.source === 'window_percent' && value.limitTokens === 0) ||
      !['window_percent', 'token_override', 'unknown'].includes(String(policy.source)))
  )
    throw new Error('Invalid context compaction policy');
  // Rolling upgrade compatibility: retire historical guessed defaults without
  // rewriting their provider token receipts or binding them to a newer model.
  const legacyDefault = value.limitSource === 'runner_default';
  let history: ContextUsageHistory | undefined;
  if (payload.history !== undefined) {
    const raw = payload.history;
    if (
      !isObject(raw) ||
      raw.sessionId !== conversationId ||
      !isTokenCount(raw.totalObserved) ||
      !['recorded', 'latest_only'].includes(String(raw.coverage)) ||
      !Array.isArray(raw.samples) ||
      raw.samples.length === 0 ||
      raw.samples.length > 128 ||
      raw.totalObserved < raw.samples.length ||
      (raw.coverage === 'latest_only' && (raw.samples.length !== 1 || raw.totalObserved !== 1))
    ) {
      throw new Error('Invalid context usage history');
    }
    const samples = raw.samples.map((candidate) => {
      const parsed = parseContextUsage({ status: 'available', snapshot: candidate }, conversationId);
      if (parsed.status !== 'available') throw new Error('Invalid context history sample');
      return parsed.snapshot;
    });
    const latest = samples.at(-1)!;
    if (
      latest.model !== value.model ||
      latest.usedTokens !== value.usedTokens ||
      latest.outputTokens !== value.outputTokens ||
      latest.state !== value.state ||
      latest.source !== value.source ||
      latest.observedAt !== value.observedAt ||
      latest.limitTokens !== (legacyDefault ? 0 : value.limitTokens) ||
      latest.limitSource !== (legacyDefault ? 'unknown' : value.limitSource)
    ) {
      throw new Error('Invalid context history latest sample');
    }
    if (
      new Set(samples.map((sample) => sample.requestId)).size !== samples.length ||
      samples.at(-1)?.requestId !== value.requestId
    )
      throw new Error('Invalid context history order');
    let peak: ContextUsageSnapshot | undefined;
    if (raw.peak !== undefined) {
      const parsed = parseContextUsage({ status: 'available', snapshot: raw.peak }, conversationId);
      if (
        parsed.status !== 'available' ||
        parsed.snapshot.source !== 'provider' ||
        parsed.snapshot.state !== 'complete'
      ) {
        throw new Error('Invalid context history peak');
      }
      peak = parsed.snapshot;
      if (samples.some((sample) => sample.source === 'provider' && sample.usedTokens > peak!.usedTokens)) {
        throw new Error('Invalid context history peak');
      }
    }
    history = {
      sessionId: conversationId,
      totalObserved: raw.totalObserved,
      coverage: raw.coverage as ContextUsageHistory['coverage'],
      samples,
      ...(peak ? { peak } : {}),
    };
  }
  return {
    status: 'available',
    snapshot: (legacyDefault ? { ...value, limitTokens: 0, limitSource: 'unknown' } : value) as ContextUsageSnapshot,
    ...(history ? { history } : {}),
    ...(policy === undefined
      ? {}
      : {
          autoCompaction: legacyDefault
            ? ({
                enabled: (policy as ContextCompactionPolicy).enabled,
                windowTokens: 0,
                thresholdTokens:
                  (policy as ContextCompactionPolicy).source === 'token_override'
                    ? (policy as ContextCompactionPolicy).thresholdTokens
                    : 0,
                percent: 0,
                source: (policy as ContextCompactionPolicy).source === 'token_override' ? 'token_override' : 'unknown',
              } as ContextCompactionPolicy)
            : (policy as ContextCompactionPolicy),
        }),
  };
}

/** A stream estimate is separate from durable request/provider counters. */
export function projectContextUsage(snapshot: ContextUsageSnapshot): ContextUsageSnapshot {
  if (!snapshot.progress) return snapshot;
  return {
    ...snapshot,
    source: 'estimated',
    usedTokens: snapshot.progress.usedTokens,
    outputTokens: snapshot.progress.outputTokens,
    observedAt: snapshot.progress.observedAt,
  };
}

/**
 * Keep locally measured input estimates unchanged. Scaling them to the
 * provider's total would fabricate per-category accuracy and hide the delta.
 * Public response tokens are included under messages, with their source kept
 * separate in the diagnostics. The category sum need not equal provider usage.
 */
export function estimateContextUsageBreakdown(snapshot: ContextUsageSnapshot): ContextUsageBreakdownRow[] {
  const total = snapshot.inputEstimates.reduce((sum, row) => sum + BigInt(row.tokens), BigInt(snapshot.outputTokens));
  if (total > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Invalid context usage estimate total');
  const rows = snapshot.inputEstimates.map((row) => ({ ...row }));
  rows[CONTEXT_USAGE_CATEGORIES.indexOf('messages')].tokens += snapshot.outputTokens;
  return rows;
}

export async function fetchContextUsage(conversationId: string, signal: AbortSignal): Promise<ContextUsageResult> {
  const response = await fetch(`/api/conversations/${encodeURIComponent(conversationId)}/context-usage`, {
    credentials: 'include',
    cache: 'no-store',
    headers: { Accept: 'application/json' },
    signal,
  });
  if (!response.ok)
    throw new ContextUsageRequestError(
      `Context usage request failed: ${response.status}`,
      response.status === 408 || response.status === 429 || response.status >= 500
    );
  return parseContextUsage(await response.json(), conversationId);
}
