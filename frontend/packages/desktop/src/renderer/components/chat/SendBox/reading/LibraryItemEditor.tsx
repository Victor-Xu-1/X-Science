import type { LiteratureCommand, LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { LibraryBibliographicFields, LibraryReferenceTypeField } from './LibraryBibliographicFields';
import {
  buildLibraryBibliographicEdit,
  projectLibraryBasic,
  projectLibraryScalars,
  type LibraryScalarKey,
} from './libraryBibliographicDraft';

type SubmittedEdit = { operationId: string; before: LiteratureReceipt | undefined };

/** Metadata/membership editing never owns the accepted PDF or its content. */
export function LibraryItemEditor({
  item,
  projectId,
  busy,
  readOnly = false,
  receipt,
  submit,
  close,
}: {
  item: LiteratureItem;
  projectId?: string;
  busy: boolean;
  readOnly?: boolean;
  receipt?: LiteratureReceipt;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  close: () => void;
}) {
  const { t } = useTranslation();
  const { authors: initialAuthors, year: initialYear, doi: initialDoi } = projectLibraryBasic(item);
  const [title, setTitle] = useState(item.title);
  const [authors, setAuthors] = useState(initialAuthors);
  const [year, setYear] = useState(initialYear);
  const [doi, setDoi] = useState(initialDoi);
  const yearChanged = year !== initialYear;
  const [scalars, setScalars] = useState(() => projectLibraryScalars(item.metadata));
  const [touched, setTouched] = useState<ReadonlySet<LibraryScalarKey>>(() => new Set());
  const changeScalar = (key: LibraryScalarKey, value: string) => {
    setScalars((previous) => ({ ...previous, [key]: value }));
    setTouched((previous) => new Set(previous).add(key));
  };
  const scalarProps = { metadata: item.metadata, draft: scalars, disabled: busy || readOnly, change: changeScalar };
  const [confirmRemove, setConfirmRemove] = useState(false);
  const submitted = useRef<SubmittedEdit | undefined>(undefined);
  const finish = useCallback(
    (attempt: SubmittedEdit) => {
      if (submitted.current !== attempt) return;
      submitted.current = undefined;
      close();
    },
    [close]
  );
  useEffect(
    () => () => {
      submitted.current = undefined;
    },
    [item.id]
  );
  useEffect(() => {
    const attempt = submitted.current;
    if (
      attempt &&
      receipt &&
      receipt !== attempt.before &&
      receipt.operation_id === attempt.operationId &&
      receipt.item.id === item.id
    )
      finish(attempt);
  }, [receipt, item.id, finish]);
  const run = (command: LiteratureCommand): void => {
    if (busy || readOnly) return;
    const attempt = { operationId: command.operation_id, before: receipt };
    submitted.current = attempt;
    void submit(command).then(
      () => finish(attempt),
      (): undefined => undefined
    );
  };
  const included = Boolean(projectId && item.project_ids.includes(projectId));
  return (
    <section className='pdf-library-editor' aria-label={t('conversation.reading.libraryDetails')}>
      <LibraryReferenceTypeField {...scalarProps} />
      <label>
        {t('conversation.reading.libraryTitle')}
        <input
          value={title}
          maxLength={4096}
          disabled={busy || readOnly}
          onChange={(event) => setTitle(event.target.value)}
        />
      </label>
      <label>
        {t('conversation.reading.libraryAuthors')}
        <input
          value={authors}
          maxLength={2048}
          disabled={busy || readOnly}
          onChange={(event) => setAuthors(event.target.value)}
        />
      </label>
      <label>
        {t('conversation.reading.libraryYear')}
        <input
          value={year}
          maxLength={4}
          inputMode='numeric'
          disabled={busy || readOnly}
          onChange={(event) => setYear(event.target.value)}
        />
      </label>
      <label>
        {t('conversation.libraryTable.columns.doi')}
        <input
          value={doi}
          maxLength={2048}
          autoCapitalize='none'
          spellCheck={false}
          disabled={busy || readOnly}
          onChange={(event) => setDoi(event.target.value)}
        />
      </label>
      <LibraryBibliographicFields {...scalarProps} />
      <div className='pdf-library-editor__buttons'>
        <button
          type='button'
          disabled={busy || readOnly || !title.trim() || Boolean(yearChanged && year && !/^\d{4}$/.test(year))}
          onClick={() => run(buildLibraryBibliographicEdit(item, { title, authors, year, doi }, scalars, touched))}
        >
          {t('common.save')}
        </button>
        {projectId && (
          <button
            type='button'
            disabled={busy || readOnly}
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
        )}
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
              disabled={busy || readOnly}
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
            <button type='button' disabled={busy || readOnly} onClick={() => setConfirmRemove(false)}>
              {t('common.cancel')}
            </button>
          </>
        ) : (
          <button type='button' disabled={busy || readOnly} onClick={() => setConfirmRemove(true)}>
            {t('conversation.reading.libraryRemove')}
          </button>
        )}
      </div>
    </section>
  );
}
