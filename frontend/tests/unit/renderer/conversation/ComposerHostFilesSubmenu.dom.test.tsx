import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../i18nTestUtils';

vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));

const grant = {
  id: '/fixture/root',
  hostPath: '/fixture/root',
  guestPath: '/fixture/root',
  mountName: 'Fixture',
  mode: 'ro',
};
const response = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetRendererAccountScopeForTest();
});

async function renderFiles(onLocalFilesAdded = vi.fn()) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/preferences/host-grants') return response({ grants: [grant] });
    if (url.pathname === '/api/preferences/host-browse')
      return response(
        url.searchParams.get('path') === grant.hostPath
          ? [
              { name: 'nested', isDirectory: true },
              { name: 'sample.csv', isDirectory: false },
            ]
          : [{ name: 'nested.csv', isDirectory: false }]
      );
    throw new Error(`unexpected fixture route: ${url.pathname}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  await renderWithI18n(<FileAttachButton openFileSelector={vi.fn()} onLocalFilesAdded={onLocalFilesAdded} />, 'en-US', {
    wrapper: MemoryRouter,
  });
  return { fetchMock, onLocalFilesAdded };
}

async function openFiles() {
  fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'Your files' }));
  return screen.findByRole('menu', { name: 'Your files' });
}

describe('Your files nested submenu', () => {
  it('opens a 300px submenu, adds only from the trailing action, and resets checks on close without rereading folders', async () => {
    const { fetchMock, onLocalFilesAdded } = await renderFiles();
    expect(fetchMock).not.toHaveBeenCalled();
    const menu = await openFiles();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(menu).toHaveClass('conversation-control-submenu');
    expect(menu).toHaveStyle({ width: 'min(300px, calc(100vw - 24px))', maxHeight: 'min(340px, calc(100vh - 24px))' });
    expect(screen.getByRole('menu', { name: 'Add to message' })).toBeVisible();
    fireEvent.click(await within(menu).findByRole('button', { name: 'Expand Fixture' }));
    const filename = await within(menu).findByText('sample.csv');
    expect(filename.closest('button')).toBeNull();
    fireEvent.click(filename);
    expect(onLocalFilesAdded).not.toHaveBeenCalled();
    fireEvent.click(within(menu).getByRole('button', { name: 'Reference sample.csv' }));
    expect(onLocalFilesAdded).toHaveBeenCalledTimes(1);
    expect(within(menu).getByRole('button', { name: 'Referenced sample.csv' })).toBeDisabled();
    expect(menu).toBeVisible();
    fireEvent.click(within(menu).getByRole('button', { name: 'Collapse Fixture' }));
    fireEvent.click(within(menu).getByRole('button', { name: 'Expand Fixture' }));
    expect(fetchMock).toHaveBeenCalledTimes(2);
    fireEvent.keyDown(menu, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Your files' })).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('menuitem', { name: 'Your files' }));
    const reopened = await screen.findByRole('menu', { name: 'Your files' });
    expect(within(reopened).getByRole('button', { name: 'Reference sample.csv' })).toBeEnabled();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('supports keyboard submenu navigation and caches nested directory listings across collapse', async () => {
    const user = userEvent.setup();
    const { fetchMock, onLocalFilesAdded } = await renderFiles();
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    const trigger = screen.getByRole('menuitem', { name: 'Your files' });
    trigger.focus();
    await user.keyboard('{ArrowRight}');
    const menu = await screen.findByRole('menu', { name: 'Your files' });
    await waitFor(() => expect(within(menu).getByRole('button', { name: 'Grant folder…' })).toHaveFocus());
    const root = await within(menu).findByRole('button', { name: 'Expand Fixture' });
    root.focus();
    await user.keyboard('{ArrowRight}');
    const nested = await within(menu).findByRole('button', { name: 'Expand nested' });
    nested.focus();
    await user.keyboard('{Enter}');
    const add = await within(menu).findByRole('button', { name: 'Reference nested.csv' });
    add.focus();
    await user.keyboard(' ');
    expect(onLocalFilesAdded).toHaveBeenCalledTimes(1);
    const collapse = within(menu).getByRole('button', { name: 'Collapse nested' });
    collapse.focus();
    await user.keyboard('{ArrowLeft}{Enter}');
    expect(await within(menu).findByText('nested.csv')).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(3);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(screen.getByRole('menu', { name: 'Add to message' })).toBeVisible();
  });

  it('closes the popup immediately on account reset and ignores a late previous-owner directory response', async () => {
    setRendererAccountOwner('first-owner');
    let resolveListing!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      resolveListing = resolve;
    });
    const { fetchMock, onLocalFilesAdded } = await renderFiles();
    fetchMock.mockImplementation(async (input: RequestInfo | URL) =>
      new URL(String(input), 'http://localhost').pathname === '/api/preferences/host-browse'
        ? pending
        : response({ grants: [grant] })
    );
    const menu = await openFiles();
    const outerMenu = screen.getByRole('menu', { name: 'Add to message' });
    fireEvent.click(await within(menu).findByRole('button', { name: 'Expand Fixture' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    act(() => {
      setRendererAccountOwner('second-owner');
    });
    await waitFor(() => {
      // The old portaled nodes must be removed, not merely hidden by exit animation.
      expect(menu).not.toBeInTheDocument();
      expect(outerMenu).not.toBeInTheDocument();
      expect(screen.queryByRole('menu', { name: 'Your files' })).not.toBeInTheDocument();
    });
    expect(screen.queryByRole('menu', { name: 'Add to message' })).not.toBeInTheDocument();
    await act(async () => {
      resolveListing(response([{ name: 'old-owner.txt', isDirectory: false }]));
      await pending;
    });
    expect(screen.queryByText('old-owner.txt')).not.toBeInTheDocument();
    expect(onLocalFilesAdded).not.toHaveBeenCalled();
  });
});
