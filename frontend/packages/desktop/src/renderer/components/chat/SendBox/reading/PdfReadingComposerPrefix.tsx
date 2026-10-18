import React from 'react';
import { useTranslation } from 'react-i18next';
import type { usePdfReadingComposer } from '@/renderer/hooks/chat/usePdfReadingComposer';
import { PdfReadingBar } from './PdfReadingBar';
import { PdfReadingSuggestion } from './PdfReadingSuggestion';

export function PdfReadingComposerPrefix({
  enabled,
  reading,
  initialSend,
  suggestion,
  frozenQueue,
  draftPersistence,
  children,
}: {
  enabled: boolean;
  reading: ReturnType<typeof usePdfReadingComposer>;
  initialSend: {
    failed: boolean;
    preparationFailed?: boolean;
    retry: () => void;
    canKeepPdfAttachments?: boolean;
    keepPdfAttachments?: () => void;
  };
  suggestion: { visible: boolean; keepAttachments: () => void };
  frozenQueue: { present: boolean; useCurrent: () => void };
  draftPersistence?: { failed: boolean; retry: () => void };
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <>
      {draftPersistence?.failed && (
        <div className='pdf-reading-notice' role='alert'>
          {t('conversation.sendbox.draftSaveFailed')}
          <button type='button' onClick={draftPersistence.retry}>
            {t('conversation.sendbox.retryDraftSave')}
          </button>
        </div>
      )}
      {initialSend.failed && (
        <div className='pdf-reading-notice' role='alert'>
          {t(
            initialSend.preparationFailed
              ? 'conversation.reading.prepareFailed'
              : 'conversation.reading.initialSendFailed'
          )}
          <button type='button' onClick={initialSend.retry}>
            {t('common.retry')}
          </button>
          {initialSend.canKeepPdfAttachments && !reading.view.pending && (
            <button type='button' onClick={initialSend.keepPdfAttachments}>
              {t('conversation.reading.keepAttachments')}
            </button>
          )}
        </div>
      )}
      {enabled && <PdfReadingBar view={reading.view} controller={reading.controller} onOpen={reading.open} />}
      {suggestion.visible && <PdfReadingSuggestion onKeepAttachments={suggestion.keepAttachments} />}
      {frozenQueue.present && (
        <div className='pdf-reading-notice' role='status'>
          {t('conversation.reading.queuedOriginal')}
          <button
            type='button'
            disabled={!reading.view.context || Boolean(reading.view.pending)}
            onClick={frozenQueue.useCurrent}
          >
            {t('conversation.reading.useCurrent')}
          </button>
        </div>
      )}
      {children}
    </>
  );
}
