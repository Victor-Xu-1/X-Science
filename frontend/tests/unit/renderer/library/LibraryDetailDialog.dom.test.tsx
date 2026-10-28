import React from 'react';
import { act, cleanup, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, paper } from './libraryCollectionsFixtures';
import '@arco-design/web-react/lib/_util/react-19-adapter';

// Only instrument the PDF leaf; the real Browser/Pane/immutable target/focus owners remain.
vi.mock('@/renderer/pages/library/LibraryAttachmentPreview', () => ({
  LibraryAttachmentPreview: () => <button type='button'>PDF fixture action</button>,
}));
let navigate: ReturnType<typeof useNavigate>;
const release: Array<() => void> = [];
function Location() {
  navigate = useNavigate();
  return <output aria-label='Detail fixture location'>{useLocation().search}</output>;
}
async function mount(path = '/library') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path='/library'
          element={
            <>
              <LibraryPage />
              <Location />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
    'en-US'
  );
}
const withPdf = {
  ...paper(),
  attachments: [
    {
      id: 'attachment-a',
      kind: 'fullText',
      versions: [
        {
          id: 'version-a',
          attachment_id: 'attachment-a',
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
function http(intercept?: Parameters<typeof installCollectionHTTP>[0]['intercept']) {
  return installCollectionHTTP({
    collections: () => [],
    items: () => [withPdf, paper('b', 'Paper B')],
    detail: (id) => (id === 'a' ? withPdf : paper(id, `Paper ${id.toUpperCase()}`)),
    intercept,
  });
}
async function openA() {
  const row = await screen.findByRole('button', { name: /^Paper A$/ });
  act(() => row.focus());
  fireEvent.click(row);
  return { row, dialog: await screen.findByRole('dialog', { name: 'Paper A' }) };
}
describe('Library detail through its real Page/read/mutation/modal owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('detail-dialog-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const done of release.splice(0)) done();
      await Promise.resolve();
    });
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('opens a single responsive detail dialog outside the list and returns focus to its actual row', async () => {
    const data = http();
    await mount();
    const { row, dialog } = await openA();
    expect(dialog).toHaveStyle({ width: '760px', maxWidth: 'calc(100vw - 32px)' });
    const mask = document.querySelector<HTMLElement>('.arco-modal-mask')!;
    expect(mask.style.backgroundColor).toBe('transparent');
    expect(getComputedStyle(mask).backgroundColor).toBe('rgba(0, 0, 0, 0)');
    expect(document.querySelector('.library-workbench__panes > aside')).toBeNull();
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(row).toHaveFocus());
    expect(screen.getByLabelText('Detail fixture location')).not.toHaveTextContent('item=');
    expect(data.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it('closes a loading opening without allowing its late result to reopen or replace the next detail', async () => {
    const held = deferred<Response>();
    release.push(() => held.resolve(json(withPdf)));
    const data = http((url) => (url.pathname === '/api/literature/items/a' ? held.promise : undefined));
    await mount();
    const row = await screen.findByRole('button', { name: /^Paper A$/ });
    act(() => row.focus());
    fireEvent.click(row);
    const loading = await screen.findByRole('dialog', { name: 'Literature details' });
    fireEvent.click(within(loading).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: /^Paper B$/ }));
    await screen.findByRole('dialog', { name: 'Paper B' });
    await act(async () => held.resolve(json(withPdf)));
    expect(screen.getByRole('dialog', { name: 'Paper B' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Paper A' })).toBeNull();
    expect(data.requests.filter((request) => request.path === '/api/literature/items/a')).toHaveLength(1);
  });

  it.each(['item', 'account'] as const)(
    'ignores an old delayed mask close after the %s opening changes',
    async (scope) => {
      http();
      await mount();
      const { dialog } = await openA();
      const timers = vi.spyOn(globalThis, 'setTimeout');
      const maskTarget = dialog.closest('.arco-modal-wrapper')!;
      fireEvent.mouseDown(maskTarget);
      fireEvent.click(maskTarget);
      const delayed = [...timers.mock.calls].reverse().find((call) => call[1] === 100)?.[0];
      expect(delayed).toBeTypeOf('function');
      await act(async () => {
        if (scope === 'item') await navigate('/library?item=b');
        else setRendererAccountOwner('next-dialog-owner');
      });
      const replacement = await screen.findByRole('dialog', { name: scope === 'item' ? 'Paper B' : 'Paper A' });
      expect(replacement).not.toBe(dialog);
      act(() => (delayed as () => void)());
      expect(replacement).toBeInTheDocument();
    }
  );

  it('keeps failure and retry in the same URL-owned opening and returns to the list when no row exists', async () => {
    let fail = true;
    http((url) =>
      url.pathname === '/api/literature/items/c' && fail ? json({ code: 'LITERATURE_INTERNAL' }, 503) : undefined
    );
    await mount('/library?item=c');
    const failed = await screen.findByRole('dialog', { name: 'Literature details' });
    await within(failed).findByRole('alert');
    fail = false;
    fireEvent.click(within(failed).getByRole('button', { name: 'Retry' }));
    const ready = await screen.findByRole('dialog', { name: 'Paper C' });
    expect(ready).toBe(failed);
    fireEvent.click(within(ready).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.getByRole('region', { name: 'Library papers' })).toHaveFocus();
  });

  it('preserves composing/handled Escape and lets a native select dismiss before closing its parent', async () => {
    http();
    await mount();
    const { dialog } = await openA();
    fireEvent.keyDown(dialog, { key: 'Escape', isComposing: true });
    expect(dialog).toBeInTheDocument();
    const handled = createEvent.keyDown(dialog, { key: 'Escape', cancelable: true });
    handled.preventDefault();
    fireEvent(dialog, handled);
    expect(dialog).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const referenceType = within(dialog).getByRole('combobox', { name: 'Reference type' });
    fireEvent.pointerDown(referenceType);
    fireEvent.keyDown(referenceType, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    expect(referenceType).toBeInTheDocument();
    fireEvent.click(
      within(within(dialog).getByRole('region', { name: 'Edit details' })).getByRole('button', { name: /^Close$/ })
    );
    await waitFor(() => expect(within(dialog).queryByRole('combobox', { name: 'Reference type' })).toBeNull());
    const select = within(dialog).getByRole('combobox', { name: /Reading status/ });
    fireEvent.pointerDown(select);
    fireEvent.keyDown(select, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    fireEvent.keyDown(select, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('preserves a durable pending edit when the UI closes and does not reopen on its late ACK', async () => {
    const held = deferred<Response>();
    let acknowledge: (() => void) | undefined;
    http((url, init) => {
      if (init.method !== 'PATCH' || url.pathname !== '/api/literature/items/a') return undefined;
      const body = JSON.parse(String(init.body)) as { operation_id: string; title: string };
      acknowledge = () =>
        held.resolve(
          json({
            schema: 'x-science.literature-receipt.v1',
            operation_id: body.operation_id,
            item: { ...withPdf, title: body.title, metadata_revision: 2 },
          })
        );
      release.push(acknowledge);
      return held.promise;
    });
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Title' }), { target: { value: 'Saved later' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(acknowledge).toBeTypeOf('function'));
    const controller = getLiteratureMutationController(getRendererAccountOwnerId());
    const pending = controller.getSnapshot().pending;
    expect(pending).toBeDefined();
    fireEvent.click(
      within(dialog).getByRole('group', { name: 'Literature details' }).querySelector('button[aria-label="Close"]')!
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(controller.getSnapshot().pending?.operation_id).toBe(pending?.operation_id);
    await act(async () => acknowledge!());
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps related lookup and inline PDF preview inside the dialog; preview Escape closes only the child', async () => {
    http((url, init) =>
      url.pathname === '/api/literature/related'
        ? json({
            schema: 'x-science.literature-related.v1',
            item_id: 'a',
            metadata_revision: 1,
            direction: JSON.parse(String(init.body)).direction,
            state: 'unavailable',
            records: [],
            issue: 'no-match',
          })
        : undefined
    );
    await mount();
    const { row, dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'References' }));
    const related = await within(dialog).findByRole('region', { name: 'Related literature' });
    await within(related).findByText(/Could not identify this paper/);
    fireEvent.click(within(related).getByRole('button', { name: 'Close' }));
    const preview = within(dialog).getByRole('button', { name: /Preview PDF/ });
    fireEvent.click(preview);
    const child = await within(dialog).findByRole('region', { name: 'Attachment preview' });
    expect(within(child).getByRole('button', { name: 'PDF fixture action' })).toBeInTheDocument();
    fireEvent.keyDown(child, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).queryByRole('region', { name: 'Attachment preview' })).toBeNull();
    expect(preview).toHaveFocus();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(row).toHaveFocus();
  });
});
