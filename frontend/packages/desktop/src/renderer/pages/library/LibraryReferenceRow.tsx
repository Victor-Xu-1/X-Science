import React from 'react';
import { BookOpen, ChevronRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { literatureItemReadingStatus, type LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { LibraryReadingStatus } from './LibraryReadingStatus';

/** Keep the detail opener and personal-state control as independent keyboard targets. */
export function LibraryReferenceRow({
  item,
  authors,
  selected,
  busy,
  register,
  open,
  changeStatus,
}: {
  item: LiteratureItem;
  authors: string;
  selected: boolean;
  busy: boolean;
  register: (node: HTMLButtonElement | null) => void;
  open: () => void;
  changeStatus: (value: LiteratureReadingStatus) => void;
}) {
  const { t } = useTranslation();
  return (
    <li className='library-workbench__reference-row' data-selected={selected}>
      <button ref={register} type='button' className='library-workbench__item' aria-pressed={selected} onClick={open}>
        <span className='library-workbench__book'>
          <BookOpen size={16} aria-hidden='true' />
        </span>
        <span className='library-workbench__item-copy'>
          <strong>{item.title}</strong>
          <span>{authors || t('conversation.libraryWorkbench.noAuthors')}</span>
        </span>
        <ChevronRight size={14} aria-hidden='true' />
      </button>
      <LibraryReadingStatus
        value={literatureItemReadingStatus(item)}
        disabled={busy || item.deleted}
        onChange={changeStatus}
        accessibleLabel={t('conversation.libraryReadingStatus.forPaper', { title: item.title })}
      />
    </li>
  );
}
