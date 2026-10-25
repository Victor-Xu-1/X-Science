import React from 'react';
import { useTranslation } from 'react-i18next';
import type { ContextInspectionCall } from '@/renderer/services/runtime/contextInspection';
import { estimateContextUsageBreakdown, type ContextUsageSnapshot } from '@/renderer/services/contextUsage';
import { contextDisplayWindow, formatContextTokens } from './contextWindowHistoryModel';
import {
  CALL_COLORS,
  CONTEXT_CALL_FIELDS,
  INSPECTION_COLORS,
  reportedCallToken,
  knownInspectionNumber,
  type ContextInspectionTurnPoint,
} from './contextInspectionModel';
import styles from './ContextWindowInspector.module.css';

const PREFIX = 'conversation.contextInspector.';

export function InspectionComposition({ sample, title }: { sample: ContextUsageSnapshot; title: string }) {
  const { t } = useTranslation();
  const rows = estimateContextUsageBreakdown(sample);
  const window = contextDisplayWindow(sample);
  const categoryTotal = rows.reduce((sum, row) => sum + row.tokens, 0);
  const visualTotal = Math.max(1, sample.usedTokens, categoryTotal);
  const occupancy = window.windowTokens > 0 ? Math.min(1, visualTotal / window.windowTokens) : 1;
  return (
    <section className={styles.card} aria-label={title}>
      <h3>{title}</h3>
      <div className={styles.composition} aria-hidden='true'>
        {rows.map((row) => (
          <span
            key={row.key}
            style={{
              width: (categoryTotal ? (row.tokens / categoryTotal) * occupancy * 100 : 0) + '%',
              background: INSPECTION_COLORS[row.key],
            }}
          />
        ))}
      </div>
      <div className={styles.compositionGrid}>
        <div>
          <strong className={styles.largeValue}>
            {sample.source === 'estimated' ? '≈ ' : ''}
            {formatContextTokens(sample.usedTokens)}
          </strong>
          <span className={styles.muted}> / {formatContextTokens(window.windowTokens)}</span>
          <p className={styles.muted}>
            {sample.model} ·{' '}
            {t('conversation.contextUsage.' + (sample.source === 'provider' ? 'providerShort' : 'estimatedShort'))}
          </p>
          {window.isDefaultBudget && (
            <p className={styles.muted}>
              {t('conversation.contextUsage.defaultWindowNote', { tokens: formatContextTokens(window.windowTokens) })}
            </p>
          )}
        </div>
        <div className={styles.legend}>
          {rows.map((row) => (
            <div key={row.key}>
              <span>
                <i style={{ background: INSPECTION_COLORS[row.key] }} />
                {t(
                  'conversation.contextUsage.' +
                    {
                      tools: 'toolsAndSubagents',
                      mcp: 'connectorsAndMcp',
                      systemPrompt: 'systemPrompt',
                      messages: 'messages',
                      skills: 'skills',
                    }[row.key]
                )}
              </span>
              <span>≈ {formatContextTokens(row.tokens)}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export function InspectionTurnDetails({ turn }: { turn: ContextInspectionTurnPoint }) {
  const { t } = useTranslation();
  const known = ['completed', 'failed', 'cancelled', 'reclaimed', 'pending', 'running'].includes(turn.status);
  return (
    <section className={styles.details} data-testid='context-inspector-turn-details'>
      <div className={styles.detailHeading}>
        <h3>
          {t(PREFIX + 'outcomeNumber', {
            revision: turn.inputRevision ?? '—',
            attempt: knownInspectionNumber(turn.runnerAttempt) ?? '—',
          })}
        </h3>
        <span>{known ? t(PREFIX + 'status_' + turn.status) : t(PREFIX + 'statusUnknown')}</span>
      </div>
      {turn.sample ? (
        <InspectionComposition sample={turn.sample} title={t(PREFIX + 'windowUsed')} />
      ) : (
        <p className={styles.emptyValue}>{t(PREFIX + 'usageUnavailable')}</p>
      )}
      {turn.completedAt && <p className={styles.muted}>{new Date(turn.completedAt).toLocaleString()}</p>}
    </section>
  );
}

export function InspectionCallDetails({ call, index }: { call: ContextInspectionCall; index: number }) {
  const { t } = useTranslation();
  return (
    <section className={styles.details} data-testid='context-inspector-call-details'>
      <div className={styles.detailHeading}>
        <h3>{t(PREFIX + 'callNumber', { count: index + 1 })}</h3>
        <span>{t(PREFIX + 'callState_' + (call.failureKind ?? call.state))}</span>
      </div>
      <p className={styles.muted}>
        {call.model} · {new Date(call.observedAt).toLocaleString()}
      </p>
      <p className={styles.muted}>
        {t(PREFIX + 'outcomeNumber', {
          revision: knownInspectionNumber(call.inputRevision) ?? '—',
          attempt: knownInspectionNumber(call.runnerAttempt) ?? '—',
        })}
      </p>
      {call.state === 'failed' && !call.failureKind && (
        <p className={styles.muted}>{t(PREFIX + 'failureCauseUnknown')}</p>
      )}
      <div className={styles.callGrid}>
        <div>
          <span className={styles.muted}>{t(PREFIX + 'windowUsed')}</span>
          <p>
            <strong>{formatContextTokens(call.usedTokens)}</strong> /{' '}
            {call.limitTokens ? formatContextTokens(call.limitTokens) : '—'}
          </p>
          {call.limitTokens > 0 ? (
            <div className={styles.meter} aria-hidden='true'>
              <span style={{ width: Math.min(100, (call.usedTokens / call.limitTokens) * 100) + '%' }} />
            </div>
          ) : (
            <p className={styles.muted}>{t('conversation.contextUsage.unknownCapacity')}</p>
          )}
          <p className={styles.muted}>{t(PREFIX + 'contextNotBilling')}</p>
        </div>
        <dl className={styles.tokenRows}>
          {CONTEXT_CALL_FIELDS.map((field) => {
            const value = reportedCallToken(call, field);
            const invalid = call.callUsage?.invalid_fields?.[field];
            return (
              <div key={field}>
                <dt>
                  <i style={{ background: CALL_COLORS[field] }} />
                  {t('conversation.contextUsage.call_' + field)}
                </dt>
                <dd data-testid={'context-inspector-token-' + field}>
                  {invalid
                    ? t('conversation.contextUsage.callInvalid')
                    : value === undefined
                      ? t('conversation.contextUsage.callNotReported')
                      : formatContextTokens(value)}
                </dd>
              </div>
            );
          })}
        </dl>
      </div>
    </section>
  );
}
