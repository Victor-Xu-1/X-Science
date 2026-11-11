import { isLiteratureRestoreBulk } from '@/renderer/services/runtime/literatureRestoreBulkIntent';
import React, { useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import type {
  LiteratureMutationView,
  LiteratureMutationController,
} from '@/renderer/services/runtime/LiteratureMutationController';
import {
  isLiteratureReadingBulk,
  literatureReadingBulkProgress,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import './libraryBulkProgress.css';

/** Only the original pending plan and acknowledged child positions supply this projection. */
export function LibraryBulkProgress({
  view: summary,
  controller,
  scopeKey,
  message,
  resume,
  pause,
  discard,
}: {
  view: LiteratureMutationView;
  controller?: LiteratureMutationController;
  scopeKey: string;
  message?: 'resolveFailed' | 'emptyResolved' | 'operationFailed';
  resume: (stopAfterCurrent?: boolean) => Promise<void>;
  pause: () => Promise<void>;
  discard: () => Promise<void>;
}) {
  const read = () => summary;
  const view = useSyncExternalStore(
    controller?.subscribe ?? noSubscription,
    controller?.getSnapshot ?? read,
    controller?.getSnapshot ?? read
  );
  const { t } = useTranslation();
  const bulk = (value: unknown) => isLiteratureReadingBulk(value) || isLiteratureRestoreBulk(value);
  const pending = bulk(view.pending) ? view.pending : undefined;
  const intent = pending ?? (bulk(view.action) ? view.action : undefined);
  const attemptMessage = message ? (
    <p role={message === 'emptyResolved' ? 'status' : 'alert'}>{t(`conversation.librarySelection.${message}`)}</p>
  ) : null;
  // A retained historical action must not hide the outcome of this view's newer attempt.
  if (!intent || (!pending && attemptMessage && view.error !== 'storage'))
    return attemptMessage ? <div className='library-bulk-progress'>{attemptMessage}</div> : null;
  const restore = isLiteratureRestoreBulk(intent);
  const namespace = restore ? 'libraryRestore' : 'librarySelection';
  const progress = restore
    ? { confirmedItems: intent.confirmed_items, totalItems: intent.commands.length }
    : literatureReadingBulkProgress(intent);
  const active = view.mutating && bulk(view.action) && view.action.operation_id === intent.operation_id;
  const unknown = pending?.phase === 'sending' && !active;
  const key = active
    ? pending
      ? 'activeProgress'
      : 'preparingProgress'
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
      aria-label={t(restore ? 'conversation.libraryRestore.entry' : 'conversation.librarySelection.readingStatus')}
      aria-busy={active}
      className='library-bulk-progress'
    >
      {attemptMessage}
      <p role={view.error ? 'alert' : 'status'}>
        {view.error === 'storage'
          ? t('conversation.reading.libraryStorage')
          : t(`conversation.${namespace}.${key}`, { done: progress.confirmedItems, total: progress.totalItems })}
      </p>
      {intent.scope_key !== scopeKey && <p>{t(`conversation.${namespace}.previousScope`)}</p>}
      {unknown && <p>{t(`conversation.${namespace}.currentBatchPending`)}</p>}
      {pending ? (
        <div className='library-bulk-progress__actions'>
          {active ? (
            <button type='button' disabled={pending.paused} onClick={() => void pause()}>
              {t(`conversation.${namespace}.stopAfterBatch`)}
            </button>
          ) : (
            <>
              <button type='button' disabled={view.mutating} onClick={() => void resume()}>
                {t(`conversation.${namespace}.resume`)}
              </button>
              {pending.phase === 'ready' ? (
                <button type='button' disabled={view.mutating} onClick={() => void discard()}>
                  {t(`conversation.${namespace}.discardRemainder`)}
                </button>
              ) : (
                <button type='button' disabled={view.mutating} onClick={() => void resume(true)}>
                  {t(`conversation.${namespace}.stopAfterBatch`)}
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

const noSubscription = (): (() => void) => () => undefined;
