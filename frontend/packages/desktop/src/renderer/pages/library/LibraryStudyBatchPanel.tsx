import React, { useEffect, useId, useRef, useState } from 'react';
import { Check, ChevronRight, LoaderCircle, Pause, Sparkles, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { loadLiteratureItem } from '@/renderer/services/runtime/literatureLibrary';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { useLibraryStudyBatch } from './useLibraryStudyBatch';
import { LibraryStudyBatchResult } from './LibraryStudyBatchResult';
import './libraryStudyBatch.css';

const P = 'conversation.libraryStudyBatch.';
export function LibraryStudyBatchPanel({
  account,
  model,
  onOpenDetails,
  returnFocusRef,
}: {
  account: string;
  model: ReturnType<typeof useLibraryStudyBatch>;
  onOpenDetails: (id: string) => void;
  returnFocusRef?: React.RefObject<HTMLButtonElement | null>;
}) {
  const { t } = useTranslation();
  const id = useId();
  const progress = useRef<HTMLButtonElement>(null);
  const [expanded, setExpanded] = useState<number[]>([]);
  const [titles, setTitles] = useState<Record<string, string>>({});
  const batch = model.view?.batch;
  useEffect(() => {
    setExpanded([]);
    setTitles({});
  }, [account, batch?.id]);
  useEffect(() => {
    const controller = new AbortController();
    if (batch)
      void Promise.all(
        batch.rows.map(async (row) => {
          const item = await loadLiteratureItem(row.item_id, controller.signal).catch((): undefined => undefined);
          return [row.item_id, item?.title] as const;
        })
      ).then((values) => {
        if (!controller.signal.aborted && account === getRendererAccountScopeToken())
          setTitles(
            Object.fromEntries(values.filter((value): value is readonly [string, string] => value[1] !== undefined))
          );
      });
    return () => controller.abort();
  }, [account, batch?.id]);
  if (!batch && !model.issue && !model.busy) return null;
  const done = batch?.rows.filter((row) => row.state === 'succeeded').length ?? 0;
  const pending = batch?.rows.some((row) => row.state === 'pending' || row.state === 'running');
  const failed = batch?.rows.some((row) => row.state === 'failed');
  const close = () => {
    model.setOpen(false);
    (returnFocusRef?.current?.isConnected ? returnFocusRef.current : progress.current)?.focus({ preventScroll: true });
  };
  return (
    <section className='library-study-batch' aria-label={t(P + 'title')}>
      <button
        ref={progress}
        type='button'
        className='library-study-batch__trigger'
        aria-expanded={model.open}
        aria-controls={id}
        onClick={() => model.setOpen(!model.open)}
      >
        {model.busy ? (
          <LoaderCircle size={14} className='library-study__spinner' aria-hidden='true' />
        ) : (
          <Sparkles size={14} aria-hidden='true' />
        )}
        <span>{t(P + 'title')}</span>
        <span>{batch ? t(P + 'progress', { done, total: batch.rows.length }) : t(P + 'preparing')}</span>
        <ChevronRight size={13} className={model.open ? 'library-study-batch__chevron-open' : ''} aria-hidden='true' />
      </button>
      {model.open && (
        <div
          id={id}
          className='library-study-batch__card'
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              event.stopPropagation();
              close();
            }
          }}
        >
          <header>
            <h3>{t(P + 'title')}</h3>
            <button type='button' aria-label={t(P + 'close')} onClick={close}>
              <X size={15} aria-hidden='true' />
            </button>
          </header>
          {(batch || model.busy) && (
            <p className='library-study-batch__muted' role='status'>
              {t(
                P +
                  (model.pausing
                    ? 'pausing'
                    : model.busy
                      ? 'working'
                      : batch?.paused && pending
                        ? 'paused'
                        : pending
                          ? 'ready'
                          : failed
                            ? 'someFailed'
                            : 'completed')
              )}
            </p>
          )}
          {model.issue && <p role='alert'>{t(P + model.issue, { count: model.view?.limit ?? 5 })}</p>}
          {batch && (
            <ol>
              {batch.rows.map((row, index) => (
                <li key={row.item_id}>
                  {row.state === 'succeeded' ? (
                    <>
                      <button
                        type='button'
                        className='library-study-batch__row'
                        aria-expanded={expanded.includes(index)}
                        aria-controls={`${id}-${index}`}
                        onClick={() =>
                          setExpanded((value) =>
                            value.includes(index) ? value.filter((i) => i !== index) : [...value, index]
                          )
                        }
                      >
                        <Check size={15} aria-hidden='true' />
                        <span>{titles[row.item_id] ?? t(P + 'paper', { number: index + 1 })}</span>
                        <ChevronRight
                          size={14}
                          className={expanded.includes(index) ? 'library-study-batch__chevron-open' : ''}
                          aria-hidden='true'
                        />
                      </button>
                      {expanded.includes(index) && (
                        <div id={`${id}-${index}`}>
                          <LibraryStudyBatchResult
                            key={JSON.stringify([account, batch.id, row.request.operation_id, row.result_revision])}
                            account={account}
                            batchId={batch.id}
                            index={index}
                            row={row}
                            onOpenDetails={(item) => {
                              model.setOpen(false);
                              onOpenDetails(item);
                            }}
                          />
                        </div>
                      )}
                    </>
                  ) : (
                    <div className='library-study-batch__row'>
                      {row.state === 'running' ? (
                        <LoaderCircle size={15} className='library-study__spinner' aria-hidden='true' />
                      ) : (
                        <span className='library-study-batch__dot' />
                      )}
                      <span>{titles[row.item_id] ?? t(P + 'paper', { number: index + 1 })}</span>
                      <small>{t(P + 'state.' + row.state)}</small>
                    </div>
                  )}
                  {row.state === 'failed' && (
                    <p className='library-study-batch__row-issue'>
                      {t(P + 'issue.' + (row.issue ?? 'generation-failed'))}
                    </p>
                  )}
                </li>
              ))}
            </ol>
          )}
          <footer>
            {model.busy && (
              <button
                type='button'
                className='library-workbench__button'
                disabled={model.pausing}
                onClick={() => void model.pause()}
              >
                <Pause size={13} aria-hidden='true' />
                {t(P + 'pause')}
              </button>
            )}
            {!model.busy && pending && (
              <button type='button' className='library-workbench__button' onClick={() => void model.resume()}>
                {t(P + 'resume')}
              </button>
            )}
            {!model.busy && failed && (
              <button type='button' className='library-workbench__button' onClick={() => void model.retry()}>
                {t(P + 'retry')}
              </button>
            )}
            <button
              type='button'
              className='library-workbench__button'
              disabled={model.busy}
              onClick={() => void model.refresh()}
            >
              {t(P + 'refresh')}
            </button>
          </footer>
        </div>
      )}
    </section>
  );
}
