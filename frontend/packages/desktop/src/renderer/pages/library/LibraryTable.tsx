import React from 'react';
import { BookOpen, LoaderCircle, Paperclip } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LibraryColumnId, LibraryColumnPreferences } from '@/common/adapter/libraryColumnPreferences';
import './libraryTable.css';

export type LibraryTableProps = {
  rows: readonly LiteratureItem[];
  readStatus: 'loading' | 'ready' | 'error';
  selectedId: string;
  preferences: LibraryColumnPreferences;
  authors: (item: LiteratureItem) => string;
  statusCell: (item: LiteratureItem) => React.ReactNode;
  open: (item: LiteratureItem) => void;
  registerTitleButton: (itemId: string, element: HTMLButtonElement | null) => void;
  onRetry: () => void;
  emptyTitle?: string;
  emptyDescription?: string;
  emptyAction?: React.ReactNode;
};
const widths: Record<LibraryColumnId, number> = {
  readingStatus: 128,
  year: 80,
  authors: 200,
  doi: 220,
  attachments: 88,
};
const metadataText = (value: unknown) =>
  typeof value === 'string' ? value : typeof value === 'number' && Number.isSafeInteger(value) ? String(value) : '';

/** Pure catalogue projection. The caller retains every read, selection and mutation owner. */
export function LibraryTable({
  rows,
  readStatus,
  selectedId,
  preferences,
  authors,
  statusCell,
  open,
  registerTitleButton,
  onRetry,
  emptyTitle,
  emptyDescription,
  emptyAction,
}: LibraryTableProps) {
  const { t } = useTranslation();
  const visible = new Set(preferences.visible);
  const columns = preferences.order.filter((column) => visible.has(column));
  const cell = (column: LibraryColumnId, item: LiteratureItem): React.ReactNode => {
    if (column === 'readingStatus') return statusCell(item);
    if (column === 'attachments') {
      const count = item.attachments.length;
      return count ? (
        <span className='library-table__files' aria-label={t('conversation.libraryTable.files', { count })}>
          <Paperclip size={14} aria-hidden='true' />
          <span>{count}</span>
        </span>
      ) : (
        <span aria-label={t('conversation.libraryTable.noFiles')}>—</span>
      );
    }
    const value = column === 'authors' ? authors(item) : metadataText(item.metadata[column]);
    return (
      <span className='library-table__text' title={value || undefined}>
        {value || '—'}
      </span>
    );
  };
  return (
    <div className='library-table'>
      <div
        className='library-table__viewport'
        role='region'
        aria-label={t('conversation.libraryTable.scrollRegion')}
        tabIndex={0}
      >
        <table
          aria-label={t('conversation.libraryTable.title')}
          aria-busy={readStatus === 'loading'}
          style={{ minWidth: 280 + columns.reduce((sum, column) => sum + widths[column], 0) }}
        >
          <colgroup>
            <col style={{ width: 280 }} />
            {columns.map((column) => (
              <col key={column} style={{ width: widths[column] }} />
            ))}
          </colgroup>
          <thead>
            <tr>
              <th scope='col'>{t('conversation.libraryTable.titleColumn')}</th>
              {columns.map((column) => (
                <th scope='col' key={column}>
                  {t(`conversation.libraryTable.columns.${column}`)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {readStatus !== 'ready' || !rows.length ? (
              <tr>
                <td colSpan={columns.length + 1} className='library-table__state-cell'>
                  <div className='library-table__state' role={readStatus === 'error' ? 'alert' : 'status'}>
                    {readStatus === 'loading' ? (
                      <>
                        <LoaderCircle size={20} className='library-table__spin' aria-hidden='true' />
                        <span>{t('conversation.libraryTable.loading')}</span>
                      </>
                    ) : readStatus === 'error' ? (
                      <>
                        <h2>{t('conversation.libraryTable.loadFailed')}</h2>
                        <button type='button' onClick={onRetry}>
                          {t('common.retry')}
                        </button>
                      </>
                    ) : (
                      <>
                        <BookOpen size={28} aria-hidden='true' />
                        <h2>{emptyTitle ?? t('conversation.libraryTable.empty')}</h2>
                        <p>{emptyDescription ?? t('conversation.libraryTable.emptyHint')}</p>
                        {emptyAction}
                      </>
                    )}
                  </div>
                </td>
              </tr>
            ) : (
              rows.map((item) => (
                <tr key={item.id} data-selected={item.id === selectedId}>
                  <td className='library-table__title-cell'>
                    <button
                      type='button'
                      className='library-table__title'
                      ref={(element) => registerTitleButton(item.id, element)}
                      aria-pressed={item.id === selectedId}
                      onClick={() => open(item)}
                    >
                      <span>{item.title}</span>
                    </button>
                  </td>
                  {columns.map((column) => (
                    <td
                      key={column}
                      data-column={column}
                      className={column === 'year' || column === 'attachments' ? 'library-table__numeric' : undefined}
                    >
                      {cell(column, item)}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
