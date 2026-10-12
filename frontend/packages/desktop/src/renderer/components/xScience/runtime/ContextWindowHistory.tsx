/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useId, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { type ContextUsageHistory } from '@/renderer/services/contextUsage';
import {
  contextDisplayWindow,
  contextWindowChart,
  formatContextTokens,
  summarizeContextWindowHistory,
} from './contextWindowHistoryModel';
import styles from './ContextUsagePanel.module.css';

/** Compact numeric history, with each receipt bound to its own model/window. */
export default function ContextWindowHistory({
  history,
  interactive = true,
}: {
  history: ContextUsageHistory;
  interactive?: boolean;
}) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<string>();
  const [expanded, setExpanded] = useState(false);
  const chartId = useId();
  const pointNodes = useRef(new Map<string, HTMLButtonElement>());
  const removedFocusedPoint = useRef(false);
  const positionedPoint = useRef<string | undefined>(undefined);
  const points = contextWindowChart(history);
  const point = points.find((candidate) => candidate.sample.requestId === selected) ?? points.at(-1)!;
  const { peak, truncated } = summarizeContextWindowHistory(history);
  const peakWindow = peak ? contextDisplayWindow(peak) : undefined;
  const prefix = 'conversation.contextUsage.';
  useLayoutEffect(() => {
    const restore = removedFocusedPoint.current;
    removedFocusedPoint.current = false;
    if (!expanded || !interactive) {
      positionedPoint.current = undefined;
      return;
    }
    const target = pointNodes.current.get(point.sample.requestId);
    if (restore && document.activeElement === document.body) {
      target?.focus({ preventScroll: true });
    }
    if (positionedPoint.current !== point.sample.requestId) {
      positionedPoint.current = point.sample.requestId;
      target?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
  });
  if (history.samples.length < 2 && history.totalObserved <= 1) {
    return (
      <div className={styles.historyMeta} data-testid='context-window-history'>
        {t(`${prefix}${history.coverage === 'latest_only' ? 'legacyHistoryShort' : 'recordedRequests'}`, {
          count: history.totalObserved,
        })}
      </div>
    );
  }
  return (
    <section className={styles.history} aria-label={t(`${prefix}historyTitle`)} data-testid='context-window-history'>
      <button
        type='button'
        className={styles.historyToggle}
        data-testid='context-window-history-toggle'
        aria-expanded={expanded}
        aria-controls={chartId}
        onClick={() => {
          if (!expanded) setSelected(point.sample.requestId);
          setExpanded(!expanded);
        }}
      >
        <strong>{t(`${prefix}historyTitle`)}</strong>
        <span>
          {t(`${prefix}historySummary`, {
            count: history.totalObserved,
            tokens: peak ? formatContextTokens(peak.usedTokens) : '—',
          })}
        </span>
        <span aria-hidden='true'>{expanded ? '▴' : '▾'}</span>
      </button>
      <div id={chartId} hidden={!expanded}>
        {expanded && (
          <>
            <div className={styles.policy} data-testid='context-window-peak'>
              <span>
                {t(`${prefix}providerPeak`)}: {peak ? formatContextTokens(peak.usedTokens) : '—'}
                {peakWindow
                  ? ` / ${formatContextTokens(peakWindow.windowTokens)} · ${peakWindow.percent.toFixed(1)}%${
                      peakWindow.isDefaultBudget ? ` · ${t(`${prefix}defaultWindow`)}` : ''
                    }`
                  : ''}
              </span>
              <span>{peak?.model}</span>
            </div>
            <div
              className={styles.historyChart}
              data-testid='context-window-history-chart'
              role='group'
              aria-label={t(`${prefix}historyChartLabel`)}
              title={t(`${prefix}historyCoverage`)}
            >
              {points.map(({ sample, index, height }) => {
                const label = `${index + 1} · ${sample.model} · ${
                  sample.source === 'estimated' ? '≈ ' : ''
                }${formatContextTokens(sample.usedTokens)} · ${new Date(sample.observedAt).toLocaleString()}`;
                return (
                  <button
                    type='button'
                    key={sample.requestId}
                    ref={(node) => {
                      const previous = pointNodes.current.get(sample.requestId);
                      if (node) pointNodes.current.set(sample.requestId, node);
                      else {
                        if (previous === document.activeElement) removedFocusedPoint.current = true;
                        pointNodes.current.delete(sample.requestId);
                      }
                    }}
                    title={label}
                    aria-label={label}
                    aria-pressed={sample.requestId === point.sample.requestId}
                    tabIndex={sample.requestId === point.sample.requestId ? 0 : -1}
                    onFocus={() => setSelected(sample.requestId)}
                    onClick={() => setSelected(sample.requestId)}
                    onKeyDown={(event) => {
                      if (!interactive || event.nativeEvent.isComposing || event.defaultPrevented) return;
                      const next =
                        event.key === 'Home'
                          ? 0
                          : event.key === 'End'
                            ? points.length - 1
                            : event.key === 'ArrowLeft'
                              ? Math.max(0, index - 1)
                              : event.key === 'ArrowRight'
                                ? Math.min(points.length - 1, index + 1)
                                : undefined;
                      if (next === undefined) return;
                      event.preventDefault();
                      const requestId = points[next].sample.requestId;
                      setSelected(requestId);
                      pointNodes.current.get(requestId)?.focus({ preventScroll: true });
                    }}
                    className={styles.historyPoint}
                    data-source={sample.source}
                    data-phase={sample.progress?.phase}
                  >
                    <span style={{ height: `${Math.max(2, height * 100)}%` }} />
                  </button>
                );
              })}
            </div>
            <div
              className={styles.policy}
              data-testid='context-window-selected'
              title={`${new Date(point.sample.observedAt).toLocaleString()}${point.isDefaultBudget ? ` · ${t(`${prefix}defaultWindowNote`, { tokens: formatContextTokens(point.windowTokens) })}` : ''}`}
            >
              <span>
                {point.sample.source === 'estimated' ? t(`${prefix}estimatedShort`) : t(`${prefix}providerShort`)} ·{' '}
                {point.sample.model}
              </span>
              <span>
                {point.sample.source === 'estimated' ? '≈ ' : ''}
                {formatContextTokens(point.sample.usedTokens)}
                {` / ${formatContextTokens(point.windowTokens)} · ${point.percent.toFixed(1)}%`}
                {point.isDefaultBudget ? ` · ${t(`${prefix}defaultWindow`)}` : ''}
              </span>
              {point.sample.progress?.phase === 'compacting' && (
                <span title={t(`${prefix}compactionObserved`)}>{t(`${prefix}compactionPreparing`)}</span>
              )}
              {truncated && <span>{t(`${prefix}historyTruncated`, { count: history.samples.length })}</span>}
            </div>
          </>
        )}
      </div>
    </section>
  );
}
