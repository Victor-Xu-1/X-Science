import React, { StrictMode } from 'react';
import { act, fireEvent, renderHook, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_LIBRARY_COLUMN_PREFERENCES as defaults } from '@/common/adapter/libraryColumnPreferences';
import { LibraryColumnMenu } from '@/renderer/pages/library/LibraryColumnMenu';
import { renderWithI18n } from '../i18nTestUtils';
import '@arco-design/web-react/lib/_util/react-19-adapter';

const mocks = vi.hoisted(() => ({ account: 'account:a', load: vi.fn(), save: vi.fn() }));
vi.mock('@/renderer/services/rendererAccountScope', () => ({
  getRendererAccountScopeToken: () => mocks.account,
}));
vi.mock('@/renderer/services/runtime/libraryColumnPreferences', () => ({
  loadLibraryColumnPreferences: mocks.load,
  saveLibraryColumnPreferences: mocks.save,
}));
import { useLibraryColumnPreferences } from '@/renderer/pages/library/useLibraryColumnPreferences';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((yes) => {
    resolve = yes;
  });
  return { promise, resolve };
}

describe('Library column preference mounted view', () => {
  beforeEach(() => {
    mocks.account = 'account:a';
    mocks.load.mockReset();
    mocks.save.mockReset();
  });
  it('projects an existing V2 into the original five-column menu without an empty Notes control or write', async () => {
    mocks.load.mockResolvedValue({
      version: 2,
      order: ['readingStatus', 'notes', 'year', 'authors', 'doi', 'attachments'],
      visible: ['notes', 'year'],
    });
    function ExistingMenu() {
      const view = useLibraryColumnPreferences(mocks.account);
      return (
        <LibraryColumnMenu
          preferences={view.preferences}
          status={view.status}
          onPreferencesChange={view.changePreferences}
          onRetry={view.retry}
          onReset={view.reset}
        />
      );
    }
    const mounted = await renderWithI18n(<ExistingMenu />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    const menu = await screen.findByRole('dialog');
    await waitFor(() =>
      expect(
        within(menu)
          .getAllByRole('checkbox')
          .every((element) => !(element as HTMLInputElement).disabled)
      ).toBe(true)
    );
    expect(within(menu).getAllByRole('checkbox')).toHaveLength(5);
    expect(within(menu).queryByText(/notes/i)).toBeNull();
    expect(menu.querySelector('[data-column="notes"]')).toBeNull();
    expect(mocks.save).not.toHaveBeenCalled();
    mounted.unmount();
  });
  it('survives StrictMode cleanup and isolates account changes, activation reads and unmount cancellation', async () => {
    const requests: { signal: AbortSignal; pending: ReturnType<typeof deferred<unknown>> }[] = [];
    mocks.load.mockImplementation((signal: AbortSignal) => {
      const pending = deferred<unknown>();
      requests.push({ signal, pending });
      return pending.promise;
    });
    const { result, rerender, unmount } = renderHook(({ account }) => useLibraryColumnPreferences(account), {
      initialProps: { account: mocks.account },
      wrapper: StrictMode,
    });
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[0].signal.aborted).toBe(true);
    await act(async () => {
      requests[0].pending.resolve({ ...defaults, visible: [] });
      requests[1].pending.resolve(defaults);
    });
    expect(result.current.status).toBe('ready');
    expect(result.current.preferences).toEqual(defaults);
    mocks.account = 'account:b';
    rerender({ account: mocks.account });
    await waitFor(() => expect(requests).toHaveLength(3));
    expect(result.current.status).toBe('loading');
    await act(async () => requests[2].pending.resolve({ ...defaults, visible: ['doi'] }));
    expect(result.current.preferences.visible).toEqual(['doi']);
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    act(() => window.dispatchEvent(new Event('focus')));
    await waitFor(() => expect(requests).toHaveLength(4));
    await act(async () => requests[3].pending.resolve({ ...defaults, visible: ['year'] }));
    expect(result.current.preferences.visible).toEqual(['year']);
    act(() => window.dispatchEvent(new Event('focus')));
    await waitFor(() => expect(requests).toHaveLength(5));
    unmount();
    expect(requests[4].signal.aborted).toBe(true);
    await act(async () => requests[4].pending.resolve(defaults));
    expect(mocks.save).not.toHaveBeenCalled();
  });
});
