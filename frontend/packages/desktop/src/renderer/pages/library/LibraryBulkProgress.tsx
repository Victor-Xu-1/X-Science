import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureMutationView } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  isLiteratureReadingBulk,
  literatureReadingBulkProgress,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import './libraryBulkProgress.css';

/** Only the original pending plan and acknowledged child positions supply this projection. */
export function LibraryBulkProgress({
  view,
  scopeKey,
  message,
  resume,
  pause,
  discard,
}: {
  view: LiteratureMutationView;
  scopeKey: string;
  message?: 'resolveFailed' | 'emptyResolved' | 'startFailed';
  resume: (stopAfterCurrent?: boolean) => Promise<void>;
  pause: () => Promise<void>;
  discard: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const pending = isLiteratureReadingBulk(view.pending) ? view.pending : undefined;
  const intent = pending ?? (isLiteratureReadingBulk(view.action) ? view.action : undefined);
  if (!intent)
    return message ? (
      <p role='status' className='library-bulk-progress'>
        {t(`conversation.librarySelection.${message}`)}
      </p>
    ) : null;
  const progress = literatureReadingBulkProgress(intent);
  const active = Boolean(pending && view.mutating);
  const unknown = pending?.phase === 'sending' && !active;
  const key = active
    ? 'activeProgress'
    : unknown
      ? 'uncertainProgress'
      : !pending
        ? progress.confirmedItems === progress.totalItems
          ? 'completedProgress'
          : 'stoppedProgress'
        : pending.paused
          ? 'pausedProgress'
          : 'readyProgress';
  return (
    <section
      aria-label={t('conversation.librarySelection.readingStatus')}
      aria-busy={active}
      className='library-bulk-progress'
    >
      <p role={view.error ? 'alert' : 'status'}>
        {view.error === 'storage'
          ? t('conversation.reading.libraryStorage')
          : t(`conversation.librarySelection.${key}`, { done: progress.confirmedItems, total: progress.totalItems })}
      </p>
      {intent.scope_key !== scopeKey && <p>{t('conversation.librarySelection.previousScope')}</p>}
      {unknown && <p>{t('conversation.librarySelection.currentBatchPending')}</p>}
      {pending ? (
        <div className='library-bulk-progress__actions'>
          {active ? (
            <button type='button' disabled={pending.paused} onClick={() => void pause()}>
              {t('conversation.librarySelection.stopAfterBatch')}
            </button>
          ) : (
            <>
              <button type='button' onClick={() => void resume()}>
                {t('conversation.librarySelection.resume')}
              </button>
              {pending.phase === 'ready' ? (
                <button type='button' onClick={() => void discard()}>
                  {t('conversation.librarySelection.discardRemainder')}
                </button>
              ) : (
                <button type='button' onClick={() => void resume(true)}>
                  {t('conversation.librarySelection.stopAfterBatch')}
                </button>
              )}
            </>
          )}
        </div>
      ) : view.error === 'storage' ? (
        <button type='button' onClick={() => void resume()}>
          {t('common.retry')}
        </button>
      ) : null}
    </section>
  );
}
