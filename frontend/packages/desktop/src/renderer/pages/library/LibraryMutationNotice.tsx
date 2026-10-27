import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureMutationReceipt } from '@/renderer/services/runtime/LiteratureMutationController';

/** Feedback projection only; the account-scoped controller owns every operation. */
export function LibraryMutationNotice({
  hidden,
  error,
  failed,
  mutating,
  acknowledged,
  retry,
}: {
  hidden: boolean;
  error: 'uncertain' | 'storage' | 'rejected' | null;
  failed: boolean;
  mutating: boolean;
  acknowledged?: LiteratureMutationReceipt;
  retry: () => void;
}) {
  const { t } = useTranslation();
  const message =
    acknowledged?.schema === 'x-science.literature-receipt.v1'
      ? t('conversation.reading.libraryUpdated', { name: acknowledged.item.title })
      : acknowledged?.schema === 'x-science.literature-reading-status-receipt.v1'
        ? t('conversation.libraryReadingStatus.updated')
        : acknowledged?.schema === 'x-science.literature-collection-receipt.v1'
          ? acknowledged.action === 'membership'
            ? t('conversation.libraryWorkbench.membershipUpdated')
            : acknowledged.action === 'delete'
              ? t('conversation.libraryWorkbench.collectionDeleted')
              : t('conversation.libraryWorkbench.collectionUpdated', { name: acknowledged.collection.name })
          : undefined;
  return (
    <>
      {!hidden && (error || failed) && (
        <div role='alert' className='library-workbench__notice'>
          <span>
            {t(
              error === 'uncertain'
                ? 'conversation.reading.libraryUncertain'
                : error === 'storage'
                  ? 'conversation.reading.libraryStorage'
                  : 'conversation.reading.libraryRejected'
            )}
          </span>
          <button type='button' disabled={mutating} onClick={retry}>
            {t('common.retry')}
          </button>
        </div>
      )}
      {message && (
        <p role='status' className='library-workbench__notice'>
          {message}
        </p>
      )}
    </>
  );
}
