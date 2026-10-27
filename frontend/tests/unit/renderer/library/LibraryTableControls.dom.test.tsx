import React, { useState } from 'react';
import { act, cleanup, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  type LibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';
import { LibraryTable, type LibraryTableProps } from '@/renderer/pages/library/LibraryTable';
import { LibraryColumnMenu, type LibraryColumnMenuProps } from '@/renderer/pages/library/LibraryColumnMenu';
import { renderWithI18n } from '../../i18nTestUtils';

const reference: LiteratureItem = {
  id: 'reference-a',
  title: 'Synthetic reference',
  metadata_revision: 1,
  metadata: { year: 2025, authors: 'A. Author', doi: '10.1234/reference' },
  deleted: false,
  project_ids: [],
  attachments: [
    {
      id: 'attachment',
      kind: 'fullText',
      versions: [
        {
          id: 'version',
          attachment_id: 'attachment',
          version_number: 1,
          filename: 'reference.pdf',
          mime_type: 'application/pdf',
          size_bytes: 3,
          checksum: 'a'.repeat(64),
          page_count: 2,
          available: true,
        },
      ],
    },
  ],
};
const tableProps = (extra: Partial<LibraryTableProps> = {}): LibraryTableProps => ({
  rows: [reference],
  readStatus: 'ready',
  selectedId: reference.id,
  preferences: DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  authors: (item) => String(item.metadata.authors),
  statusCell: () => <button type='button'>Independent state control</button>,
  open: vi.fn(),
  registerTitleButton: vi.fn(),
  onRetry: vi.fn(),
  ...extra,
});
const headings = () => screen.getAllByRole('columnheader').map((element) => element.textContent);
function Controlled({ changed = vi.fn() }: { changed?: (preferences: LibraryColumnPreferences) => void }) {
  const [preferences, setPreferences] = useState(DEFAULT_LIBRARY_COLUMN_PREFERENCES);
  return (
    <>
      <LibraryColumnMenu
        preferences={preferences}
        status='ready'
        onPreferencesChange={(next) => {
          changed(next);
          setPreferences(next);
        }}
        onRetry={vi.fn()}
        onReset={() => setPreferences(DEFAULT_LIBRARY_COLUMN_PREFERENCES)}
      />
      <LibraryTable {...tableProps({ preferences })} />
    </>
  );
}
async function openColumns() {
  const trigger = screen.getByRole('button', { name: 'Columns' });
  fireEvent.click(trigger);
  const panel = await screen.findByRole('dialog', { name: 'Customize columns' });
  return { panel, trigger };
}
function dispatchDrag(kind: 'dragStart' | 'dragOver' | 'drop', target: HTMLElement, transfer: object, clientY: number) {
  const event = createEvent[kind](target);
  Object.defineProperties(event, { dataTransfer: { value: transfer }, clientY: { value: clientY } });
  fireEvent(target, event);
}

describe('Pure Library table and controlled column settings', () => {
  const unexpectedFetch = vi.fn(() => {
    throw new Error('Pure Library controls must not request network');
  });
  beforeEach(() => {
    unexpectedFetch.mockClear();
    vi.stubGlobal('fetch', unexpectedFetch);
  });
  afterEach(() => {
    cleanup();
    try {
      expect(unexpectedFetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      vi.restoreAllMocks();
    }
  });

  it('renders only supported visible columns, keeps independent row controls and follows the current language', async () => {
    const props = tableProps();
    const rendered = await renderWithI18n(<LibraryTable {...props} />, 'en-US');
    expect(headings()).toEqual(['Title', 'Status', 'Year', 'Authors', 'Files']);
    expect(screen.queryByRole('columnheader', { name: 'DOI' })).toBeNull();
    expect(screen.getByText('2025')).toBeInTheDocument();
    expect(screen.getByText('A. Author')).toBeInTheDocument();
    expect(screen.getByLabelText('1 file')).toBeInTheDocument();
    const opener = screen.getByRole('button', { name: 'Synthetic reference' });
    expect(opener).toHaveAttribute('aria-pressed', 'true');
    expect(props.registerTitleButton).toHaveBeenCalledWith(reference.id, opener);
    fireEvent.click(screen.getByRole('button', { name: 'Independent state control' }));
    expect(props.open).not.toHaveBeenCalled();
    fireEvent.click(opener);
    expect(props.open).toHaveBeenCalledTimes(1);
    expect(props.open).toHaveBeenCalledWith(reference);
    expect(rendered.container.querySelector('button button')).toBeNull();
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    expect(headings()).toEqual(['标题', '阅读状态', '年份', '作者', '文件']);
    expect(screen.getByRole('table', { name: '文献列表' })).toBeInTheDocument();
  });

  it('reorders with the keyboard, hides optional columns without leaving width behind, and restores Escape focus', async () => {
    await renderWithI18n(<Controlled />, 'en-US');
    const originalWidth = (screen.getByRole('table') as HTMLTableElement).style.minWidth;
    const { panel, trigger } = await openColumns();
    const move = within(panel).getByRole('button', { name: 'Move Authors' });
    move.focus();
    fireEvent.keyDown(move, { key: 'ArrowUp' });
    expect(headings()).toEqual(['Title', 'Status', 'Authors', 'Year', 'Files']);
    expect(within(panel).getByRole('button', { name: 'Move Authors' })).toHaveFocus();
    for (const checkbox of within(panel).getAllByRole('checkbox') as HTMLInputElement[])
      if (checkbox.checked) fireEvent.click(checkbox);
    expect(headings()).toEqual(['Title']);
    expect(parseFloat((screen.getByRole('table') as HTMLTableElement).style.minWidth)).toBeLessThan(
      parseFloat(originalWidth)
    );
    expect(screen.getByRole('button', { name: 'Synthetic reference' })).toBeInTheDocument();
    fireEvent.keyDown(panel, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(trigger).toHaveFocus();
  });

  it('applies a controlled drag before a known column and ignores a drop without a local drag', async () => {
    const changed = vi.fn();
    await renderWithI18n(<Controlled changed={changed} />, 'en-US');
    const { panel } = await openColumns();
    const source = within(panel).getByRole('button', { name: 'Move Year' });
    const target = within(panel).getByRole('button', { name: 'Move Status' }).closest('li')!;
    vi.spyOn(target, 'getBoundingClientRect').mockReturnValue({
      top: 10,
      height: 40,
      left: 0,
      width: 300,
      right: 300,
      bottom: 50,
      x: 0,
      y: 10,
      toJSON: () => ({}),
    });
    const transfer = { effectAllowed: '', dropEffect: '', setData: vi.fn() };
    dispatchDrag('dragStart', source, transfer, 10);
    dispatchDrag('dragOver', target, transfer, 12);
    expect(target).toHaveAttribute('data-drop', 'before');
    dispatchDrag('drop', target, transfer, 12);
    expect(headings()).toEqual(['Title', 'Year', 'Status', 'Authors', 'Files']);
    expect(changed).toHaveBeenCalledTimes(1);
    dispatchDrag('drop', target, transfer, 12);
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it('shows actual preference loading, invalid and unsaved states without claiming persistence success', async () => {
    const props: LibraryColumnMenuProps = {
      preferences: DEFAULT_LIBRARY_COLUMN_PREFERENCES,
      status: 'loading',
      onPreferencesChange: vi.fn(),
      onRetry: vi.fn(),
      onReset: vi.fn(),
    };
    const rendered = await renderWithI18n(<LibraryColumnMenu {...props} />, 'en-US');
    const { panel, trigger } = await openColumns();
    expect(panel).toHaveAttribute('aria-modal', 'false');
    expect(panel).toHaveAttribute('aria-busy', 'true');
    expect(within(panel).getByText('Loading column preferences…')).toBeInTheDocument();
    expect(within(panel).getByRole('checkbox', { name: 'Show Authors' })).toBeDisabled();
    rendered.rerender(<LibraryColumnMenu {...props} status='invalid' />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Saved column settings are invalid.');
    expect(screen.getByRole('checkbox', { name: 'Show Authors' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(props.onRetry).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibraryColumnMenu {...props} status='read-error' />);
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load column settings.');
    fireEvent.click(screen.getByRole('button', { name: 'Restore default columns' }));
    expect(props.onReset).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibraryColumnMenu {...props} status='saving' />);
    expect(screen.getByRole('checkbox', { name: 'Show Authors' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Restore default columns' })).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show Authors' }));
    expect(props.onPreferencesChange).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibraryColumnMenu {...props} status='write-error' />);
    expect(screen.getByRole('alert')).toHaveTextContent('Your layout has not been saved.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(props.onRetry).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole('button', { name: 'Close column settings' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(trigger).toHaveFocus();
  });

  it('distinguishes loading, failed and empty reads while leaving clear-filter and retry ownership with the caller', async () => {
    const clear = vi.fn();
    const props = tableProps({ readStatus: 'loading' });
    const rendered = await renderWithI18n(<LibraryTable {...props} />, 'en-US');
    expect(screen.getByRole('status')).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('Loading references…');
    expect(screen.queryByRole('button', { name: 'Synthetic reference' })).toBeNull();
    rendered.rerender(<LibraryTable {...props} readStatus='error' />);
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load references');
    expect(screen.getByRole('alert').closest('.library-table__viewport')).toBeNull();
    expect(screen.queryByText('No references yet')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(props.onRetry).toHaveBeenCalledTimes(1);
    rendered.rerender(
      <LibraryTable
        {...props}
        readStatus='ready'
        rows={[]}
        emptyTitle='No matching references'
        emptyDescription='Change the filter.'
        emptyAction={
          <button type='button' onClick={clear}>
            Clear filters
          </button>
        }
      />
    );
    expect(screen.getByRole('status')).toHaveTextContent('No matching references');
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(clear).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('status').closest('.library-table__viewport')).toBeNull();
    expect(screen.queryByRole('table')).toBeNull();
  });
});
