/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { type ContextUsageHistory } from '@/renderer/services/contextUsage';
import { contextWindowChart, formatContextTokens, summarizeContextWindowHistory } from './contextWindowHistoryModel';
import styles from './ContextUsagePanel.module.css';

/** Compact numeric history, with each receipt bound to its own model/window. */
export default function ContextWindowHistory({ history }: { history: ContextUsageHistory }) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<string>();
  const points = contextWindowChart(history);
  const point = points.find((candidate) => candidate.sample.requestId === selected) ?? points.at(-1)!;
  const { peak, providerCount, truncated } = summarizeContextWindowHistory(history);
  const prefix = 'conversation.contextUsage.';
  return (
    <section className={styles.history} aria-label={t(`${prefix}historyTitle`)} data-testid='context-window-history'>
      <div className={styles.historyHeading}>
        <strong>{t(`${prefix}historyTitle`)}</strong>
        <span>{t(`${prefix}recordedRequests`, { count: history.totalObserved })}</span>
      </div>
      <div className={styles.policy} data-testid='context-window-peak'>
        <span>
          {t(`${prefix}providerPeak`)}: {peak ? formatContextTokens(peak.usedTokens) : '—'}
          {peak && peak.limitTokens > 0
            ? ` / ${formatContextTokens(peak.limitTokens)} · ${((peak.usedTokens * 100) / peak.limitTokens).toFixed(1)}%`
            : ''}
        </span>
        <span>
          {peak?.model} · {t(`${prefix}providerSamples`, { count: providerCount })}
        </span>
      </div>
      <div className={styles.historyChart} role='group' aria-label={t(`${prefix}historyChartLabel`)}>
        {points.map(({ sample, index, height }) => {
          const label = `${index + 1} · ${sample.model} · ${sample.source === 'estimated' ? '≈ ' : ''}${formatContextTokens(sample.usedTokens)} · ${new Date(sample.observedAt).toLocaleString()}`;
          return (
            <button
              type='button'
              key={sample.requestId}
              title={label}
              aria-label={label}
              aria-pressed={sample.requestId === point.sample.requestId}
              onClick={() => setSelected(sample.requestId)}
              className={styles.historyPoint}
              data-source={sample.source}
              data-phase={sample.progress?.phase}
            >
              <span style={{ height: `${Math.max(2, height * 100)}%` }} />
            </button>
          );
        })}
      </div>
      <div className={styles.policy} data-testid='context-window-selected'>
        <span>
          {point.sample.source === 'estimated' ? t(`${prefix}estimatedSample`) : t(`${prefix}providerSample`)} ·{' '}
          {point.sample.model}
        </span>
        <span>
          {point.sample.source === 'estimated' ? '≈ ' : ''}
          {formatContextTokens(point.sample.usedTokens)}
          {point.percent !== undefined
            ? ` / ${formatContextTokens(point.sample.limitTokens)} · ${point.percent.toFixed(1)}%`
            : ` · ${t(`${prefix}unknownCapacity`)}`}
          {' · '}
          {new Date(point.sample.observedAt).toLocaleString()}
        </span>
        {point.sample.progress?.phase === 'compacting' && <span>{t(`${prefix}compactionObserved`)}</span>}
        <span>{t(`${prefix}historyCoverage`)}</span>
        {history.coverage === 'latest_only' && <span>{t(`${prefix}legacyHistoryUnavailable`)}</span>}
        {truncated && <span>{t(`${prefix}historyTruncated`, { count: history.samples.length })}</span>}
      </div>
    </section>
  );
}
