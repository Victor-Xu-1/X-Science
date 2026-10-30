import React from 'react';
import { LoaderCircle, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { literatureReadingStatuses, type LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { librarySelectionCount, type LibrarySelectionState } from './librarySelectionState';
import './librarySelection.css';

export type LibrarySelectionToolbarProps = {
  selection: LibrarySelectionState;
  totalMatching?: number;
  canSelectAllMatching: boolean;
  disabled: boolean;
  busy?: boolean;
  exportControl?: React.ReactNode;
  resolving?: boolean;
  onSelectAllMatching: () => void;
  onClear: () => void;
  onChangeStatus: (status: LiteratureReadingStatus) => void;
};

/** Controlled selection actions; request, progress and persistence remain with the caller. */
export function LibrarySelectionToolbar({
  selection,
  totalMatching,
  canSelectAllMatching,
  disabled,
  busy = false,
  exportControl,
  resolving = false,
  onSelectAllMatching,
  onClear,
  onChangeStatus,
}: LibrarySelectionToolbarProps) {
  const { t } = useTranslation();
  const count = librarySelectionCount(selection, totalMatching);
  const hasSelection = selection.allMatchingSelected || selection.selectedIds.length > 0;
  const blocked = disabled || busy || resolving;
  return (
    <div
      role='toolbar'
      aria-label={t('conversation.librarySelection.title')}
      aria-busy={busy || resolving}
      className='library-selection-toolbar'
    >
      <div className='library-selection-toolbar__summary' aria-live='polite'>
        {selection.allMatchingSelected ? (
          <>
            <span className='library-selection-toolbar__count'>
              {t(
                selection.excludedMatchingIds.length
                  ? 'conversation.librarySelection.allExcept'
                  : 'conversation.librarySelection.allSelected',
                { count: selection.excludedMatchingIds.length }
              )}
            </span>
            {count !== undefined && <span>{t('conversation.librarySelection.selectedCount', { count })}</span>}
          </>
        ) : (
          <span className='library-selection-toolbar__count'>
            {t('conversation.librarySelection.selectedCount', { count: selection.selectedIds.length })}
          </span>
        )}
      </div>
      {!selection.allMatchingSelected && hasSelection && canSelectAllMatching && (
        <button type='button' disabled={blocked} onClick={onSelectAllMatching}>
          {t('conversation.librarySelection.selectAllMatching')}
        </button>
      )}
      {exportControl}
      <label className='library-selection-toolbar__status'>
        <span className='sr-only'>{t('conversation.librarySelection.readingStatus')}</span>
        <select
          aria-label={t('conversation.librarySelection.readingStatus')}
          value=''
          disabled={blocked || !hasSelection}
          onChange={(event) => {
            const status = literatureReadingStatuses.find((value) => value === event.currentTarget.value);
            if (!blocked && hasSelection && status) onChangeStatus(status);
          }}
        >
          <option value='' disabled>
            {t('conversation.librarySelection.changeStatus')}
          </option>
          {literatureReadingStatuses.map((status) => (
            <option key={status} value={status}>
              {t(`conversation.libraryReadingStatus.${status}`)}
            </option>
          ))}
        </select>
      </label>
      {resolving && (
        <span role='status' className='library-selection-toolbar__working'>
          <LoaderCircle size={14} aria-hidden='true' />
          {t('conversation.librarySelection.resolving')}
        </span>
      )}
      <button
        type='button'
        className='library-selection-toolbar__clear'
        aria-label={t('conversation.librarySelection.clear')}
        disabled={blocked || !hasSelection}
        onClick={onClear}
      >
        <X size={14} aria-hidden='true' />
      </button>
    </div>
  );
}
