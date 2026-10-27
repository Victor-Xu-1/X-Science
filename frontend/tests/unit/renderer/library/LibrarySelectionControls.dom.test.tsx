import React from 'react';
import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { DEFAULT_LIBRARY_COLUMN_PREFERENCES } from '@/common/adapter/libraryColumnPreferences';
import {
  LibraryTable,
  type LibraryTableProps,
  type LibraryTableSelection,
} from '@/renderer/pages/library/LibraryTable';
import {
  LibrarySelectionToolbar,
  type LibrarySelectionToolbarProps,
} from '@/renderer/pages/library/LibrarySelectionToolbar';
import { allMatchingLibrarySelection, explicitLibrarySelection } from '@/renderer/pages/library/librarySelectionState';
import { renderWithI18n } from '../../i18nTestUtils';

const item: LiteratureItem = {
  id: 'paper',
  title: 'Synthetic paper',
  metadata_revision: 1,
  metadata: { year: 2024 },
  deleted: false,
  project_ids: [],
  attachments: [],
};
const tableProps = (): LibraryTableProps => ({
  rows: [item],
  readStatus: 'ready',
  selectedId: item.id,
  preferences: DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  authors: () => 'Ada',
  statusCell: () => <button type='button'>Independent status</button>,
  open: vi.fn(),
  registerTitleButton: vi.fn(),
  onRetry: vi.fn(),
});
const toolbarProps = (): LibrarySelectionToolbarProps => ({
  selection: explicitLibrarySelection(['a', 'b']),
  canSelectAllMatching: true,
  disabled: false,
  onSelectAllMatching: vi.fn(),
  onClear: vi.fn(),
  onChangeStatus: vi.fn(),
});

describe('Pure Library selection controls', () => {
  const fetch = vi.fn(() => {
    throw new Error('Selection presentation must not fetch');
  });
  beforeEach(() => {
    fetch.mockClear();
    vi.stubGlobal('fetch', fetch);
  });
  afterEach(() => {
    cleanup();
    try {
      expect(fetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
      vi.restoreAllMocks();
    }
  });

  it('adds optional mixed/page/shift checkboxes without replacing title refs or opening details', async () => {
    const props = tableProps();
    const rendered = await renderWithI18n(<LibraryTable {...props} />, 'en-US');
    const table = screen.getByRole('table');
    expect(table.style.minWidth).toBe('776px');
    expect(screen.queryByRole('checkbox')).toBeNull();
    const opener = screen.getByRole('button', { name: item.title });
    expect(props.registerTitleButton).toHaveBeenCalledWith(item.id, opener);
    const selection: LibraryTableSelection = {
      selected: () => false,
      toggle: vi.fn(),
      pageChecked: false,
      pageMixed: true,
      togglePage: vi.fn(),
      disabled: false,
    };
    rendered.rerender(<LibraryTable {...props} selection={selection} />);
    expect(screen.getByRole('table').style.minWidth).toBe('816px');
    expect(screen.getByRole('checkbox', { name: 'Select this page' })).toHaveProperty('indeterminate', true);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Synthetic paper' }), { shiftKey: true });
    expect(selection.toggle).toHaveBeenCalledWith(item.id, true);
    expect(props.open).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select this page' }));
    expect(selection.togglePage).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: item.title }));
    expect(props.open).toHaveBeenCalledWith(item);
    fireEvent.click(screen.getByRole('button', { name: 'Independent status' }));
    expect(props.open).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibraryTable {...props} selection={{ ...selection, disabled: true }} />);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Synthetic paper' }));
    expect(selection.toggle).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibraryTable {...props} />);
    expect(screen.getByRole('table').style.minWidth).toBe('776px');
    expect(screen.queryByRole('checkbox')).toBeNull();
  });

  it('shows unknown all-mode membership without a fake zero and delegates real status/clear/all actions', async () => {
    const props = toolbarProps();
    const rendered = await renderWithI18n(
      <LibrarySelectionToolbar {...props} selection={allMatchingLibrarySelection(['excluded'])} />,
      'en-US'
    );
    expect(screen.getByRole('toolbar', { name: 'Reference selection' })).toBeInTheDocument();
    expect(screen.getByText('All matching except 1')).toBeInTheDocument();
    expect(screen.queryByText('0 selected')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Select all matching references' })).toBeNull();
    fireEvent.change(screen.getByRole('combobox', { name: 'Reading status for selection' }), {
      target: { value: 'read' },
    });
    expect(props.onChangeStatus).toHaveBeenCalledWith('read');
    fireEvent.click(screen.getByRole('button', { name: 'Clear selection' }));
    expect(props.onClear).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibrarySelectionToolbar {...props} />);
    expect(screen.getByText('2 selected')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Select all matching references' }));
    expect(props.onSelectAllMatching).toHaveBeenCalledTimes(1);
    rendered.rerender(<LibrarySelectionToolbar {...props} resolving />);
    expect(screen.getByRole('toolbar')).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByText('Resolving selected references…')).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'unread' } });
    fireEvent.click(screen.getByRole('button', { name: 'Clear selection' }));
    expect(props.onChangeStatus).toHaveBeenCalledTimes(1);
    expect(props.onClear).toHaveBeenCalledTimes(1);
  });

  it('switches English/Chinese selection labels and uses the same truthful progress placeholders', async () => {
    const props = toolbarProps();
    const rendered = await renderWithI18n(<LibrarySelectionToolbar {...props} />, 'en-US');
    expect(rendered.i18n.t('conversation.librarySelection.activeProgress', { done: 1, total: 3 })).toBe(
      'Saving 1 of 3 references…'
    );
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('toolbar', { name: '文献选择' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '清除选择' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '所选文献的阅读状态' })).toBeInTheDocument();
    expect(rendered.i18n.t('conversation.librarySelection.uncertainProgress', { done: 1, total: 3 })).toBe(
      '已确认 1 / 3 项，当前批次的结果仍需确认。'
    );
    rendered.rerender(<LibrarySelectionToolbar {...props} selection={allMatchingLibrarySelection()} />);
    expect(screen.getByText('已选择所有匹配文献')).toBeInTheDocument();
    expect(screen.queryByText('已选择 0 项')).toBeNull();
  });
});
