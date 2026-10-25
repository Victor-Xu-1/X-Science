import React, { useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { ContextInspection } from '@/renderer/services/runtime/contextInspection';
import { estimateContextUsageBreakdown } from '@/renderer/services/contextUsage';
import { InspectionCallDetails, InspectionTurnDetails } from './ContextInspectionDetails';
import {
  CALL_COLORS,
  INSPECTION_COLORS,
  callSegments,
  inspectionCallBands,
  inspectionTurnPoints,
  knownInspectionNumber,
} from './contextInspectionModel';
import { formatContextTokens } from './contextWindowHistoryModel';
import styles from './ContextWindowInspector.module.css';

const PREFIX = 'conversation.contextInspector.';
type Granularity = 'turn' | 'call';

/** View-local pin/preview only. All telemetry remains owned by useContextUsage. */
export default function ContextInspectionHistory({
  inspection,
  granularity,
}: {
  inspection: ContextInspection;
  granularity: Granularity;
}) {
  const { t } = useTranslation();
  const calls = granularity === 'call';
  const bands = inspectionCallBands(inspection);
  const orderedCalls = bands.flatMap((band) => band.calls);
  const turnPoints = inspectionTurnPoints(inspection);
  const items = calls ? orderedCalls : turnPoints;
  const [pinned, setPinned] = useState<string>();
  const [preview, setPreview] = useState<string>();
  const nodes = useRef(new Map<string, HTMLButtonElement>());
  const scroller = useRef<HTMLDivElement>(null);
  const pinnedId = items.some((item) => item.id === pinned) ? pinned : items.at(-1)?.id;
  const activeId = items.some((item) => item.id === preview) ? preview : pinnedId;
  const index = items.findIndex((item) => item.id === activeId);
  const values = calls
    ? orderedCalls.map((call) => {
        const segments = callSegments(call);
        return segments.length ? segments.reduce((sum, segment) => sum + segment.tokens, 0) : undefined;
      })
    : turnPoints.map((turn) => turn.sample?.usedTokens);
  const maximum = Math.max(1, ...values.filter((value) => value !== undefined));
  useLayoutEffect(() => {
    if (!pinned && scroller.current) scroller.current.scrollLeft = scroller.current.scrollWidth;
  }, [items.length, pinned]);
  const renderPoint = (itemIndex: number) => {
    const item = items[itemIndex];
    const call = calls ? orderedCalls[itemIndex] : undefined;
    const sample = !calls ? turnPoints[itemIndex].sample : undefined;
    const rows = sample ? estimateContextUsageBreakdown(sample) : [];
    const categoryTotal = rows.reduce((sum, row) => sum + row.tokens, 0);
    const label = calls
      ? t(PREFIX + 'callNumber', { count: itemIndex + 1 })
      : t(PREFIX + 'outcomeNumber', {
          revision: turnPoints[itemIndex].inputRevision ?? '—',
          attempt: knownInspectionNumber(turnPoints[itemIndex].runnerAttempt) ?? '—',
        });
    const value = values[itemIndex];
    return (
      <div className={styles.pointColumn} key={item.id}>
        <button
          type='button'
          ref={(node) => {
            if (node) nodes.current.set(item.id, node);
            else nodes.current.delete(item.id);
          }}
          className={styles.point}
          data-testid={'context-inspector-' + granularity + '-point'}
          aria-label={
            label + ' · ' + (value === undefined ? t(PREFIX + 'usageUnavailable') : formatContextTokens(value))
          }
          aria-pressed={pinnedId === item.id}
          data-active={activeId === item.id || undefined}
          tabIndex={pinnedId === item.id ? 0 : -1}
          onPointerEnter={() => setPreview(item.id)}
          onPointerLeave={() => setPreview(undefined)}
          onFocus={() => setPreview(item.id)}
          onBlur={() => setPreview(undefined)}
          onClick={() => {
            setPinned((current) => (current === item.id || itemIndex === items.length - 1 ? undefined : item.id));
            nodes.current.get(item.id)?.focus({ preventScroll: true });
          }}
          onKeyDown={(event) => {
            const next =
              event.key === 'Home'
                ? 0
                : event.key === 'End'
                  ? items.length - 1
                  : event.key === 'ArrowLeft'
                    ? Math.max(0, itemIndex - 1)
                    : event.key === 'ArrowRight'
                      ? Math.min(items.length - 1, itemIndex + 1)
                      : undefined;
            if (next === undefined || event.nativeEvent.isComposing) return;
            event.preventDefault();
            nodes.current.get(items[next].id)?.focus({ preventScroll: true });
            nodes.current.get(items[next].id)?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
          }}
        >
          {value === undefined ? (
            <span className={styles.unknownBar} aria-hidden='true'>
              ?
            </span>
          ) : (
            <span
              className={styles.bar}
              style={{ height: Math.max(2, (value / maximum) * 100) + '%' }}
              aria-hidden='true'
            >
              {call
                ? callSegments(call).map((segment) => (
                    <span
                      key={segment.field}
                      style={{
                        height: (value ? (segment.tokens / value) * 100 : 0) + '%',
                        background: CALL_COLORS[segment.field],
                      }}
                    />
                  ))
                : rows.map((row) => (
                    <span
                      key={row.key}
                      style={{
                        height: (categoryTotal ? (row.tokens / categoryTotal) * 100 : 0) + '%',
                        background: INSPECTION_COLORS[row.key],
                      }}
                    />
                  ))}
            </span>
          )}
        </button>
        {!calls && (
          <span className={styles.pointLabel} aria-hidden='true'>
            {turnPoints[itemIndex].inputRevision ?? '—'}
          </span>
        )}
      </div>
    );
  };
  const inProgress = !calls ? inspection.turns.filter((turn) => ['pending', 'running'].includes(turn.status)) : [];
  const progressNotice = inProgress.length > 0 && (
    <div className={styles.notice} data-testid='context-inspector-in-progress' role='status'>
      <strong>{t(PREFIX + 'inProgress')}</strong>
      {inProgress.map((turn) => (
        <span key={turn.id}>
          {t(PREFIX + 'inputNumber', { revision: knownInspectionNumber(turn.inputRevision) ?? '—' })} ·{' '}
          {t(PREFIX + 'status_' + turn.status)}
        </span>
      ))}
    </div>
  );
  const unknownTurns = !calls ? inspection.turns.filter((turn) => turn.status === 'unknown') : [];
  const unresolvedNotice = unknownTurns.length > 0 && (
    <div className={styles.notice} data-testid='context-inspector-unresolved-turns' role='status'>
      <strong>{t(PREFIX + 'unresolvedTurns')}</strong>
      {unknownTurns.map((turn) => (
        <span key={turn.id}>
          {t(PREFIX + 'inputNumber', { revision: knownInspectionNumber(turn.inputRevision) ?? '—' })} ·{' '}
          {t(PREFIX + 'statusUnknown')}
        </span>
      ))}
    </div>
  );
  if (!items.length)
    return (
      <>
        {progressNotice}
        {unresolvedNotice}
        <div className={styles.empty} role='status'>
          {t(PREFIX + (calls ? 'noCalls' : 'noTurns'))}
        </div>
      </>
    );
  return (
    <section aria-label={t(PREFIX + (calls ? 'callHistory' : 'history'))}>
      {progressNotice}
      {unresolvedNotice}
      <h3>{t(PREFIX + (calls ? 'callHistory' : 'history'))}</h3>
      <p className={styles.muted}>{t(PREFIX + (calls ? 'callHistoryHelp' : 'historyHelp'))}</p>
      <div className={styles.historyCard}>
        <div className={styles.chartArea}>
          <div className={styles.axis} aria-hidden='true'>
            <span>{formatContextTokens(maximum)}</span>
            <span>{formatContextTokens(maximum / 2)}</span>
            <span>0</span>
          </div>
          <div className={styles.chartScroller} ref={scroller}>
            <div
              className={styles.chart}
              style={{ minWidth: Math.max(560, items.length * (calls ? 48 : 40)) + 'px' }}
              role='group'
              aria-label={t(PREFIX + (calls ? 'callChart' : 'turnChart'))}
            >
              {calls
                ? bands.map((band) => (
                    <div key={band.turnId} className={styles.callBand} data-testid='context-inspector-call-band'>
                      {band.calls.map((call) => renderPoint(orderedCalls.indexOf(call)))}
                      <span className={styles.turnLane} aria-hidden='true'>
                        {t(PREFIX + 'inputNumber', { revision: band.inputRevision ?? '—' })}
                      </span>
                    </div>
                  ))
                : items.map((_item, itemIndex) => renderPoint(itemIndex))}
            </div>
          </div>
        </div>
        {index >= 0 &&
          (calls ? (
            <InspectionCallDetails call={orderedCalls[index]} index={index} />
          ) : (
            <InspectionTurnDetails turn={turnPoints[index]} />
          ))}
      </div>
    </section>
  );
}
