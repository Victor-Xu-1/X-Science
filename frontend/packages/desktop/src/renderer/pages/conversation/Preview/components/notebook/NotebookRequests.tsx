import React from 'react';
import { useTranslation } from 'react-i18next';
import type { NotebookSessionController } from '@/renderer/services/notebook/NotebookSessionController';
import type { NotebookSessionSnapshot } from '@/renderer/services/notebook/NotebookSessionStore';
import styles from './notebook.module.css';

export function NotebookRequests({
  controller,
  snapshot,
}: {
  controller: NotebookSessionController;
  snapshot: NotebookSessionSnapshot;
}) {
  const { t } = useTranslation();
  return (
    <section className={styles.requests} aria-label={t('conversation.manualNotebook.requests')}>
      {snapshot.scanning && <p role='status'>{t('conversation.manualNotebook.scanning')}</p>}
      {!snapshot.scanComplete && !snapshot.scanning && (
        <p role='status'>{t('conversation.manualNotebook.scanIncomplete')}</p>
      )}
      {snapshot.nextCursor && (
        <button
          type='button'
          disabled={snapshot.scanning}
          onClick={() => {
            void controller.loadMorePending();
          }}
        >
          {t('conversation.manualNotebook.scanMore')}
        </button>
      )}
      {snapshot.requests.map((entry) => {
        const receipt = entry.receipt;
        const legacy = (receipt?.requestKind ?? entry.summary?.requestKind) === 'legacy';
        const live = controller.isLive(entry.target);
        const terminal = receipt?.state === 'terminal';
        const state = receipt?.state ?? entry.summary?.state ?? entry.phase;
        const status = terminal
          ? receipt?.disposition
          : entry.cancelIntent && state !== 'cancel_requested'
            ? 'cancelIntent'
            : state;
        const observationAge = receipt ? Date.now() - Date.parse(receipt.physical.sampledAt) : Number.NaN;
        const physicalState =
          receipt && !entry.error && observationAge >= -5000 && observationAge <= 15000
            ? receipt.physical.status
            : 'unavailable';
        const canCancel = !legacy && live && entry.request && !terminal && state !== 'outcome_unknown';
        return (
          <div
            className={styles.request}
            key={entry.id}
            data-testid='manual-notebook-request'
            data-request-id={entry.id}
          >
            <div className={styles.requestHeader}>
              <span>
                {entry.target.language} · {entry.target.environment}
              </span>
              <code>{entry.id.slice(0, 8)}</code>
              <strong>{t(`conversation.manualNotebook.status.${status ?? 'uncertain'}`)}</strong>
            </div>
            {legacy && <p>{t('conversation.manualNotebook.legacyReadOnly')}</p>}
            {!legacy && !live && <p>{t('conversation.manualNotebook.oldTargetReadOnly')}</p>}
            {receipt && (
              <p className={styles.observation}>
                {t('conversation.manualNotebook.physical')}:{' '}
                {t(`conversation.manualNotebook.physicalState.${physicalState}`)}
              </p>
            )}
            {entry.error && (
              <p role='alert' className={styles.error}>
                {t(`conversation.manualNotebook.${entry.error}`)}
              </p>
            )}
            {receipt?.rejection && (
              <pre className={styles.output}>
                {receipt.rejection.stdout}
                {receipt.rejection.stderr}
              </pre>
            )}
            {(entry.request || receipt) && (
              <details>
                <summary>{t('conversation.manualNotebook.frozenSource')}</summary>
                <pre className={styles.output}>{entry.request?.code ?? receipt?.source}</pre>
              </details>
            )}
            <div className={styles.requestActions}>
              <button
                type='button'
                onClick={() => {
                  void controller.lookup(entry.id);
                }}
              >
                {t('conversation.manualNotebook.checkReceipt')}
              </button>
              {entry.phase === 'uncertain' && entry.request && !receipt && live && (
                <button
                  type='button'
                  disabled={snapshot.commandBusy}
                  onClick={() => {
                    void controller.retry(entry.id);
                  }}
                >
                  {t('conversation.manualNotebook.retrySame')}
                </button>
              )}
              {canCancel && (
                <button
                  type='button'
                  disabled={entry.cancelling || state === 'cancel_requested'}
                  onClick={() => {
                    void controller.cancel(entry.id);
                  }}
                >
                  {t('conversation.manualNotebook.cancel')}
                </button>
              )}
            </div>
          </div>
        );
      })}
    </section>
  );
}
