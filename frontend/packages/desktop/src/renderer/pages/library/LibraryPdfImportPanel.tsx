import React, { useEffect, useId, useRef } from 'react';
import { CheckCircle2, Loader2, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { isLibraryReadFresh } from './useLibraryWorkbench';
import type { LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import type { LibraryPdfImportModel } from './useLibraryPdfImport';

const progressKey = (state: LibraryPdfImportModel['view']['uploadState']) => {
  switch (state) {
    case 'hashing':
      return 'conversation.libraryPdfImport.hashing';
    case 'waiting-lock':
      return 'conversation.libraryPdfImport.waitingLock';
    case 'initializing':
      return 'conversation.libraryPdfImport.initializing';
    case 'uploading':
      return 'conversation.libraryPdfImport.uploading';
    case 'catalogue':
      return 'conversation.libraryPdfImport.catalogue';
    default:
      return 'conversation.libraryPdfImport.working';
  }
};
function noticeKey(model: LibraryPdfImportModel) {
  if (model.failure === 'invalidFile') return 'conversation.libraryPdfImport.invalidFile';
  if (model.failure === 'insufficientStorage') return 'conversation.libraryPdfImport.insufficientStorage';
  if (model.failure === 'invalidReceipt') return 'conversation.libraryPdfImport.invalidReceipt';
  if (model.pending && model.view.uploadState === 'file-required') return 'conversation.libraryPdfImport.fileRequired';
  if (model.pending && model.view.uploadState === 'file-mismatch') return 'conversation.libraryPdfImport.fileMismatch';
  if (model.pending && model.view.uploadState === 'conflict') return 'conversation.libraryPdfImport.conflict';
  if (model.view.error === 'storage') return 'conversation.libraryPdfImport.storage';
  if (model.pending && model.view.error === 'rejected') return 'conversation.libraryPdfImport.rejected';
  if (model.pending && model.view.error === 'uncertain') return 'conversation.libraryPdfImport.uncertain';
  if (model.failure) return 'conversation.libraryPdfImport.submitFailed';
  return undefined;
}

export function LibraryPdfImportPanel({
  model,
  refreshProjects,
  viewReference,
}: {
  model: LibraryPdfImportModel;
  refreshProjects: () => void;
  viewReference: (receipt: LiteratureReceipt) => void;
}) {
  const { t } = useTranslation();
  const headingId = useId();
  const panelRef = useRef<HTMLElement>(null);
  const titleRef = useRef<HTMLInputElement>(null);
  const focusPending = useRef<HTMLButtonElement>(null);
  const focusedVersion = useRef(0);
  useEffect(() => {
    if (!model.open || model.focusVersion <= focusedVersion.current) return;
    focusedVersion.current = model.focusVersion;
    (model.busy
      ? panelRef.current
      : model.pending
        ? (focusPending.current ?? panelRef.current)
        : (titleRef.current ?? panelRef.current)
    )?.focus();
  }, [model.open, model.focusVersion, model.pending, model.busy]);
  if (!model.open) return null;
  const pending = model.pending;
  const selectedProject = model.projects.value?.find(
    (project) => project.projectId === (pending?.init.project_id ?? model.draft.projectId)
  );
  const requiresSelection =
    pending &&
    pending.phase !== 'catalogue' &&
    !model.selectedOriginal &&
    (model.view.uploadState === 'file-required' || model.view.uploadState === 'file-mismatch');
  const notice = !model.busy && !model.success ? noticeKey(model) : undefined;
  return (
    <section
      ref={panelRef}
      tabIndex={-1}
      role='dialog'
      aria-modal='false'
      aria-labelledby={headingId}
      aria-busy={model.busy}
      className='library-pdf-import-panel'
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.preventDefault();
          event.stopPropagation();
          model.close();
        }
      }}
    >
      <header className='library-pdf-import-panel__heading'>
        <h2 id={headingId}>{t('conversation.libraryPdfImport.panelTitle')}</h2>
        <button
          type='button'
          className='library-pdf-import-panel__icon'
          aria-label={t('conversation.libraryPdfImport.closeLabel')}
          disabled={model.busy}
          onClick={model.close}
        >
          <X size={15} aria-hidden='true' />
        </button>
      </header>
      {model.success ? (
        <div className='library-pdf-import-panel__success'>
          <p role='status'>
            <CheckCircle2 size={15} aria-hidden='true' />
            <strong>{t('conversation.libraryPdfImport.success')}</strong>
          </p>
          <p>{t('conversation.libraryPdfImport.successDetail', { title: model.success.item.title })}</p>
          <div className='library-pdf-import-panel__actions'>
            <button type='button' disabled={model.busy} onClick={() => viewReference(model.success!)}>
              {t('conversation.libraryPdfImport.viewReference')}
            </button>
            <button type='button' disabled={model.busy} onClick={model.close}>
              {t('common.close')}
            </button>
          </div>
        </div>
      ) : (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (!pending) model.submit();
          }}
        >
          <p className='library-pdf-import-panel__hint'>
            {t(pending ? 'conversation.libraryPdfImport.frozenHint' : 'conversation.libraryPdfImport.description')}
          </p>
          {pending ? (
            <dl className='library-pdf-import-panel__frozen'>
              <div>
                <dt>{t('conversation.libraryPdfImport.file')}</dt>
                <dd>{pending.init.filename}</dd>
              </div>
              <div>
                <dt>{t('conversation.libraryPdfImport.project')}</dt>
                <dd>{selectedProject?.name ?? pending.init.project_id}</dd>
              </div>
              <div>
                <dt>{t('conversation.libraryPdfImport.title')}</dt>
                <dd>{pending.title}</dd>
              </div>
            </dl>
          ) : (
            <>
              <div className='library-pdf-import-panel__file'>
                <span>
                  {t('conversation.libraryPdfImport.file')}:{' '}
                  <strong>{model.draft.file?.name ?? t('conversation.libraryPdfImport.noFile')}</strong>
                </span>
                <button
                  type='button'
                  disabled={model.busy || model.otherPending}
                  onClick={() => model.inputRef.current?.click()}
                >
                  {t('conversation.libraryPdfImport.choose')}
                </button>
              </div>
              <div className='library-pdf-import-panel__fields'>
                <label>
                  {t('conversation.libraryPdfImport.project')}
                  <select
                    value={model.draft.projectId}
                    disabled={model.busy || !isLibraryReadFresh(model.projects) || model.otherPending}
                    onChange={(event) => model.setProject(event.target.value)}
                    required
                  >
                    <option value=''>{t('conversation.libraryPdfImport.projectPlaceholder')}</option>
                    {model.draft.projectId &&
                      !model.projects.value?.some((project) => project.projectId === model.draft.projectId) && (
                        <option value={model.draft.projectId} disabled>
                          {t('conversation.libraryWorkbench.projectUnavailable')}
                        </option>
                      )}
                    {model.projects.value?.map((project) => (
                      <option key={project.projectId} value={project.projectId}>
                        {project.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  {t('conversation.libraryPdfImport.title')}
                  <input
                    ref={titleRef}
                    value={model.draft.title}
                    maxLength={4096}
                    disabled={model.busy || model.otherPending}
                    onChange={(event) => model.setTitle(event.target.value)}
                    required
                  />
                </label>
              </div>
              {!isLibraryReadFresh(model.projects) ? (
                <div
                  role={model.projects.status === 'error' || model.projects.refreshError ? 'alert' : 'status'}
                  className='library-pdf-import-panel__hint'
                >
                  <span>
                    {t(
                      model.projects.status === 'error' || model.projects.refreshError
                        ? 'conversation.libraryPdfImport.projectError'
                        : 'conversation.libraryPdfImport.projectLoading'
                    )}
                  </span>
                  <button type='button' onClick={refreshProjects}>
                    {t('conversation.libraryPdfImport.retryProjects')}
                  </button>
                </div>
              ) : !model.projects.value?.length ? (
                <div role='status' className='library-pdf-import-panel__hint'>
                  <span>{t('conversation.libraryPdfImport.projectEmpty')}</span>
                  <button type='button' onClick={refreshProjects}>
                    {t('conversation.libraryPdfImport.retryProjects')}
                  </button>
                </div>
              ) : model.draft.projectId && !selectedProject ? (
                <p role='alert'>{t('conversation.libraryPdfImport.projectChanged')}</p>
              ) : null}
            </>
          )}
          {pending && pending.phase !== 'catalogue' && (
            <div className='library-pdf-import-panel__file'>
              <button
                ref={focusPending}
                type='button'
                disabled={model.busy || model.discarding}
                onClick={() => model.inputRef.current?.click()}
              >
                {t('conversation.libraryPdfImport.chooseOriginal')}
              </button>
              {model.selectedOriginal && (
                <span>
                  {t('conversation.libraryPdfImport.selectedOriginal', { name: model.selectedOriginal.name })}
                </span>
              )}
            </div>
          )}
          {model.otherPending && <p role='alert'>{t('conversation.libraryPdfImport.otherPending')}</p>}
          {notice && <p role='alert'>{t(notice)}</p>}
          {model.busy && (
            <p role='status' className='library-pdf-import-panel__progress'>
              <Loader2 size={14} className='library-workbench__spin' aria-hidden='true' />
              {t(progressKey(model.view.uploadState))}
            </p>
          )}
          {model.busy && (
            <p className='library-pdf-import-panel__hint'>{t('conversation.libraryPdfImport.cancelBeforeClose')}</p>
          )}
          {pending && model.view.canDiscardRejectedUpload && (
            <div className='library-pdf-import-panel__discard'>
              <p>{t('conversation.libraryPdfImport.discardHint')}</p>
              <button type='button' disabled={model.busy || model.discarding} onClick={model.discard}>
                {t(
                  model.discarding
                    ? 'conversation.libraryPdfImport.discarding'
                    : 'conversation.libraryPdfImport.discard'
                )}
              </button>
            </div>
          )}
          <footer className='library-pdf-import-panel__actions'>
            <button type='button' disabled={model.busy} onClick={model.close}>
              {t('common.close')}
            </button>
            {model.busy ? (
              <button type='button' onClick={model.cancel}>
                {t('common.cancel')}
              </button>
            ) : pending ? (
              <button type='button' disabled={Boolean(requiresSelection) || model.discarding} onClick={model.retry}>
                {t('conversation.libraryPdfImport.retry')}
              </button>
            ) : (
              <button type='submit' disabled={!model.canImport}>
                {t('conversation.libraryPdfImport.submit')}
              </button>
            )}
          </footer>
        </form>
      )}
    </section>
  );
}
