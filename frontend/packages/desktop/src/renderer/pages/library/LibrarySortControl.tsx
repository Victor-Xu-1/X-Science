import React from 'react';
import { ArrowUpDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { literaturePageSorts, type LiteraturePageSort } from '@/renderer/services/runtime/literatureLibrary';

/** Controlled presentation. URL changes and the single page reader stay with LibraryPage. */
export function LibrarySortControl({
  value,
  disabled,
  onChange,
}: {
  value?: LiteraturePageSort;
  disabled: boolean;
  onChange: (value: LiteraturePageSort | undefined) => void;
}) {
  const { t } = useTranslation();
  return (
    <label className='library-sort-control'>
      <span className='sr-only'>{t('conversation.librarySort.label')}</span>
      <ArrowUpDown size={14} aria-hidden='true' />
      <select
        value={value ?? ''}
        disabled={disabled}
        onChange={(event) => {
          if (disabled) return;
          const next = literaturePageSorts.find((sort) => sort === event.currentTarget.value);
          if (event.currentTarget.value === '' || next) onChange(next);
        }}
      >
        <option value=''>{t('conversation.librarySort.current')}</option>
        <option value='title-asc'>{t('conversation.librarySort.titleAscending')}</option>
        <option value='title-desc'>{t('conversation.librarySort.titleDescending')}</option>
      </select>
    </label>
  );
}
