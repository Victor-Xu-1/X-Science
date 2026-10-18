import React from 'react';
import { useTranslation } from 'react-i18next';
import type { ContextCompactionController } from '@/renderer/services/compaction/ContextCompactionController';
import { compactionUnresolved, type CompactionView } from '@/renderer/services/compaction/contextCompactionTypes';
import styles from './ContextCompactionAction.module.css';

export type ContextCompactionPresentation = {
  controller: ContextCompactionController | null;
  view: CompactionView;
  capacityLimited?: boolean;
  retryCapacity?: () => void;
};
function actionLabel(view: CompactionView, unresolved: boolean): string {
  if (!unresolved) return 'conversation.manualCompaction.compactLabel';
  if (view.uncertain) return 'conversation.manualCompaction.unconfirmed';
  if (!view.receipt)
    return view.mutation === 'submit'
      ? 'conversation.manualCompaction.submitting'
      : 'conversation.manualCompaction.pending';
  if (view.receipt.state === 'compacting') return 'conversation.manualCompaction.compactingLabel';
  if (view.receipt.execution_observation === 'draining') return 'conversation.manualCompaction.observation.draining';
  return `conversation.manualCompaction.state.${view.receipt.state}`;
}
export default function ContextCompactionAction({
  presentation,
  measured,
  percent,
}: {
  presentation?: ContextCompactionPresentation;
  measured: boolean;
  percent: number;
}) {
  const { t } = useTranslation();
  if (presentation?.capacityLimited)
    return (
      <div className={styles.root} data-testid='context-compaction-action'>
        <div role='alert' className={styles.status}>
          {t('conversation.manualCompaction.capacityLimited')}
        </div>
        <button
          type='button'
          className={styles.action}
          onClick={presentation.retryCapacity}
          disabled={!presentation.retryCapacity}
        >
          {t('conversation.manualCompaction.retryCapacity')}
        </button>
      </div>
    );
  if (!presentation?.controller) return null;
  const { controller, view } = presentation;
  const unresolved = compactionUnresolved(view);
  const labelKey = actionLabel(view, unresolved);
  const compacting = labelKey === 'conversation.manualCompaction.compactingLabel';
  const showAction = unresolved || (measured && percent >= 30);
  const visible = showAction || view.error || view.receipt?.archive_status === 'pending';
  if (!visible) return null;
  const enabled = !unresolved && !view.loading && !view.error && view.snapshot?.availability.available;
  return (
    <div className={styles.root} data-testid='context-compaction-action'>
      {showAction && (
        <button
          type='button'
          className={styles.action}
          disabled={!enabled}
          aria-label={t(labelKey)}
          title={
            unresolved
              ? t(labelKey)
              : enabled
                ? undefined
                : t(`conversation.manualCompaction.reason.${view.snapshot?.availability.reason ?? 'unavailable'}`)
          }
          onClick={() => {
            void controller.start();
          }}
        >
          <svg width='12' height='12' viewBox='0 0 16 16' fill='none' stroke='currentColor' aria-hidden='true'>
            <path d='M1 1l5 5M6 2v4H2M15 15l-5-5m0 4v-4h4' />
          </svg>
          {t(
            compacting
              ? 'conversation.manualCompaction.compacting'
              : unresolved
                ? labelKey
                : 'conversation.manualCompaction.compact'
          )}
        </button>
      )}
      {view.receipt && (
        <div role='status' className={styles.status}>
          {t(`conversation.manualCompaction.state.${view.receipt.state}`)}
          {' · '}
          {t(`conversation.manualCompaction.observation.${view.receipt.execution_observation}`)}
        </div>
      )}
      {view.waitingForPrompt && (
        <div role='status' className={styles.status}>
          {t('conversation.manualCompaction.waitingForPrompt')}
        </div>
      )}
      {view.receipt?.archive_status === 'pending' && (
        <div role='status' className={styles.status}>
          {t('conversation.manualCompaction.archivePending')}
        </div>
      )}
      {unresolved && !view.uncertain && (
        <button
          type='button'
          className={styles.action}
          disabled={Boolean(view.mutation)}
          onClick={() => {
            void controller.stop();
          }}
        >
          {t('conversation.manualCompaction.cancel')}
        </button>
      )}
      {view.error && (
        <div role='alert' className={styles.status}>
          {t(`conversation.manualCompaction.error.${view.error}`)}
        </div>
      )}
      {view.uncertain && view.error !== 'scope' && (
        <button
          type='button'
          className={styles.action}
          disabled={Boolean(view.mutation)}
          onClick={() => {
            void controller.retry();
          }}
        >
          {t(
            view.uncertain === 'submit'
              ? 'conversation.manualCompaction.retryOriginal'
              : 'conversation.manualCompaction.retryCancel'
          )}
        </button>
      )}
      {(view.error || unresolved) && (
        <button
          type='button'
          className={styles.action}
          disabled={view.loading || Boolean(view.mutation)}
          onClick={() => {
            void controller.refresh();
          }}
        >
          {t('conversation.manualCompaction.checkReceipt')}
        </button>
      )}
    </div>
  );
}
