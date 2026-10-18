import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../i18nTestUtils';

let conversation = { conversation_id: 'first-conversation' };
vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
vi.mock('@/renderer/hooks/context/ConversationContext', () => ({ useConversationContextSafe: () => conversation }));

const grant = {
  id: '/fixture/root',
  hostPath: '/fixture/root',
  guestPath: '/fixture/root',
  mountName: 'Fixture',
  mode: 'rw',
};
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  conversation = { conversation_id: 'first-conversation' };
});

async function openFiles() {
  fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'Your files' }));
  return screen.findByRole('menu', { name: 'Your files' });
}

describe('composer host access actions', () => {
  it('requires an explicit canonical operator grant and keeps a 403 visible without a fallback', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), 'http://localhost').pathname;
      if ((init?.method ?? 'GET') === 'GET') return response({ grants: [] });
      expect(path).toBe('/api/go/preferences/host-grants');
      return response({ code: 'HOST_OPERATOR_REQUIRED', error: 'fixture permission boundary' }, 403);
    });
    vi.stubGlobal('fetch', fetchMock);
    const selected = vi.fn();
    await renderWithI18n(
      <FileAttachButton xScienceV11 openFileSelector={vi.fn()} onLocalFilesAdded={selected} />,
      'en-US',
      { wrapper: MemoryRouter }
    );
    const dialog = await openFiles();
    await screen.findByText('No granted folders.');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Grant folder…' }));
    expect(screen.getByRole('dialog', { name: 'Grant folder access' })).toBeVisible();
    const action = screen.getByRole('region', { name: 'Grant folder access' });
    expect(within(action).getByText(/can stop this account's active tools/)).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    fireEvent.change(within(action).getByRole('textbox', { name: 'Host folder path' }), {
      target: { value: '/fixture/new-folder' },
    });
    fireEvent.click(within(action).getByRole('button', { name: 'Grant folder access' }));
    expect(
      await within(screen.getByRole('dialog', { name: 'Grant folder access' })).findByText(
        'The installation operator must approve this folder access change.'
      )
    ).toBeVisible();
    expect(selected).not.toHaveBeenCalled();
    const writes = fetchMock.mock.calls.filter(([, init]) => init?.method && init.method !== 'GET');
    expect(writes).toHaveLength(1);
    expect(JSON.parse(writes[0][1]!.body as string)).toEqual({ path: '/fixture/new-folder', mode: 'read' });
  });

  it('revokes once from the root trailing action without closing the menu or deleting files', async () => {
    let revoked = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), 'http://localhost').pathname;
      expect(path).toBe('/api/preferences/host-grants');
      if (init?.method === 'DELETE') {
        revoked = true;
        return response({});
      }
      return response({ grants: revoked ? [] : [grant] });
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(
      <FileAttachButton xScienceV11 openFileSelector={vi.fn()} onLocalFilesAdded={vi.fn()} />,
      'en-US',
      { wrapper: MemoryRouter }
    );
    await openFiles();
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke access to Fixture' }));
    expect(await screen.findByText('No granted folders.')).toBeVisible();
    expect(screen.getByRole('menu', { name: 'Your files' })).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'DELETE')).toHaveLength(1);
    expect(fetchMock.mock.calls.every(([input]) => !String(input).includes('/api/fs/'))).toBe(true);
  });

  it('does not restore an old submenu or its late listing after conversation A to B to A', async () => {
    let resolveListing!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      resolveListing = resolve;
    });
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), 'http://localhost').pathname;
      return path === '/api/preferences/host-browse' ? pending : response({ grants: [grant] });
    });
    vi.stubGlobal('fetch', fetchMock);
    const selected = vi.fn();
    const element = () => <FileAttachButton xScienceV11 openFileSelector={vi.fn()} onLocalFilesAdded={selected} />;
    const view = await renderWithI18n(element(), 'en-US', { wrapper: MemoryRouter });
    await openFiles();
    fireEvent.click(await screen.findByRole('button', { name: 'Expand Fixture' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    conversation = { conversation_id: 'second-conversation' };
    view.rerender(element());
    conversation = { conversation_id: 'first-conversation' };
    view.rerender(element());
    await act(async () => {
      resolveListing(response([{ name: 'late.txt', isDirectory: false }]));
      await pending;
    });
    expect(screen.queryByRole('menu', { name: 'Your files' })).not.toBeInTheDocument();
    expect(screen.queryByText('late.txt')).not.toBeInTheDocument();
    expect(selected).not.toHaveBeenCalled();
  });
});
