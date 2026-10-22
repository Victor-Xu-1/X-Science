import type { PdfReadingSnapshot } from '@/common/adapter/pdfReadingContext';
import { boundPdfPreview } from '@/renderer/services/runtime/pdfReadingPreview';
import { useOptionalPreviewContext } from '../../Preview/context/PreviewContext';
import { BookOpenText } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';

/** Historical evidence opens its original version/page; it never relinks it. */
export function MessagePdfReadingContext({
  snapshot,
  unavailable,
}: {
  snapshot?: PdfReadingSnapshot;
  unavailable?: boolean;
}) {
  const preview = useOptionalPreviewContext();
  const { t } = useTranslation();
  if (unavailable)
    return (
      <span className='text-12px text-t-secondary' role='status'>
        {t('conversation.reading.historyUnavailable')}
      </span>
    );
  const binding =
    snapshot?.bindings.find((item) => item.binding_id === snapshot.active_binding_id) ?? snapshot?.bindings[0];
  if (!binding || !snapshot || !preview) return null;
  const label = snapshot.page_number
    ? t('conversation.reading.pageLabel', { page: snapshot.page_number })
    : t('conversation.reading.label');
  return (
    <button
      type='button'
      data-slot='message-pdf-reading-context'
      className='mb-6px inline-flex items-center gap-6px rounded-6px border border-solid border-border-2 bg-fill-2 px-8px py-2px text-13px leading-20px hover:bg-fill-1'
      title={binding.name}
      aria-label={`${label} · ${binding.name}`}
      onClick={() => {
        const target = boundPdfPreview(binding, snapshot, snapshot.page_number);
        preview.openPreview(target.content, 'pdf', target.metadata, { presentation: 'single' });
      }}
    >
      <BookOpenText size={14} strokeWidth={2} aria-hidden='true' />
      {label}
    </button>
  );
}
