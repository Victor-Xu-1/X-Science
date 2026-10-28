import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureDirection, RelatedRecord } from '@/common/adapter/literatureRelated';
import React, { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { useLibraryRelatedLookup } from './useLibraryRelatedLookup';
import { useRelatedInboxStage } from './useRelatedInboxStage';

function RelatedPaper({ record }: { record: RelatedRecord }) {
  const { t } = useTranslation();
  const href = /^[a-f0-9]{40}$/i.test(record.paper_id)
    ? `https://www.semanticscholar.org/paper/${record.paper_id}`
    : undefined;
  return (
    <article className='pdf-library-related__paper'>
      <h4>{record.title}</h4>
      {record.authors.length > 0 ? (
        <div className='pdf-library-related__authors'>{record.authors.join(', ')}</div>
      ) : null}
      {record.year !== null || record.container_title ? (
        <div className='pdf-library-related__publication'>
          {[record.year, record.container_title].filter((value) => value !== null && value !== '').join(' · ')}
        </div>
      ) : null}
      {record.abstract ? <div className='pdf-library-related__abstract'>{record.abstract}</div> : null}
      {record.identifiers.length > 0 ? (
        <div className='pdf-library-related__identifiers'>
          {record.identifiers.map(({ scheme, value }) => `${scheme.toUpperCase()}: ${value}`).join(' · ')}
        </div>
      ) : null}
      {href ? (
        <a href={href} target='_blank' rel='noopener noreferrer'>
          {t('conversation.reading.related.openSource')}
        </a>
      ) : null}
    </article>
  );
}

/** Discovery and Inbox staging do not download or attach full-text PDFs. */
export function LibraryRelatedLookup({
  item,
  initialDirection,
  close,
}: {
  item: LiteratureItem;
  initialDirection: LiteratureDirection;
  close: () => void;
}) {
  const { t, i18n } = useTranslation();
  const panelId = useId();
  const query = useLibraryRelatedLookup(item.id, item.metadata_revision, initialDirection);
  const records = query.result?.records ?? [];
  const stage = useRelatedInboxStage(item, query.direction, records);
  const retryDate = query.retryAt === undefined ? undefined : new Date(query.retryAt);
  const retryTime =
    retryDate && Number.isFinite(retryDate.getTime()) ? retryDate.toLocaleTimeString(i18n.resolvedLanguage) : undefined;
  return (
    <section className='pdf-library-related' aria-label={t('conversation.reading.related.title')}>
      <header className='pdf-library-related__header'>
        <h3>{t('conversation.reading.related.title')}</h3>
        <button type='button' onClick={close}>
          {t('common.close')}
        </button>
      </header>
      {!query.ownerChanged ? <div className='pdf-library-related__source'>{item.title}</div> : null}
      <div
        className='pdf-library-related__tabs'
        role='tablist'
        aria-label={t('conversation.reading.related.direction')}
      >
        {(['references', 'citations'] as const).map((direction) => (
          <button
            key={direction}
            type='button'
            role='tab'
            id={`${panelId}-${direction}`}
            aria-controls={panelId}
            aria-selected={query.direction === direction}
            tabIndex={query.direction === direction ? 0 : -1}
            disabled={query.ownerChanged}
            onClick={() => {
              if (query.direction !== direction) query.switchDirection(direction);
            }}
            onKeyDown={(event) => {
              if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
              event.preventDefault();
              const next =
                event.key === 'Home'
                  ? 'references'
                  : event.key === 'End'
                    ? 'citations'
                    : direction === 'references'
                      ? 'citations'
                      : 'references';
              query.switchDirection(next);
              event.currentTarget.parentElement?.querySelector<HTMLButtonElement>(`[id="${panelId}-${next}"]`)?.focus();
            }}
          >
            {t(`conversation.reading.related.${direction}`)}
          </button>
        ))}
      </div>
      <div
        id={panelId}
        role='tabpanel'
        aria-labelledby={`${panelId}-${query.direction}`}
        aria-busy={query.loading}
        className='pdf-library-related__results'
      >
        {query.ownerChanged ? (
          <p role='alert'>{t('conversation.reading.related.ownerChanged')}</p>
        ) : (
          <>
            {query.loading ? (
              <div className='pdf-library-related__notice' role='status'>
                <span>{t('conversation.reading.related.loading')}</span>
                <button type='button' onClick={query.cancel}>
                  {t('common.cancel')}
                </button>
              </div>
            ) : null}
            {query.issue || query.cancelled ? (
              <div className='pdf-library-related__notice' role={query.cancelled ? 'status' : 'alert'}>
                <span>
                  {t(`conversation.reading.related.${query.cancelled ? 'cancelled' : `issues.${query.issue}`}`)}
                </span>
                <button type='button' onClick={query.retry}>
                  {t('common.retry')}
                </button>
              </div>
            ) : null}
            {query.issue === 'rate-limited' && retryTime ? (
              <div className='pdf-library-related__hint'>
                {t('conversation.reading.related.retryTime', { time: retryTime })}
              </div>
            ) : null}
            {!query.loading &&
            !query.issue &&
            !query.cancelled &&
            query.result?.state === 'match-required' &&
            query.result.seed ? (
              <div className='pdf-library-related__match'>
                <p>{t('conversation.reading.related.confirmHint')}</p>
                <RelatedPaper record={query.result.seed} />
                <button type='button' onClick={query.confirmMatch}>
                  {t('conversation.reading.related.confirm')}
                </button>
              </div>
            ) : null}
            {records.length > 0 ? (
              <ul aria-label={t('conversation.reading.related.results')}>
                {records.map((record) => (
                  <li key={record.paper_id}>
                    <label>
                      <input
                        type='checkbox'
                        checked={stage.selected.includes(record.paper_id)}
                        disabled={stage.blocked || query.loading || Boolean(query.issue)}
                        onChange={() => stage.toggle(record.paper_id)}
                      />
                      {t('conversation.reading.related.stageSelect', { title: record.title })}
                    </label>
                    <RelatedPaper record={record} />
                    {stage.results[record.paper_id] && (
                      <p role={['failed', 'invalid'].includes(stage.results[record.paper_id]) ? 'alert' : 'status'}>
                        {t('conversation.reading.related.stageStates.' + stage.results[record.paper_id])}
                      </p>
                    )}
                  </li>
                ))}
              </ul>
            ) : !query.loading && !query.issue && !query.cancelled && query.result?.state === 'ready' ? (
              <p>
                {t(
                  `conversation.reading.related.${query.direction === 'references' ? 'emptyReferences' : 'emptyCitations'}`
                )}
              </p>
            ) : null}
            {records.length > 0 && (
              <button
                type='button'
                disabled={stage.blocked || query.loading || Boolean(query.issue) || stage.selected.length === 0}
                onClick={() => void stage.submitSelected()}
              >
                {t('conversation.reading.related.stageAdd', { count: stage.selected.length })}
              </button>
            )}
            {stage.running && <p role='status'>{t('conversation.reading.related.stageRunning')}</p>}
            {stage.pending && !stage.running && (
              <div role='alert' className='pdf-library-related__notice'>
                <span>{t('conversation.reading.related.stagePending')}</span>
                <button type='button' disabled={!stage.canRetryPending} onClick={() => void stage.retryPending()}>
                  {t('common.retry')}
                </button>
              </div>
            )}
            {!stage.pending && stage.blocked && !stage.running && (
              <p role='status'>{t('conversation.reading.related.stageBlocked')}</p>
            )}
            {query.result?.omitted ? (
              <p className='pdf-library-related__hint'>{t('conversation.reading.related.omitted')}</p>
            ) : null}
            {query.result?.next_offset !== undefined && !query.issue && !query.cancelled ? (
              <button type='button' className='pdf-library-more' disabled={query.loading} onClick={query.loadMore}>
                {t('conversation.reading.related.more')}
              </button>
            ) : null}
          </>
        )}
      </div>
      <div className='pdf-library-related__hint'>{t('conversation.reading.related.readOnlyHint')}</div>
    </section>
  );
}
