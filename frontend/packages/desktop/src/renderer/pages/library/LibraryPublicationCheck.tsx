import React, { useId } from 'react';
import { ExternalLink, FileText, LoaderCircle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { useLibraryPublicationCheck } from './useLibraryPublicationCheck';
import './libraryPublicationCheck.css';

const PREFIX = 'conversation.libraryPublicationCheck.';
const noticeKey = (type: string) => {
  switch (type.toLowerCase().replace(/[\s-]+/g, '_')) {
    case 'retraction':
      return 'retraction';
    case 'partial_retraction':
      return 'partialRetraction';
    case 'withdrawal':
      return 'withdrawal';
    case 'correction':
    case 'erratum':
    case 'corrigendum':
      return 'correction';
    case 'expression_of_concern':
      return 'concern';
    case 'reinstatement':
      return 'reinstatement';
    default:
      return 'update';
  }
};

export function LibraryPublicationCheck({ account, item }: { account: string; item: LiteratureItem }) {
  const { t, i18n } = useTranslation();
  const heading = useId();
  const help = useId();
  const model = useLibraryPublicationCheck(account, item);
  if (!model.current) return null;
  const issue = model.issue === 'doi-required' ? undefined : model.issue;
  const failure =
    !model.available || model.issue === 'doi-required'
      ? undefined
      : issue
        ? t(PREFIX + 'issues.' + issue)
        : model.storageFailed
          ? t(PREFIX + 'storage')
          : model.rejected
            ? t(PREFIX + 'rejected')
            : model.failure
              ? t(PREFIX + (model.failure === 'read' ? 'readFailed' : 'writeFailed'))
              : undefined;
  const retryRead = !model.pending && Boolean(failure) && model.retryMode === 'get' && !model.rejected;
  const restoreStorage = model.available && model.storageFailed;
  const label = model.busy
    ? 'checking'
    : restoreStorage
      ? 'retry'
      : model.pending
        ? 'resume'
        : failure
          ? 'retry'
          : model.snapshot
            ? 'checkAgain'
            : 'checkNow';
  return (
    <section className='library-publication-check' aria-labelledby={heading}>
      <div className='library-publication-check__heading'>
        <h3 id={heading}>{t(PREFIX + 'title')}</h3>
        <button
          type='button'
          className='library-workbench__button'
          disabled={
            restoreStorage
              ? model.busy
              : model.pending
                ? !model.canResume
                : retryRead
                  ? model.loading || model.busy
                  : !model.canCheck
          }
          aria-describedby={help}
          onClick={restoreStorage ? model.restore : model.pending ? model.resume : retryRead ? model.read : model.check}
        >
          {model.busy && <LoaderCircle size={14} aria-hidden='true' className='library-publication-check__spinner' />}
          {t(PREFIX + label)}
        </button>
      </div>
      {model.deleted ? (
        <p>{t(PREFIX + 'unavailable')}</p>
      ) : !model.hasDoi || model.issue === 'doi-required' ? (
        <p>{t(PREFIX + 'doiRequired')}</p>
      ) : null}
      {model.loading && (
        <p role='status' className={model.snapshot ? 'sr-only' : undefined}>
          {t(PREFIX + 'loading')}
        </p>
      )}
      {model.busy && (
        <p role='status' className='sr-only'>
          {t(PREFIX + 'checking')}
        </p>
      )}
      {failure && (
        <p role='alert' className='library-publication-check__issue'>
          {failure}
        </p>
      )}
      {model.available && model.otherPending && (
        <p className='library-publication-check__muted'>{t(PREFIX + 'otherPending')}</p>
      )}
      {model.available && model.pending && !model.busy && <p role='status'>{t(PREFIX + 'pending')}</p>}
      {model.snapshot ? (
        model.snapshot.notices.length ? (
          <ul className='library-publication-check__notices'>
            {model.snapshot.notices.map((notice, index) => (
              <li key={JSON.stringify([notice.doi, notice.type, notice.date, notice.source, index])}>
                <strong>{t(PREFIX + 'notices.' + noticeKey(notice.type))}</strong>
                <span className='library-publication-check__muted'>{notice.type}</span>
                <span className='library-publication-check__muted'>
                  {t(PREFIX + 'sources.' + notice.source)}
                  {notice.date && (
                    <>
                      {' '}
                      · <time dateTime={notice.date}>{notice.date}</time>
                    </>
                  )}
                </span>
                <a href={`https://doi.org/${encodeURIComponent(notice.doi)}`} target='_blank' rel='noopener noreferrer'>
                  {notice.doi}
                  <ExternalLink size={12} aria-hidden='true' />
                </a>
              </li>
            ))}
          </ul>
        ) : (
          <p role='status' className='library-publication-check__empty'>
            <FileText size={16} aria-hidden='true' />
            {t(PREFIX + 'noNotices')}
          </p>
        )
      ) : model.loaded && !failure && !model.issue && !model.loading && !model.busy ? (
        <p className='library-publication-check__empty'>
          <FileText size={16} aria-hidden='true' />
          {t(PREFIX + 'notChecked')}
        </p>
      ) : null}
      <footer className='library-publication-check__footer'>
        <span>Crossref</span>
        {model.snapshot && (
          <time dateTime={model.snapshot.checked_at} title={model.snapshot.checked_at}>
            {t(PREFIX + 'lastChecked', {
              time: new Date(model.snapshot.checked_at).toLocaleString(i18n.language, {
                dateStyle: 'medium',
                timeStyle: 'short',
              }),
            })}
          </time>
        )}
        <p id={help}>{t(PREFIX + 'disclosure')}</p>
      </footer>
    </section>
  );
}
