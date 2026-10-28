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
import './libraryDetailDialog.css';

/** Render the existing read owner's states without owning requests or mutations. */
export function LibraryDetailsPane({
  reads,
  busy,
  submit,
  changeStatus,
  receipt,
  close,
}: {
  reads: ReturnType<typeof useLibraryWorkbench>;
  busy: boolean;
  submit: <Command extends LiteratureCommand | LiteratureCollectionCommand>(
    command: Command
  ) => Promise<ReceiptFor<Command>>;
  changeStatus: (itemId: string, value: LiteratureReadingStatus) => void;
  receipt?: LiteratureReceipt;
  close: () => void;
}) {
  const { t } = useTranslation();
  const selected = reads.detail.status === 'ready' ? reads.detail.value : undefined;
  const stale = Boolean(reads.detail.refreshing || reads.detail.refreshError);
  const dialog = useLibraryDetailDialog(reads.account, reads.detail.status, close);
  return (
    <WorkbenchModal
      visible
      footer={null}
      closable={!selected}
      focusLock
      autoFocus
      escToExit={false}
      unmountOnExit
      onCancel={dialog.close}
      afterOpen={dialog.focus}
      maskStyle={{ backgroundColor: 'transparent' }}
      className='library-workbench__detail-dialog'
      wrapClassName='library-workbench__detail-layer'
      style={{ width: 760, maxWidth: 'calc(100vw - 32px)' }}
      modalRender={(node) =>
        React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
          ? React.cloneElement(node, {
              'aria-label': selected?.title || t('conversation.libraryWorkbench.details'),
              ...dialog.events,
            })
          : node
      }
    >
      <div ref={dialog.contentRef}>
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
              account={reads.account}
              collections={reads.collections}
              memberships={reads.memberships}
              submitCollection={submit}
              refreshCollections={reads.refresh}
              projects={reads.projects.value ?? []}
              projectStatus={reads.projects.status}
              busy={busy || stale}
              stale={stale}
              changeReadingStatus={(value) => {
                if (!stale) changeStatus(selected.id, value);
              }}
              submit={submit}
              receipt={receipt}
              close={dialog.close}
            />
          </>
        )}
      </div>
    </WorkbenchModal>
  );
}
