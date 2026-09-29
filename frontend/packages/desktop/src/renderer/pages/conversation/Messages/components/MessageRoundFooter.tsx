import type { RoundSummary } from '@/common/chat/roundSummary';
import { Popover } from '@arco-design/web-react';
import { IconThunderbolt, IconCode } from '@arco-design/web-react/icon';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { projectRoundUsage } from '../roundUsageBreakdown';
import styles from './MessageRoundFooter.module.css';

export function formatRoundDuration(milliseconds: number): string {
  const seconds = Math.floor(milliseconds / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m ${seconds % 60}s`;
}

const inputCategories = ['input', 'cache_read', 'cache_write'] as const;
export default function MessageRoundFooter({ summary }: { summary: RoundSummary }) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const numbers = new Intl.NumberFormat(i18n.language);
  const date = new Date(summary.completed_at);
  const completed = new Intl.DateTimeFormat(i18n.language, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
  const label = (key: string) => t(`messages.roundSummary.${key}`);
  const usage = projectRoundUsage(summary.tokens);
  const value = (key: keyof NonNullable<RoundSummary['tokens']>) =>
    summary.tokens ? numbers.format(summary.tokens[key]) : label('unavailable');
  const content = (
    <section
      className={styles.panel}
      aria-label={label('calls')}
      data-testid='round-usage-panel'
      onKeyDown={(event) => {
        if (event.key === 'Escape') setOpen(false);
      }}
    >
      <header className={styles.header}>
        <strong>{label('calls')}</strong>
        <span>
          {summary.call_count > 0
            ? t('messages.roundSummary.callCount', {
                count: summary.call_count,
              })
            : label('unavailable')}
        </span>
      </header>
      {usage.inputPercent !== null && (
        <>
          <p className={styles.caption}>{label('composition')}</p>
          <div
            className={styles.bar}
            data-testid='round-usage-bar'
            role='img'
            aria-label={t('messages.roundSummary.compositionDescription', {
              input: numbers.format(usage.inputTotal!),
              output: value('output'),
              total: value('total'),
            })}
          >
            <i data-category='input' style={{ width: `${usage.inputPercent}%` }} />
            <i data-category='output' style={{ width: `${100 - usage.inputPercent}%` }} />
          </div>
        </>
      )}
      <dl className={styles.legend}>
        <div className={styles.inputGroup}>
          <dt>
            <i data-category='input' />
            {label('inputTotal')}
          </dt>
          <dd data-usage-row='input_total'>
            {usage.inputTotal === null ? label('unavailable') : numbers.format(usage.inputTotal)}
          </dd>
          <dd className={styles.inputDetails}>
            <dl data-testid='round-input-breakdown' aria-label={label('inputBreakdown')}>
              {inputCategories.map((key) => (
                <div key={key} data-usage-row={key}>
                  <dt>
                    <i data-category={key === 'input' ? 'uncached' : key} />
                    {label(key === 'input' ? 'uncached' : key)}
                  </dt>
                  <dd>{value(key)}</dd>
                </div>
              ))}
            </dl>
          </dd>
        </div>
        <div data-usage-row='output'>
          <dt>
            <i data-category='output' />
            {label('output')}
          </dt>
          <dd>{value('output')}</dd>
        </div>
        <div className={styles.total}>
          <dt>{label('total')}</dt>
          <dd>{value('total')}</dd>
        </div>
      </dl>
      {usage.state === 'inconsistent' && <p className={styles.notice}>{label('inconsistentUsage')}</p>}
      {summary.usage_state !== 'complete' && (
        <p className={styles.notice}>
          {summary.usage_state === 'partial'
            ? t('messages.roundSummary.partial', {
                count: summary.reported_call_count,
                total: summary.call_count,
              })
            : label('missingUsage')}
        </p>
      )}
      <div className={styles.identity}>
        <IconCode className={styles.icon} />
        <span>
          {label('model')}：{summary.models.join(' / ') || label('unavailable')}
        </span>
      </div>
    </section>
  );
  return (
    <div className={styles.footer} data-testid='message-round-footer'>
      <time className={styles.meta} dateTime={date.toISOString()} title={date.toLocaleString(i18n.language)}>
        {label('completed')} {completed}
      </time>
      <span className={styles.meta}>
        {label('elapsed')} {formatRoundDuration(summary.elapsed_ms)}
      </span>
      <Popover trigger='click' position='top' content={content} popupVisible={open} onVisibleChange={setOpen}>
        <button
          type='button'
          className={styles.trigger}
          aria-expanded={open}
          aria-label={label('calls')}
          onKeyDown={(event) => {
            if (event.key === 'Escape') setOpen(false);
          }}
        >
          <IconThunderbolt className={styles.icon} />
          {label('calls')}
        </button>
      </Popover>
    </div>
  );
}
