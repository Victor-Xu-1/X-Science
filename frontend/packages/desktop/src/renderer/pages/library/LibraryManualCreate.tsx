import React, { useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import {
  LibraryBibliographicFields,
  LibraryReferenceTypeField,
} from '@/renderer/components/chat/SendBox/reading/LibraryBibliographicFields';
import { useLibraryDetailDialog } from './useLibraryDetailDialog';
import { LibraryDirectoryState } from './LibraryDirectoryState';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { useLibraryManualCreate, type LibraryManualCreateProps } from './useLibraryManualCreate';
import '@/renderer/components/chat/SendBox/reading/readingContext.css';
import './libraryManualCreate.css';

export function LibraryManualCreate(props: LibraryManualCreateProps) {
  const { t } = useTranslation();
  const { account, view, close, openTrash } = props;
  const [review, setReview] = useState(Boolean(props.initialDraft));
  const pmid =
    props.initialDraft?.schema === 'x-science.literature-pmid-draft.v1' ? props.initialDraft.pmid : undefined;
  const title = t(
    pmid
      ? 'conversation.libraryPmid.review'
      : props.initialDraft
        ? 'conversation.libraryDoi.review'
        : 'conversation.libraryManual.add'
  );
  const editButton = useRef<HTMLButtonElement | null>(null);
  const { basic, setBasic, scalarProps, failure, discard, setDiscard, locked, pending, requestClose, run } =
    useLibraryManualCreate(props);
  const dialog = useLibraryDetailDialog(account, view.mutating ? 'saving' : 'ready', requestClose);
  useLayoutEffect(() => {
    if (!props.initialDraft || account !== getRendererAccountScopeToken()) return;
    if (review) editButton.current?.focus({ preventScroll: true });
    else dialog.focus();
  }, [account, dialog.focus, props.initialDraft, review]);
  return (
    <WorkbenchModal
      visible
      closable={!view.mutating}
      footer={null}
      focusLock
      autoFocus
      escToExit={false}
      onCancel={requestClose}
      maskStyle={{ backgroundColor: 'transparent' }}
      style={{ width: 640, maxWidth: 'calc(100vw - 32px)' }}
      title={title}
      className='library-manual-create'
      modalRender={(node) =>
        React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
          ? React.cloneElement(node, {
              'aria-label': title,
              'aria-busy': view.mutating,
              ...dialog.events,
            })
          : node
      }
    >
      <div ref={dialog.contentRef} className='library-manual-create__body'>
        <p className='library-manual-create__description'>
          {t(
            pmid
              ? 'conversation.libraryPmid.reviewDescription'
              : props.initialDraft
                ? 'conversation.libraryDoi.reviewDescription'
                : 'conversation.libraryManual.description'
          )}
        </p>
        {discard ? (
          <div role='alert' className='library-manual-create__discard'>
            <p>{t('conversation.libraryManual.discard')}</p>
            <button type='button' onClick={() => setDiscard(false)}>
              {t('common.cancel')}
            </button>
            <button type='button' onClick={close}>
              {t('conversation.libraryManual.discardAction')}
            </button>
          </div>
        ) : (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (props.initialDraft && !review) setReview(true);
              else void run();
            }}
          >
            <div className='library-manual-create__fields'>
              {review ? (
                <>
                  <h3 className='text-15px font-semibold break-words'>{basic.title}</h3>
                  <dl className='grid gap-12px text-13px'>
                    {pmid && (
                      <div>
                        <dt className='text-t-secondary'>{t('conversation.libraryPmid.identifier')}</dt>
                        <dd className='break-words'>{pmid}</dd>
                      </div>
                    )}
                    {(
                      [
                        ['authors', Array.isArray(basic.authors) ? basic.authors.join('; ') : basic.authors],
                        ['year', basic.year],
                        ['doi', basic.doi],
                        ['publication', scalarProps.draft.container_title],
                      ] as const
                    ).map(([key, value]) => (
                      <div key={key}>
                        <dt className='text-t-secondary'>{t(`conversation.libraryDoi.fields.${key}`)}</dt>
                        <dd className='break-words'>{value || '—'}</dd>
                      </div>
                    ))}
                  </dl>
                </>
              ) : (
                <>
                  <LibraryReferenceTypeField {...scalarProps} />
                  {(['title', 'authors', 'year', 'doi'] as const).map((key) =>
                    key === 'authors' && Array.isArray(basic.authors) ? (
                      <div key={key} className='grid gap-8px'>
                        <span className='text-13px text-t-secondary'>{t('conversation.reading.libraryAuthors')}</span>
                        {basic.authors.map((name, index) => (
                          <div key={index} className='flex items-end gap-8px'>
                            <label className='library-bibliographic__field min-w-0 flex-1'>
                              <span className='sr-only'>
                                {t('conversation.reading.libraryAuthors')} {index + 1}
                              </span>
                              <input
                                value={name}
                                maxLength={1000}
                                disabled={locked}
                                onChange={(event) =>
                                  setBasic((previous) => ({
                                    ...previous,
                                    authors: Array.isArray(previous.authors)
                                      ? previous.authors.map((value, ordinal) =>
                                          ordinal === index ? event.target.value : value
                                        )
                                      : previous.authors,
                                  }))
                                }
                              />
                            </label>
                            <button
                              type='button'
                              disabled={locked}
                              className='h-28px rounded border border-solid border-[var(--reading-border)] px-8px font-inherit text-12px'
                              aria-label={t('conversation.libraryDoi.removeAuthor', { index: index + 1 })}
                              onClick={() =>
                                setBasic((previous) => ({
                                  ...previous,
                                  authors: Array.isArray(previous.authors)
                                    ? previous.authors.filter((_, ordinal) => ordinal !== index)
                                    : previous.authors,
                                }))
                              }
                            >
                              {t('common.remove')}
                            </button>
                          </div>
                        ))}
                        <button
                          type='button'
                          disabled={locked}
                          className='h-28px justify-self-start rounded border border-solid border-[var(--reading-border)] px-8px font-inherit text-12px'
                          onClick={() =>
                            setBasic((previous) => ({
                              ...previous,
                              authors: Array.isArray(previous.authors) ? [...previous.authors, ''] : previous.authors,
                            }))
                          }
                        >
                          {t('conversation.libraryDoi.addAuthor')}
                        </button>
                      </div>
                    ) : (
                      <label key={key} className='library-bibliographic__field'>
                        {key === 'doi'
                          ? t('conversation.libraryTable.columns.doi')
                          : t(
                              `conversation.reading.${key === 'title' ? 'libraryTitle' : key === 'authors' ? 'libraryAuthors' : 'libraryYear'}`
                            )}
                        <input
                          required={key === 'title'}
                          value={basic[key]}
                          maxLength={key === 'year' ? 4 : key === 'title' ? 4096 : 2048}
                          disabled={locked}
                          inputMode={key === 'year' ? 'numeric' : undefined}
                          onChange={(event) => setBasic((previous) => ({ ...previous, [key]: event.target.value }))}
                        />
                      </label>
                    )
                  )}
                  <LibraryBibliographicFields {...scalarProps} />
                </>
              )}
              {props.collectionRead && (
                <LibraryDirectoryState
                  view={props.collectionRead}
                  errorText={t('conversation.libraryWorkbench.collectionsFailed')}
                  retry={props.refresh}
                />
              )}
              {(failure || (pending && view.error)) && (
                <div role='alert'>
                  <p>
                    {t(
                      pending
                        ? 'conversation.reading.libraryUncertain'
                        : failure === 'trash'
                          ? 'conversation.libraryManual.trashConflict'
                          : 'conversation.reading.libraryRejected'
                    )}
                  </p>
                  {failure === 'trash' && (
                    <button type='button' onClick={openTrash}>
                      {t('conversation.libraryManual.openTrash')}
                    </button>
                  )}
                </div>
              )}
            </div>
            <footer className='library-manual-create__actions'>
              <button type='button' disabled={view.mutating} onClick={requestClose}>
                {t('common.cancel')}
              </button>
              {pending ? (
                <button type='button' disabled={view.mutating} onClick={() => void run(true)}>
                  {t(view.mutating ? 'common.loading' : 'common.retry')}
                </button>
              ) : (
                <>
                  {review && (
                    <button ref={editButton} type='button' disabled={locked} onClick={() => setReview(false)}>
                      {t('conversation.libraryDoi.edit')}
                    </button>
                  )}
                  <button
                    type='submit'
                    disabled={locked || !basic.title.trim() || Boolean(basic.year && !/^\d{4}$/.test(basic.year))}
                  >
                    {t(
                      view.mutating
                        ? 'common.loading'
                        : props.initialDraft
                          ? review
                            ? 'conversation.libraryDoi.save'
                            : 'conversation.libraryDoi.back'
                          : 'conversation.libraryManual.create'
                    )}
                  </button>
                </>
              )}
            </footer>
          </form>
        )}
      </div>
    </WorkbenchModal>
  );
}
