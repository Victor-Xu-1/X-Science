import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand } from '@/common/adapter/literatureLibrary';
import type { LiteratureCollectionCommand } from '@/common/adapter/literatureCollections';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import type { ReceiptFor } from '@/renderer/services/runtime/literatureUploadIntent';
import type { useLibraryWorkbench } from './useLibraryWorkbench';
import { LibraryItemDetails } from './LibraryItemDetails';

/** Render the existing read owner's states without owning requests or mutations. */
export function LibraryDetailsPane({
  reads,
  busy,
  submit,
  changeStatus,
  close,
}: {
  reads: ReturnType<typeof useLibraryWorkbench>;
  busy: boolean;
  submit: <Command extends LiteratureCommand | LiteratureCollectionCommand>(
    command: Command
  ) => Promise<ReceiptFor<Command>>;
  changeStatus: (itemId: string, value: LiteratureReadingStatus) => void;
  close: () => void;
}) {
  const { t } = useTranslation();
  const selected = reads.detail.status === 'ready' ? reads.detail.value : undefined;
  const stale = Boolean(reads.detail.refreshing || reads.detail.refreshError);
  return (
    <aside aria-label={t('conversation.libraryWorkbench.details')}>
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
          <button type='button' onClick={close}>
            {t('common.close')}
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
            close={close}
          />
        </>
      )}
    </aside>
  );
}
