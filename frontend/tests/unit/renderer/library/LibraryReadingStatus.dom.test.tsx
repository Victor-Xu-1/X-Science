import React from 'react';
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { literatureReadingStatuses } from '@/common/adapter/literatureReadingStatus';
import { LibraryReadingStatus, LibraryReadingStatusLabel } from '@/renderer/pages/library/LibraryReadingStatus';
import { renderWithI18n } from '../../i18nTestUtils';

afterEach(cleanup);

describe('Library reading-status presentation', () => {
  it.each([
    { language: 'en-US' as const, labels: ['Unread', 'Reading', 'Read'] },
    { language: 'zh-CN' as const, labels: ['未读', '在读', '已读'] },
  ])(
    'uses the same explicit status vocabulary in labels and native options in $language',
    async ({ language, labels }) => {
      const onChange = vi.fn();
      const view = await renderWithI18n(<LibraryReadingStatusLabel value='unread' />, language);
      for (const [index, value] of literatureReadingStatuses.entries()) {
        view.rerender(<LibraryReadingStatusLabel value={value} />);
        expect(screen.getByText(labels[index])).toBeVisible();
        expect(screen.queryByRole('button')).toBeNull();
        view.rerender(
          <LibraryReadingStatus
            value={value}
            disabled={false}
            onChange={onChange}
            accessibleLabel='Status for reference A'
          />
        );
        const select = screen.getByRole('combobox', { name: 'Status for reference A' });
        expect(select).toHaveValue(value);
        expect(
          within(select)
            .getAllByRole('option')
            .map((option) => option.textContent)
        ).toEqual(labels);
      }
      expect(onChange).not.toHaveBeenCalled();
    }
  );

  it('emits only the selected enum and waits for the controlled value instead of saving or changing it locally', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const view = await renderWithI18n(
      <LibraryReadingStatus value='unread' disabled={false} onChange={onChange} accessibleLabel='Reading status' />,
      'en-US'
    );
    const select = screen.getByRole('combobox', { name: 'Reading status' });
    await user.selectOptions(select, 'reading');
    expect(onChange.mock.calls).toEqual([['reading']]);
    expect(select).toHaveValue('unread');
    view.rerender(
      <LibraryReadingStatus value='reading' disabled={false} onChange={onChange} accessibleLabel='Reading status' />
    );
    expect(select).toHaveValue('reading');
    fireEvent.change(select, { target: { value: 'reading' } });
    expect(onChange.mock.calls).toEqual([['reading']]);
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('keeps a disabled selector out of the tab order and restores native focus when enabled', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const content = (disabled: boolean) => (
      <>
        <button type='button'>Before</button>
        <LibraryReadingStatus
          value='unread'
          disabled={disabled}
          onChange={onChange}
          accessibleLabel='Reading status for reference B'
        />
        <button type='button'>After</button>
      </>
    );
    const view = await renderWithI18n(content(true), 'en-US');
    const select = screen.getByRole('combobox', { name: 'Reading status for reference B' });
    expect(select).toBeDisabled();
    await user.tab();
    expect(screen.getByRole('button', { name: 'Before' })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('button', { name: 'After' })).toHaveFocus();
    await user.selectOptions(select, 'read');
    expect(onChange).not.toHaveBeenCalled();
    view.rerender(content(false));
    await user.tab({ shift: true });
    expect(select).toHaveFocus();
    await user.selectOptions(select, 'read');
    expect(onChange.mock.calls).toEqual([['read']]);
    expect(select.closest('button')).toBeNull();
  });
});
