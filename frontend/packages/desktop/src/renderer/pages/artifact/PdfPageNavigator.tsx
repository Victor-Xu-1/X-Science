import React, { useState } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export function PdfPageNavigator({
  page,
  count,
  navigate,
}: {
  page: number;
  count: number;
  navigate: (page: number) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<string | null>(null);
  const commit = () => {
    if (draft !== null && /^\d+$/.test(draft)) navigate(Number(draft));
    setDraft(null);
  };
  return (
    <div className='inline-flex items-center gap-4px text-12px' aria-label={t('conversation.reading.pageNavigation')}>
      <button
        type='button'
        disabled={page <= 1}
        onClick={() => navigate(page - 1)}
        aria-label={t('conversation.reading.previousPage')}
      >
        <ChevronLeft size={14} />
      </button>
      <input
        type='text'
        inputMode='numeric'
        value={draft ?? String(page)}
        onChange={(event) => setDraft(event.target.value)}
        aria-label={t('conversation.reading.currentPage')}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            commit();
          }
          if (event.key === 'Escape') setDraft(null);
        }}
        className='w-36px border-0 rd-4px bg-transparent text-center text-current focus-visible:outline-2 focus-visible:outline-brand'
      />
      <span>/ {count}</span>
      <button
        type='button'
        disabled={page >= count}
        onClick={() => navigate(page + 1)}
        aria-label={t('conversation.reading.nextPage')}
      >
        <ChevronRight size={14} />
      </button>
    </div>
  );
}
