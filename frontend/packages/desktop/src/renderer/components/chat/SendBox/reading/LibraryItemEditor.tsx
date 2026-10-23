import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureDirection } from '@/common/adapter/literatureRelated';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { LibraryRelatedLookup } from './LibraryRelatedLookup';

/** Metadata/membership editing never owns the accepted PDF or its content. */
export function LibraryItemEditor({
  item,
  projectId,
  busy,
  submit,
  close,
}: {
  item: LiteratureItem;
  projectId: string;
  busy: boolean;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  close: () => void;
}) {
  const { t } = useTranslation();
  const [title, setTitle] = useState(item.title);
  const [authors, setAuthors] = useState(typeof item.metadata.authors === 'string' ? item.metadata.authors : '');
  const [year, setYear] = useState(
    typeof item.metadata.year === 'string' || typeof item.metadata.year === 'number' ? String(item.metadata.year) : ''
  );
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [relatedDirection, setRelatedDirection] = useState<LiteratureDirection | null>(null);
  const run = (command: LiteratureCommand): void => {
    void submit(command).then(close, (): undefined => undefined);
  };
  const included = item.project_ids.includes(projectId);
  if (relatedDirection)
    return (
      <LibraryRelatedLookup item={item} initialDirection={relatedDirection} close={() => setRelatedDirection(null)} />
    );
  return (
    <section className='pdf-library-editor' aria-label={t('conversation.reading.libraryDetails')}>
      <div className='pdf-library-editor__buttons' aria-label={t('conversation.reading.related.direction')}>
        <button type='button' disabled={busy} onClick={() => setRelatedDirection('references')}>
          {t('conversation.reading.related.references')}
        </button>
        <button type='button' disabled={busy} onClick={() => setRelatedDirection('citations')}>
          {t('conversation.reading.related.citations')}
        </button>
      </div>
      <label>
        {t('conversation.reading.libraryTitle')}
        <input value={title} maxLength={4096} disabled={busy} onChange={(event) => setTitle(event.target.value)} />
      </label>
      <label>
        {t('conversation.reading.libraryAuthors')}
        <input value={authors} maxLength={2048} disabled={busy} onChange={(event) => setAuthors(event.target.value)} />
      </label>
      <label>
        {t('conversation.reading.libraryYear')}
        <input
          value={year}
          maxLength={4}
          inputMode='numeric'
          disabled={busy}
          onChange={(event) => setYear(event.target.value)}
        />
      </label>
      <div className='pdf-library-editor__buttons'>
        <button
          type='button'
          disabled={busy || !title.trim() || Boolean(year && !/^\d{4}$/.test(year))}
          onClick={() =>
            run({
              kind: 'edit',
              operation_id: crypto.randomUUID(),
              item_id: item.id,
              expected_revision: item.metadata_revision,
              title: title.trim(),
              metadata: { ...item.metadata, authors: authors.trim(), year: year.trim() },
            })
          }
        >
          {t('common.save')}
        </button>
        <button
          type='button'
          disabled={busy}
          onClick={() =>
            run({
              kind: 'membership',
              operation_id: crypto.randomUUID(),
              item_id: item.id,
              project_id: projectId,
              included: !included,
            })
          }
        >
          {t(included ? 'conversation.reading.libraryUnlinkProject' : 'conversation.reading.libraryLinkProject')}
        </button>
        <button type='button' disabled={busy} onClick={close}>
          {t('common.close')}
        </button>
      </div>
      <div className='pdf-library-editor__buttons'>
        {confirmRemove ? (
          <>
            <span>{t('conversation.reading.libraryRemoveNote')}</span>
            <button
              type='button'
              disabled={busy}
              onClick={() =>
                run({
                  kind: 'edit',
                  operation_id: crypto.randomUUID(),
                  item_id: item.id,
                  expected_revision: item.metadata_revision,
                  deleted: true,
                })
              }
            >
              {t('conversation.reading.libraryConfirmRemove')}
            </button>
            <button type='button' disabled={busy} onClick={() => setConfirmRemove(false)}>
              {t('common.cancel')}
            </button>
          </>
        ) : (
          <button type='button' disabled={busy} onClick={() => setConfirmRemove(true)}>
            {t('conversation.reading.libraryRemove')}
          </button>
        )}
      </div>
    </section>
  );
}
