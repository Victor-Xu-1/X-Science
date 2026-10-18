import { Link2 } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import './readingContext.css';

export function PdfReadingSuggestion({ onKeepAttachments }: { onKeepAttachments?: () => void }) {
  const { t } = useTranslation();
  return (
    <div className='pdf-reading-notice' data-testid='pdf-reading-suggestion'>
      <Link2 size={14} strokeWidth={2} aria-hidden='true' />
      <span>{t('conversation.reading.willLink')}</span>
      {onKeepAttachments && (
        <button type='button' onClick={onKeepAttachments}>
          {t('conversation.reading.keepAttachments')}
        </button>
      )}
    </div>
  );
}
