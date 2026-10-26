import React, { useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { X } from 'lucide-react';
import type { ContextUsageState } from '@/renderer/hooks/xScience/useContextUsage';
import { contextInspectionIdentity } from '@/renderer/services/runtime/contextInspection';
import { projectContextUsage } from '@/renderer/services/contextUsage';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { containTabFocus, restoreScopedFocus } from '@/renderer/utils/focusScope';
import { contextDisplayWindow, formatContextTokens } from './contextWindowHistoryModel';
import ContextCompactionAction, { type ContextCompactionPresentation } from './ContextCompactionAction';
import ContextInspectionHistory from './ContextInspectionHistory';
import { InspectionComposition } from './ContextInspectionDetails';
import { summarizeInspectionCalls } from './contextInspectionModel';
import styles from './ContextWindowInspector.module.css';

const PREFIX = 'conversation.contextInspector.';
type Props = {
  open: boolean;
  ownerScope: string;
  state: ContextUsageState;
  returnFocus: HTMLButtonElement | null;
  compaction?: ContextCompactionPresentation;
  retry: () => void;
  onClose: () => void;
};

/** Independent presentation; never fetches, polls or records context data. */
export default function ContextWindowInspector({
  open,
  ownerScope,
  state,
  returnFocus,
  compaction,
  retry,
  onClose,
}: Props) {
  const { t } = useTranslation();
  const titleId = useId();
  const descriptionId = useId();
  const panelId = useId();
  const [tab, setTab] = useState<'turn' | 'call'>('turn');
  const root = useRef<HTMLDivElement>(null);
  const tabs = useRef(new Map<string, HTMLButtonElement>());
  const authority = useRef(ownerScope);
  authority.current = ownerScope;
  useLayoutEffect(() => {
    if (!open) return;
    const node = root.current;
    const account = getRendererAccountScopeToken();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    node?.focus({ preventScroll: true });
    return () => {
      if (document.body.style.overflow === 'hidden') document.body.style.overflow = overflow;
      if (authority.current === ownerScope && getRendererAccountScopeToken() === account)
        restoreScopedFocus(returnFocus, node);
    };
  }, [open, ownerScope, returnFocus]);
  useLayoutEffect(() => {
    setTab('turn');
  }, [ownerScope, open]);
  if (!open) return null;
  const inspection = state.status === 'available' || state.status === 'unavailable' ? state.inspection : undefined;
  const currentSample = state.status === 'available' ? projectContextUsage(state.snapshot) : undefined;
  const currentBranchSample =
    currentSample && inspection?.calls.some((call) => call.id === currentSample.requestId) ? currentSample : undefined;
  const sample = currentBranchSample ?? inspection?.turns.findLast((turn) => turn.sample)?.sample;
  const summary = inspection ? summarizeInspectionCalls(inspection) : undefined;
  const coverage = inspection?.coverage;
  const stale = state.refreshState === 'stale' || state.refreshState === 'retrying';
  const inspectionKey = inspection ? contextInspectionIdentity(inspection.scope) : ownerScope;
  // Retained branch history is never evidence of the current compaction threshold.
  const percent = currentSample ? contextDisplayWindow(currentSample).percent : 0;
  return createPortal(
    <div
      className={styles.overlay}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={root}
        role='dialog'
        aria-modal='true'
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        tabIndex={-1}
        className={styles.dialog}
        data-testid='context-window-inspector'
        onKeyDown={(event) => {
          if (event.key === 'Escape' && !event.defaultPrevented && !event.nativeEvent.isComposing) {
            event.preventDefault();
            onClose();
            return;
          }
          containTabFocus(root.current, event);
        }}
      >
        <header className={styles.header}>
          <div>
            <h2 id={titleId}>{t(PREFIX + 'title')}</h2>
            <p id={descriptionId}>{t(PREFIX + 'description')}</p>
          </div>
          <button
            type='button'
            onClick={onClose}
            aria-label={t(PREFIX + 'close')}
            data-testid='context-inspector-close'
            className={styles.close}
          >
            <X size={18} />
          </button>
        </header>
        <div className={styles.toolbar} role='tablist' aria-label={t(PREFIX + 'detailLevel')}>
          {(['turn', 'call'] as const).map((value) => (
            <button
              key={value}
              type='button'
              role='tab'
              ref={(node) => {
                if (node) tabs.current.set(value, node);
              }}
              id={panelId + '-' + value}
              aria-selected={tab === value}
              aria-controls={panelId}
              tabIndex={tab === value ? 0 : -1}
              onClick={() => setTab(value)}
              onKeyDown={(event) => {
                if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
                event.preventDefault();
                const next =
                  event.key === 'Home' ? 'turn' : event.key === 'End' ? 'call' : tab === 'turn' ? 'call' : 'turn';
                setTab(next);
                tabs.current.get(next)?.focus();
              }}
            >
              {t(PREFIX + (value === 'turn' ? 'turns' : 'calls'))}
            </button>
          ))}
        </div>
        <div id={panelId} role='tabpanel' aria-labelledby={panelId + '-' + tab} className={styles.body}>
          {stale && (
            <div role='status' className={styles.notice}>
              {t(
                'conversation.contextUsage.' + (state.refreshState === 'retrying' ? 'refreshRetrying' : 'refreshStale')
              )}
              <button type='button' onClick={retry}>
                {t('conversation.contextUsage.retry')}
              </button>
            </div>
          )}
          {coverage && (coverage.association !== 'complete' || coverage.truncated) && (
            <div role='status' className={styles.notice} data-testid='context-inspector-coverage'>
              <span>
                {t(PREFIX + (coverage.association === 'unavailable' ? 'associationUnavailable' : 'partialCoverage'))}
              </span>
              <span>
                {t(PREFIX + 'coverageCounts', {
                  retained: coverage.retainedCallCount,
                  observed: coverage.observedSampleCount,
                  unassociated: coverage.unassociatedCallCount,
                })}
              </span>
              {coverage.observedCallCount === undefined && <span>{t(PREFIX + 'observedCallsUnknown')}</span>}
              {coverage.truncated && <span>{t(PREFIX + 'truncated')}</span>}
            </div>
          )}
          {state.status === 'loading' || (!inspection && state.inspectionRequest === 'loading') ? (
            <div role='status' className={styles.empty}>
              {t(PREFIX + 'loading')}
            </div>
          ) : !inspection ? (
            <div role='status' className={styles.empty}>
              <h3>
                {t(
                  PREFIX +
                    (state.status === 'error' || state.inspectionRequest === 'error' ? 'loadFailed' : 'unavailable')
                )}
              </h3>
              <p>{t(PREFIX + 'associationUnavailable')}</p>
              <button type='button' onClick={retry}>
                {t('conversation.contextUsage.retry')}
              </button>
            </div>
          ) : (
            <>
              {tab === 'turn' && sample && (
                <InspectionComposition
                  sample={sample}
                  title={t(PREFIX + (currentBranchSample ? 'composition' : 'branchSample'))}
                />
              )}
              {tab === 'call' && summary && (
                <section className={styles.card} aria-label={t(PREFIX + 'callSummary')}>
                  <h3>{t(PREFIX + 'callSummary')}</h3>
                  <div className={styles.metrics}>
                    <div>
                      <span>{t(PREFIX + 'recordedCalls')}</span>
                      <strong>{summary.count}</strong>
                    </div>
                    <div>
                      <span>{t(PREFIX + 'knownTokens')}</span>
                      <strong>
                        {summary.knownTotal === undefined ? '—' : formatContextTokens(summary.knownTotal)}
                      </strong>
                      <small>{t(PREFIX + 'reportedTotals', { count: summary.reported, total: summary.count })}</small>
                    </div>
                    <div>
                      <span>{t(PREFIX + 'peakWindow')}</span>
                      <strong>{summary.peak ? formatContextTokens(summary.peak.usedTokens) : '—'}</strong>
                      <small>{summary.peak?.model}</small>
                    </div>
                  </div>
                </section>
              )}
              <ContextInspectionHistory key={inspectionKey + ':' + tab} inspection={inspection} granularity={tab} />
            </>
          )}
        </div>
        {compaction && (
          <footer className={styles.footer}>
            <ContextCompactionAction
              presentation={compaction}
              measured={Boolean(
                currentSample && currentSample.source === 'provider' && currentSample.state !== 'request'
              )}
              percent={percent}
            />
          </footer>
        )}
      </div>
    </div>,
    document.body
  );
}
