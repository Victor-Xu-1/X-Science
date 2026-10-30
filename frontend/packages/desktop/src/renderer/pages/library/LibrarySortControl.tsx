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
  const dateOrder = value === 'updated-desc' || value === 'created-desc' || value === 'created-asc';
  const dateHint = dateOrder ? t('conversation.librarySort.unknownDatesLast') : undefined;
  return (
    <label className='library-sort-control'>
      <span className='sr-only'>{t('conversation.librarySort.label')}</span>
      <ArrowUpDown size={14} aria-hidden='true' />
      <select
        value={value ?? ''}
        disabled={disabled}
        title={dateHint}
        aria-description={dateHint}
        onChange={(event) => {
          if (disabled) return;
          const next = literaturePageSorts.find((sort) => sort === event.currentTarget.value);
          if (event.currentTarget.value === '' || next) onChange(next);
        }}
      >
        <option value=''>{t('conversation.librarySort.current')}</option>
        <option value='title-asc'>{t('conversation.librarySort.titleAscending')}</option>
        <option value='title-desc'>{t('conversation.librarySort.titleDescending')}</option>
        <option value='updated-desc'>{t('conversation.librarySort.updatedDescending')}</option>
        <option value='created-desc'>{t('conversation.librarySort.createdDescending')}</option>
        <option value='created-asc'>{t('conversation.librarySort.createdAscending')}</option>
      </select>
    </label>
  );
}
