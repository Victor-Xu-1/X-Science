import React, { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown } from 'lucide-react';
import type { SelectHandle } from '@arco-design/web-react/es/Select/interface';
import { useTranslation } from 'react-i18next';
import XScienceSelect from '@/renderer/components/base/XScienceSelect';
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
  const select = useRef<SelectHandle>(null);
  const [open, setOpen] = useState(false);
  const visible = open && !disabled;
  const dateOrder = value === 'updated-desc' || value === 'created-desc' || value === 'created-asc';
  const dateHint = dateOrder ? t('conversation.librarySort.unknownDatesLast') : undefined;
  const choices: Array<{ value: LiteraturePageSort | ''; label: string }> = [
    { value: 'updated-desc', label: t('conversation.librarySort.updatedDescending') },
    { value: 'created-desc', label: t('conversation.librarySort.createdDescending') },
    { value: 'created-asc', label: t('conversation.librarySort.createdAscending') },
    { value: 'title-asc', label: t('conversation.librarySort.titleAscending') },
    { value: 'title-desc', label: t('conversation.librarySort.titleDescending') },
    { value: '', label: t('conversation.librarySort.current') },
  ];
  const selected = value ?? '';
  const selectedLabel = choices.find((choice) => choice.value === selected)?.label;
  useEffect(() => {
    if (disabled) {
      setOpen(false);
    }
  }, [disabled]);
  const changeVisibility = (next: boolean) => {
    setOpen(next && !disabled);
  };
  return (
    <div className='library-sort-control' title={dateHint}>
      <XScienceSelect
        ref={select}
        className='library-sort-select'
        value={selected}
        disabled={disabled}
        aria-label={t('conversation.librarySort.label')}
        aria-description={dateHint}
        showSearch={false}
        popupVisible={visible}
        onVisibleChange={changeVisibility}
        renderFormat={() => selectedLabel}
        arrowIcon={<ChevronDown size={16} aria-hidden='true' />}
        dropdownMenuClassName='library-sort-options'
        triggerProps={{
          className: 'library-sort-popup',
          classNames: 'library-sort-popup-transition',
          duration: 0,
          autoAlignPopupWidth: false,
          position: 'bl',
        }}
        onKeyDown={(event) => {
          if (disabled || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey)
            return;
          const space = event.key === ' ' || event.key === 'Spacebar';
          if (!visible && (space || event.key === 'ArrowDown' || event.key === 'ArrowUp')) {
            event.preventDefault();
            changeVisibility(true);
          } else if (visible && space) {
            event.preventDefault();
            // Delegate commit to the primitive's public Enter path; it owns active options.
            select.current?.hotkeyHandler(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13 }));
          }
        }}
        onChange={(raw: unknown) => {
          if (disabled) return;
          const next = literaturePageSorts.find((sort) => sort === raw);
          if ((raw === '' || next) && next !== value) {
            changeVisibility(false);
            onChange(next);
          }
        }}
      >
        {choices.map((choice) => (
          <XScienceSelect.Option key={choice.value} value={choice.value}>
            <span className='library-sort-option-label'>{choice.label}</span>
            {choice.value === selected && <Check size={16} className='library-sort-option-check' aria-hidden='true' />}
          </XScienceSelect.Option>
        ))}
      </XScienceSelect>
    </div>
  );
}
