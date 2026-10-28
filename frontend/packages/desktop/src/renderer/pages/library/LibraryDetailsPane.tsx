import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import type { LiteratureCollectionCommand } from '@/common/adapter/literatureCollections';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import type { ReceiptFor } from '@/renderer/services/runtime/literatureUploadIntent';
import type { useLibraryWorkbench } from './useLibraryWorkbench';
import { LibraryItemDetails } from './LibraryItemDetails';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import { useLibraryDetailDialog } from './useLibraryDetailDialog';
import { useLibraryDetailExit } from './useLibraryDetailExit';
import './libraryDetailDialog.css';

/** Render the existing read owner's states without owning requests or mutations. */
export function LibraryDetailsPane({
  visible,
  reads,
  busy,
  submit,
  changeStatus,
  receipt,
  close,
  pendingOperationId,
  notice,
}: {
  visible: boolean;
  reads: ReturnType<typeof useLibraryWorkbench>;
  busy: boolean;
  submit: <Command extends LiteratureCommand | LiteratureCollectionCommand>(
    command: Command
  ) => Promise<ReceiptFor<Command>>;
  changeStatus: (itemId: string, value: LiteratureReadingStatus) => void;
  receipt?: LiteratureReceipt;
  close: () => void;
  pendingOperationId?: string;
  notice?: React.ReactNode;
}) {
  const { t } = useTranslation();
  const selected = reads.detail.status === 'ready' ? reads.detail.value : undefined;
  const stale = Boolean(reads.detail.refreshing || reads.detail.refreshError);
  const exit = useLibraryDetailExit(reads.account, pendingOperationId, close, visible);
  const dialog = useLibraryDetailDialog(reads.account, reads.detail.status, exit.close, visible);
  return (
    <>
      <WorkbenchModal
        visible={visible}
        footer={null}
        closable={!selected}
        focusLock
        autoFocus
        escToExit={false}
        // Arco disables this same focus lock while invisible; retaining it keeps the editor subtree intact.
        unmountOnExit={false}
        onCancel={dialog.close}
        afterOpen={dialog.focus}
        maskStyle={{ backgroundColor: 'transparent' }}
        mask={visible}
        wrapStyle={visible ? undefined : { visibility: 'hidden', pointerEvents: 'none' }}
        className='library-workbench__detail-dialog'
        wrapClassName='library-workbench__detail-layer'
        style={{ width: 760, maxWidth: 'calc(100vw - 32px)' }}
        modalRender={(node) =>
          React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
            ? React.cloneElement(node, {
                'aria-label': selected?.title || t('conversation.libraryWorkbench.details'),
                'aria-hidden': !visible || undefined,
                inert: !visible || undefined,
                ...dialog.events,
              })
            : node
        }
      >
        <div ref={dialog.contentRef}>
          {notice}
          {reads.detail.status === 'loading' ? (
            <p role='status' className='library-workbench__state'>
              {t('common.loading')}
            </p>
          ) : reads.detail.status === 'error' || !selected ? (
            <div role='alert' className='library-workbench__state'>
              <h2>{t('conversation.libraryWorkbench.detailFailed')}</h2>
              <button type='button' onClick={reads.refreshData}>
                {t('common.retry')}
              </button>
            </div>
          ) : (
            <>
              {stale && (
                <div role={reads.detail.refreshError ? 'alert' : 'status'} className='library-workbench__notice'>
                  <span>
                    {t(
                      reads.detail.refreshError
                        ? 'conversation.libraryReadingStatus.readFailed'
                        : 'conversation.libraryReadingStatus.refreshing'
                    )}
                  </span>
                  {reads.detail.refreshError && (
                    <button type='button' onClick={reads.refreshData}>
                      {t('common.retry')}
                    </button>
                  )}
                </div>
              )}
              <LibraryItemDetails
                key={`${reads.account}:${selected.id}`}
                item={selected}
                visible={visible}
                account={reads.account}
                collections={reads.collections}
                memberships={reads.memberships}
                submitCollection={submit}
                refreshCollections={reads.refresh}
                projects={reads.projects.value ?? []}
                projectStatus={reads.projects.status}
                projectRefreshing={reads.projects.refreshing}
                projectRefreshError={reads.projects.refreshError}
                retryProjects={reads.refresh}
                busy={busy || stale}
                stale={stale}
                changeReadingStatus={(value) => {
                  if (!stale) changeStatus(selected.id, value);
                }}
                submit={submit}
                receipt={receipt}
                close={dialog.close}
                requestEditorClose={exit.requestExit}
                registerDraft={exit.registerDraft}
              />
            </>
          )}
        </div>
      </WorkbenchModal>
      {exit.dialog}
    </>
  );
}
