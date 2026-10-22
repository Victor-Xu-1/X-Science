import {
  pdfReadingSourceKey,
  samePdfReadingSource,
  type PdfReadingPosition,
  type PdfReadingSource,
} from '@/common/adapter/pdfReadingContext';
import { useOptionalPreviewContext } from '../../context/PreviewContext';
import { Link2, Loader2 } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import '@/renderer/components/chat/SendBox/reading/readingContext.css';

export function PdfReadingPreviewAction({
  source,
  projectId,
  position,
  name = 'PDF',
}: {
  source?: PdfReadingSource;
  projectId?: string;
  position?: PdfReadingPosition;
  name?: string;
}) {
  const { t } = useTranslation();
  const owner = useOptionalPreviewContext()?.pdfReadingOwner;
  const [error, setError] = useState(false);
  const sourceKey = source ? pdfReadingSourceKey(source) : undefined;
  const authority = useRef<object>({});
  useEffect(() => {
    const token = {};
    authority.current = token;
    setError(false);
    return () => {
      if (authority.current === token) authority.current = {};
    };
  }, [
    owner?.ownerId,
    owner?.frameId,
    owner?.context?.scope.frame_incarnation_id,
    owner?.context?.scope.root_frame_incarnation_id,
    sourceKey,
  ]);
  if (!source || !owner || projectId !== owner.projectId) return null;
  const binding = owner.context?.bindings.find((item) => samePdfReadingSource(item, source));
  const draftLinked = owner.draftSources?.some((item) => samePdfReadingSource(item, source));
  const linked = Boolean(binding || draftLinked);
  if (!linked && (!position || position.pageCount <= 1)) return null;
  const full = !linked && (owner.context?.bindings.length ?? owner.draftSources?.length ?? 0) >= 3;
  const run = async () => {
    const token = authority.current;
    setError(false);
    try {
      if (position) owner.reportPosition(source, position);
      if (binding) await owner.unlink(binding.binding_id);
      else if (draftLinked && owner.unlinkDraft) await owner.unlinkDraft(source);
      else await owner.link(source, { name, position });
    } catch {
      if (authority.current === token) setError(true);
    }
  };
  return (
    <div className='pdf-reading-preview-action flex items-center gap-8px px-12px py-4px'>
      <button
        type='button'
        className='inline-flex items-center gap-6px border-0 rd-8px bg-transparent px-8px py-4px text-12px hover:bg-fill-2'
        aria-pressed={linked}
        disabled={owner.pending || full}
        onClick={() => void run()}
      >
        {owner.pending ? <Loader2 size={16} className='pdf-reading-spin' /> : <Link2 size={16} strokeWidth={2} />}
        {t(linked ? 'conversation.reading.remove' : 'conversation.reading.readWithAgent')}
      </button>
      {full && <span>{t('conversation.reading.limit')}</span>}
      {error && <span role='alert'>{t('conversation.reading.mutationFailed')}</span>}
    </div>
  );
}
