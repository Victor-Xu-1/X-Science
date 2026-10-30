import React from 'react';
import { useTranslation } from 'react-i18next';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import {
  LibraryBibliographicFields,
  LibraryReferenceTypeField,
} from '@/renderer/components/chat/SendBox/reading/LibraryBibliographicFields';
import { useLibraryDetailDialog } from './useLibraryDetailDialog';
import { useLibraryManualCreate, type LibraryManualCreateProps } from './useLibraryManualCreate';
import '@/renderer/components/chat/SendBox/reading/readingContext.css';
import './libraryManualCreate.css';

export function LibraryManualCreate(props: LibraryManualCreateProps) {
  const { t } = useTranslation();
  const { account, view, close, openTrash } = props;
  const { basic, setBasic, scalarProps, failure, discard, setDiscard, locked, pending, requestClose, run } =
    useLibraryManualCreate(props);
  const dialog = useLibraryDetailDialog(account, view.mutating ? 'saving' : 'ready', requestClose);
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
      title={t('conversation.libraryManual.add')}
      className='library-manual-create'
      modalRender={(node) =>
        React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
          ? React.cloneElement(node, {
              'aria-label': t('conversation.libraryManual.add'),
              'aria-busy': view.mutating,
              ...dialog.events,
            })
          : node
      }
    >
      <div ref={dialog.contentRef} className='library-manual-create__body'>
        <p className='library-manual-create__description'>{t('conversation.libraryManual.description')}</p>
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
              void run();
            }}
          >
            <div className='library-manual-create__fields'>
              <LibraryReferenceTypeField {...scalarProps} />
              {(['title', 'authors', 'year', 'doi'] as const).map((key) => (
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
              ))}
              <LibraryBibliographicFields {...scalarProps} />
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
                <button
                  type='submit'
                  disabled={locked || !basic.title.trim() || Boolean(basic.year && !/^\d{4}$/.test(basic.year))}
                >
                  {t(view.mutating ? 'common.loading' : 'conversation.libraryManual.create')}
                </button>
              )}
            </footer>
          </form>
        )}
      </div>
    </WorkbenchModal>
  );
}
