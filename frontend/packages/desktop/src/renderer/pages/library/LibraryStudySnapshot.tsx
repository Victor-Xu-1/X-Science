import React from 'react';
import { LoaderCircle, RefreshCw, Sparkles } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { STUDY_FIELDS, type LiteratureStudySnapshot } from '@/common/adapter/literatureStudySnapshot';
import { useLibraryStudySnapshot } from './useLibraryStudySnapshot';
import './libraryStudySnapshot.css';

const PREFIX = 'conversation.libraryStudy.';
export function LibraryStudySnapshot({
  account,
  item,
  visible = true,
  disabled = false,
}: {
  account: string;
  item: LiteratureItem;
  visible?: boolean;
  disabled?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const model = useLibraryStudySnapshot(account, item, i18n.resolvedLanguage ?? i18n.language, visible);
  const snapshot = model.view?.snapshot;
  const issue =
    model.issue === 'LITERATURE_STUDY_SOURCE_CHANGED'
      ? 'sourceChanged'
      : model.issue === 'LITERATURE_STUDY_SOURCE_UNAVAILABLE'
        ? 'sourceUnavailable'
        : model.issue === 'LITERATURE_STUDY_INPUT_TOO_LARGE'
          ? 'inputTooLarge'
          : model.issue === 'LITERATURE_STUDY_BUSY'
            ? 'busy'
            : model.issue
              ? snapshot
                ? 'regenerateFailed'
                : 'failed'
              : undefined;
  const busy = model.pending || model.loading;
  return (
    <section className='library-study' aria-label={t(PREFIX + 'title')} aria-busy={busy}>
      <div className='library-study__heading'>
        <h3>
          <Sparkles size={14} aria-hidden='true' />
          {t(PREFIX + 'title')}
        </h3>
        {snapshot && <span className='library-study__coverage'>{t(PREFIX + 'coverage.' + snapshot.coverage)}</span>}
        <button
          type='button'
          className='library-workbench__button'
          disabled={disabled || busy || model.invalid || item.deleted || (!model.retryRead && !model.view?.available)}
          onClick={model.retryRead ? model.read : model.generate}
        >
          {busy ? (
            <LoaderCircle size={13} className='library-study__spinner' aria-hidden='true' />
          ) : snapshot ? (
            <RefreshCw size={13} aria-hidden='true' />
          ) : null}
          {t(
            PREFIX +
              (model.pending
                ? 'generating'
                : model.loading
                  ? 'loading'
                  : model.issue
                    ? 'retry'
                    : snapshot
                      ? 'regenerate'
                      : 'generate')
          )}
        </button>
      </div>
      {model.pending && <p role='status'>{t(PREFIX + 'progress')}</p>}
      {issue && (
        <p role='alert' className='library-study__issue'>
          {t(PREFIX + issue)}
        </p>
      )}
      {!snapshot && !busy && !issue && (
        <p>
          {t(PREFIX + (item.deleted ? 'sourceChanged' : model.view?.available ? 'notGenerated' : 'sourceUnavailable'))}
        </p>
      )}
      {snapshot && <LibraryStudyContent snapshot={snapshot} />}
    </section>
  );
}

/** Shared display only; single-paper and batch readers keep their own exact source fences. */
export function LibraryStudyContent({ snapshot }: { snapshot: LiteratureStudySnapshot }) {
  const { t } = useTranslation();
  return (
    <>
      <dl>
        {STUDY_FIELDS.map((field) => (
          <div key={field}>
            <dt>{t(PREFIX + 'fields.' + field)}</dt>
            <dd>{snapshot.sections[field] ?? t(PREFIX + 'notReported')}</dd>
          </div>
        ))}
      </dl>
      <p className='library-study__muted'>
        {t(PREFIX + 'model')}: {snapshot.model}
      </p>
      <p className='library-study__muted'>{t(PREFIX + 'warning')}</p>
    </>
  );
}
