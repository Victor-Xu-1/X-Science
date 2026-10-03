/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Dropdown, Spin, Tooltip } from '@arco-design/web-react';
import { Close } from '@icon-park/react';
import React, { useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useContextUsage } from '@/renderer/hooks/synonBiomed/useContextUsage';
import {
  projectContextUsage,
  estimateContextUsageBreakdown,
  type ContextUsageCategory,
} from '@/renderer/services/contextUsage';
import styles from './ContextUsagePanel.module.css';
import ContextWindowHistory from './ContextWindowHistory';
import {
  formatContextTokens as formatTokenCount,
  lastConfirmedContextUsage,
} from '@/renderer/services/contextWindowHistory';

const RING_SIZE = 16;
const RING_STROKE_WIDTH = 2.5;

const COLORS: Record<ContextUsageCategory, string> = {
  systemPrompt: '#6366f1',
  tools: '#10b981',
  messages: '#f59e0b',
  mcp: '#8b5cf6',
  skills: '#ec4899',
};

/**
 * Bare usage ring; the management card owns its popover behavior.
 */
const UsageRing: React.FC<{ usedTokens: number; limitTokens: number; threshold?: number }> = ({
  usedTokens,
  limitTokens,
  threshold,
}) => {
  const percent = limitTokens > 0 ? (usedTokens / limitTokens) * 100 : 0;
  const radius = (RING_SIZE - RING_STROKE_WIDTH) / 2;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (Math.min(percent, 100) / 100) * circumference;
  return (
    <svg
      width={RING_SIZE}
      height={RING_SIZE}
      viewBox={`0 0 ${RING_SIZE} ${RING_SIZE}`}
      aria-hidden='true'
      style={{ transform: 'rotate(-90deg)', display: 'block' }}
    >
      <circle
        cx={RING_SIZE / 2}
        cy={RING_SIZE / 2}
        r={radius}
        fill='none'
        stroke='var(--color-fill-3)'
        strokeWidth={RING_STROKE_WIDTH}
      />
      <circle
        cx={RING_SIZE / 2}
        cy={RING_SIZE / 2}
        r={radius}
        fill='none'
        stroke={threshold !== undefined && usedTokens >= threshold ? '#d97706' : '#6366f1'}
        strokeWidth={RING_STROKE_WIDTH}
        strokeLinecap='round'
        strokeDasharray={circumference}
        strokeDashoffset={strokeDashoffset}
        style={{ transition: 'stroke-dashoffset 0.3s ease, stroke 0.3s ease' }}
      />
    </svg>
  );
};

type ContextUsagePanelProps = { conversationId: string; active?: boolean };

/** The server's latest main-agent request is the only usage authority. */
const ContextUsagePanel: React.FC<ContextUsagePanelProps> = ({ conversationId, active = false }) => {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const detailsId = useId();
  const { state, retry } = useContextUsage(conversationId, visible, active);
  const usage = state.status === 'available' ? projectContextUsage(state.snapshot) : null;
  const policy = state.status === 'available' ? state.autoCompaction : undefined;
  const history = state.status === 'available' ? state.history : undefined;
  const lastProvider = usage?.source === 'estimated' ? lastConfirmedContextUsage(history, usage) : undefined;
  const stale = state.refreshState === 'retrying' || state.refreshState === 'stale';
  const phase =
    usage?.state === 'failed'
      ? 'interrupted'
      : active && usage?.state === 'request'
        ? usage.progress?.phase || 'requesting'
        : 'latest';
  const statusLabel = t(`conversation.contextUsage.${phase}`);
  const policyLabel = !policy
    ? t('conversation.contextUsage.policyUnavailable')
    : !policy.enabled
      ? t('conversation.contextUsage.compactionDisabled')
      : policy.source === 'unknown'
        ? t('conversation.contextUsage.pressureRecovery')
        : t(
            policy.source === 'token_override'
              ? policy.windowTokens > 0
                ? 'conversation.contextUsage.customThreshold'
                : 'conversation.contextUsage.customThresholdTokens'
              : 'conversation.contextUsage.compactionTarget',
            { percent: Number(policy.percent.toFixed(1)), tokens: formatTokenCount(policy.thresholdTokens) }
          );
  const capacityKnown = Boolean(usage && usage.limitTokens > 0);
  const usagePercent = usage && capacityKnown ? (usage.usedTokens / usage.limitTokens) * 100 : 0;
  const rows = usage ? estimateContextUsageBreakdown(usage) : [];
  const localTotal = rows.reduce((sum, row) => sum + row.tokens, 0);
  // Keep the semantic legend order stable, but make the visual bar readable:
  // tiny shares are not swallowed by a later large segment and ties retain
  // the locally estimated category order. Never scale categories to a receipt.
  const barRows = rows.filter((row) => row.tokens > 0).toSorted((left, right) => left.tokens - right.tokens);
  const barTotal = usage ? Math.max(1, usage.limitTokens, localTotal) : 1;
  const remaining = usage ? Math.max(0, barTotal - localTotal) : 0;
  const labels = {
    systemPrompt: t('conversation.contextUsage.systemPrompt'),
    tools: t('conversation.contextUsage.toolsAndSubagents'),
    messages: t('conversation.contextUsage.messages'),
    mcp: t('conversation.contextUsage.connectorsAndMcp'),
    skills: t('conversation.contextUsage.skills'),
  };
  const details = usage
    ? [
        usage.source === 'provider'
          ? t('conversation.contextUsage.providerTotal')
          : t('conversation.contextUsage.estimatedTotal'),
        usage.limitSource === 'model_profile'
          ? t('conversation.contextUsage.modelProfileLimit')
          : usage.limitSource === 'configured'
            ? t('conversation.contextUsage.configuredLimit')
            : t('conversation.contextUsage.unknownCapacity'),
        t('conversation.contextUsage.estimatedBreakdown'),
        t('conversation.contextUsage.breakdownNote'),
        capacityKnown ? t('conversation.contextUsage.budgetNote') : t('conversation.contextUsage.compositionNote'),
        policyLabel,
        usage.progress ? t('conversation.contextUsage.streamEstimateNote') : '',
        usage.hasMedia ? t('conversation.contextUsage.mediaNote') : '',
        usage.state === 'request' ? t('conversation.contextUsage.requestPending') : '',
        usage.state === 'failed' ? t('conversation.contextUsage.failedRequest') : '',
        usage.state === 'complete'
          ? `${t('conversation.contextUsage.output')}: ${formatTokenCount(usage.outputTokens)}`
          : '',
        usage.model,
        new Date(usage.observedAt).toLocaleString(),
      ].filter(Boolean)
    : [];

  return (
    <Dropdown
      trigger='click'
      position='tl'
      popupVisible={visible}
      onVisibleChange={setVisible}
      droplist={
        <div
          className={styles.root}
          data-testid='context-usage-panel'
          role='dialog'
          aria-label={t('conversation.contextUsage.title')}
        >
          <div className={styles.header}>
            <span className={styles.headerTitle}>{t('conversation.contextUsage.title')}</span>
            <span className={styles.phase} role='status' data-testid='context-usage-phase'>
              {statusLabel}
            </span>
            <button
              type='button'
              className={styles.closeButton}
              aria-label={t('common.close')}
              data-testid='context-usage-close'
              onClick={() => {
                setVisible(false);
                triggerRef.current?.focus();
              }}
            >
              <Close theme='outline' size={24} strokeWidth={2.4} />
            </button>
          </div>
          {usage ? (
            <>
              <Tooltip
                position='top'
                trigger={['hover', 'focus']}
                content={
                  <div className={styles.details}>
                    {details.map((line, index) => (
                      <p key={index}>{line}</p>
                    ))}
                  </div>
                }
              >
                <div
                  className={styles.statRow}
                  tabIndex={0}
                  role='group'
                  aria-label={t('conversation.contextUsage.title')}
                  aria-describedby={detailsId}
                >
                  <span
                    className={styles.bigPercent}
                    data-testid={capacityKnown ? 'context-usage-percent' : 'context-usage-tokens'}
                  >
                    {capacityKnown
                      ? `${usagePercent.toFixed(1)}%`
                      : `${usage.source === 'estimated' ? '≈ ' : ''}${formatTokenCount(usage.usedTokens)}`}
                  </span>
                  <span className={styles.usedText}>
                    {capacityKnown
                      ? t('conversation.contextUsage.usedLabel')
                      : t('conversation.contextUsage.unknownCapacity')}{' '}
                    {capacityKnown && (
                      <strong>
                        {usage.source === 'estimated' ? '≈ ' : ''}
                        {formatTokenCount(usage.usedTokens)} / {formatTokenCount(usage.limitTokens)}
                      </strong>
                    )}
                  </span>
                </div>
              </Tooltip>
              <span id={detailsId} className={styles.screenReaderOnly}>
                {details.join(' · ')}
              </span>
              <div className={styles.bar} aria-hidden='true' data-testid='context-usage-bar'>
                {barRows.map((row) => (
                  <span
                    key={row.key}
                    data-category={row.key}
                    className={styles.barSegment}
                    style={{ flexGrow: row.tokens / barTotal, background: COLORS[row.key] }}
                  />
                ))}
                {remaining > 0 && <span className={styles.barRemaining} style={{ flexGrow: remaining / barTotal }} />}
              </div>
              <div className={styles.legend} data-testid='context-usage-legend'>
                {rows.map((row) => (
                  <div key={row.key} className={styles.legendRow}>
                    <span className={styles.legendLabel}>
                      <i className={styles.legendDot} style={{ background: COLORS[row.key] }} aria-hidden='true' />
                      {labels[row.key]}
                    </span>
                    <span className={styles.legendPercent}>≈ {formatTokenCount(row.tokens)}</span>
                  </div>
                ))}
              </div>
              <div className={styles.policy} data-testid='context-usage-diagnostics'>
                <span>
                  {t('conversation.contextUsage.localComposition')}: ≈ {formatTokenCount(localTotal)}
                </span>
                {usage.source === 'provider' && (
                  <span>
                    {t('conversation.contextUsage.providerDifference')}: {usage.usedTokens - localTotal > 0 ? '+' : ''}
                    {formatTokenCount(usage.usedTokens - localTotal)}
                  </span>
                )}
                {lastProvider && (
                  <span>
                    {t('conversation.contextUsage.lastConfirmed')}: {formatTokenCount(lastProvider.usedTokens)} ·{' '}
                    {new Date(lastProvider.observedAt).toLocaleTimeString()}
                  </span>
                )}
              </div>
              <div className={styles.policy} data-testid='context-usage-policy'>
                <span>{policyLabel}</span>
                <span>
                  {t(
                    capacityKnown
                      ? 'conversation.contextUsage.configuredWindow'
                      : 'conversation.contextUsage.compositionNote'
                  )}
                </span>
              </div>
              <div className={styles.policy} data-testid='context-usage-observation'>
                <span>
                  {usage.source === 'provider'
                    ? t('conversation.contextUsage.providerTotal')
                    : t('conversation.contextUsage.estimatedTotal')}
                </span>
                <span>
                  {usage.model} · {new Date(usage.observedAt).toLocaleString()}
                </span>
              </div>
              {history && <ContextWindowHistory history={history} />}
              {stale && (
                <div className={styles.refreshNotice} role='status'>
                  <span>
                    {t(
                      state.refreshState === 'retrying'
                        ? 'conversation.contextUsage.refreshRetrying'
                        : 'conversation.contextUsage.refreshStale'
                    )}
                  </span>
                  <button type='button' onClick={retry}>
                    {t('conversation.contextUsage.retry')}
                  </button>
                </div>
              )}
            </>
          ) : state.status === 'loading' ? (
            <div
              className={styles.loading}
              role='status'
              aria-label={t('conversation.contextUsage.loading')}
              data-testid='context-usage-loading'
            >
              <Spin size={16} />
            </div>
          ) : (
            <div className={styles.empty} role='status'>
              <span>
                {state.status === 'error'
                  ? t('conversation.contextUsage.loadFailed')
                  : t('conversation.contextUsage.unavailable')}
              </span>
              <button type='button' onClick={retry} className={styles.retry}>
                {t('conversation.contextUsage.retry')}
              </button>
            </div>
          )}
        </div>
      }
    >
      <button
        type='button'
        ref={triggerRef}
        data-testid='synon-biomed-context-usage-trigger'
        aria-label={t('conversation.contextUsage.title')}
        aria-expanded={visible}
        aria-haspopup='dialog'
        title={
          usage
            ? `${statusLabel} · ${usage.source === 'estimated' ? '≈ ' : ''}${capacityKnown ? `${usagePercent.toFixed(1)}%` : `${formatTokenCount(usage.usedTokens)} · ${t('conversation.contextUsage.unknownCapacity')}`}`
            : t('conversation.contextUsage.unavailable')
        }
        data-usage-state={state.status}
        className='inline-flex items-center justify-center gap-1 cursor-pointer border-0 bg-transparent p-0'
      >
        {usage && capacityKnown ? (
          <UsageRing
            usedTokens={usage.usedTokens}
            limitTokens={usage.limitTokens}
            threshold={policy?.enabled && policy.thresholdTokens > 0 ? policy.thresholdTokens : undefined}
          />
        ) : usage ? (
          <span className={styles.unknown} data-testid='context-usage-capacity-unknown' aria-hidden='true'>
            ?
          </span>
        ) : state.status === 'loading' ? (
          <Spin size={16} />
        ) : (
          <span className={styles.unknown} aria-hidden='true'>
            —
          </span>
        )}
        {usage && (
          <span className={styles.triggerValue} data-testid='context-usage-trigger-value'>
            {usage.source === 'estimated' ? '≈ ' : ''}
            {capacityKnown ? `${usagePercent.toFixed(0)}%` : formatTokenCount(usage.usedTokens)}
          </span>
        )}
      </button>
    </Dropdown>
  );
};

export default ContextUsagePanel;
