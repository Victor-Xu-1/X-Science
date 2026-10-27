import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

const preview = vi.hoisted(() => ({ mounts: 0, unmounts: 0 }));
// Instrument only shared preview lifetime/page state; this does not render or validate a PDF.
vi.mock('@/renderer/pages/library/LibraryAttachmentPreview', () => ({
  LibraryAttachmentPreview: () => {
    const [page, setPage] = React.useState(1);
    React.useEffect(() => {
      ++preview.mounts;
      return () => {
        ++preview.unmounts;
      };
    }, []);
    return (
      <div data-testid='header-pdf'>
        <output aria-label='Header fixture PDF page'>{page}</output>
        <button type='button' onClick={() => setPage(2)}>
          Advance header fixture PDF
        </button>
      </div>
    );
  },
}));

const ownerA = 'library-detail-header-owner-a';
const ownerB = 'library-detail-header-owner-b';
const release: Array<() => void> = [];
const scrollIntoView = vi.fn();
const originalScroll = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView');
const name = (id: string, owner = ownerA) =>
  `${owner === ownerA ? 'Paper' : 'Other account paper'} ${id.toUpperCase()}`;
function headerHTTP(holdFirst = false) {
  const stores = new Map<string, Map<string, LiteratureItem>>();
  for (const owner of [ownerA, ownerB]) {
    stores.set(
      owner,
      new Map<string, LiteratureItem>(
        ['a', 'b'].map((id): [string, LiteratureItem] => [
          id,
          {
            ...paper(id, name(id, owner)),
            attachments:
              id === 'a'
                ? [
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
                  ]
                : [],
          },
        ])
      )
    );
  }
  const current = () => stores.get(getRendererAccountOwnerId())!;
  const writes: Array<{ owner: string; item: string; operation: string }> = [];
  let releaseFirst: (() => void) | undefined;
  let heldReadback:
    | { owner: string; item: string; reply: ReturnType<typeof deferred<Response>>; started: boolean }
    | undefined;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [...current().values()],
    detail: (id) => current().get(id)!,
    intercept: (url, init) => {
      const match = url.pathname.match(/^\/api\/literature\/items\/([^/]+)$/);
      if (
        (init.method ?? 'GET') === 'GET' &&
        match &&
        heldReadback?.owner === getRendererAccountOwnerId() &&
        heldReadback.item === decodeURIComponent(match[1])
      ) {
        heldReadback.started = true;
        return heldReadback.reply.promise;
      }
      if (init.method !== 'PATCH' || !match) return undefined;
      const body = JSON.parse(String(init.body)) as {
        operation_id: string;
        expected_revision: number;
        title: string;
      };
      const id = decodeURIComponent(match[1]);
      const store = current();
      const before = store.get(id)!;
      expect(body.expected_revision).toBe(before.metadata_revision);
      const saved = { ...before, title: body.title, metadata_revision: before.metadata_revision + 1 };
      store.set(id, saved);
      writes.push({ owner: getRendererAccountOwnerId(), item: id, operation: body.operation_id });
      const receipt = { schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item: saved };
      if (holdFirst && writes.length === 1) {
        const held = deferred<Response>();
        releaseFirst = () => held.resolve(json(receipt));
        release.push(releaseFirst);
        return held.promise;
      }
      return json(receipt);
    },
  });
  return {
    http,
    writes,
    releaseFirst: () => releaseFirst!(),
    holdReadback: (id: string) => {
      const store = current();
      const held = { owner: getRendererAccountOwnerId(), item: id, reply: deferred<Response>(), started: false };
      heldReadback = held;
      const deliver = () => {
        if (heldReadback === held) heldReadback = undefined;
        held.reply.resolve(json(store.get(id)!));
      };
      release.push(deliver);
      return { started: () => held.started, deliver };
    },
  };
}

const header = () => screen.getByRole('group', { name: 'Literature details' });
const edit = () => within(header()).getByRole('button', { name: 'Edit details' });
const editor = () => screen.getByRole('region', { name: 'Edit details' });

describe('Library primary detail header through the real Page consumers', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner(ownerA);
    localStorage.clear();
    installLiteratureLocks();
    preview.mounts = 0;
    preview.unmounts = 0;
    scrollIntoView.mockClear();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView });
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const done of release.splice(0)) done();
      setRendererAccountOwner(null);
    });
    vi.unstubAllGlobals();
    if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll);
    else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
  });

  it('keeps the same draft and PDF while explicitly revealing the header editor and cancelling', async () => {
    const data = headerHTTP();
    await mountCollections('/library?item=a');
    await screen.findByRole('heading', { name: name('a') });
    await screen.findByRole('group', { name: 'Literature details' });
    fireEvent.click(screen.getByRole('button', { name: /Preview PDF/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Advance header fixture PDF' }));
    const pdf = screen.getByTestId('header-pdf');
    const trigger = edit();
    fireEvent.click(trigger);
    const panel = editor();
    const title = within(panel).getByRole('textbox', { name: 'Title' });
    await waitFor(() => expect(title).toHaveFocus());
    fireEvent.change(title, { target: { value: 'Keep this exact draft' } });
    const refresh = screen.getByRole('button', { name: 'Refresh' });
    const detailReads = () =>
      data.http.requests.filter((request) => request.method === 'GET' && request.path === '/api/literature/items/a')
        .length;
    const beforeRefresh = detailReads();
    refresh.focus();
    fireEvent.click(refresh);
    await waitFor(() => expect(detailReads()).toBe(beforeRefresh + 1));
    await waitFor(() => expect(title).toBeEnabled());
    expect(refresh).toHaveFocus();
    fireEvent.click(trigger);
    expect(editor()).toBe(panel);
    expect(title).toHaveFocus();
    expect(scrollIntoView.mock.contexts.at(-1)).toBe(panel);
    expect(within(panel).getByRole('textbox', { name: 'Title' })).toBe(title);
    expect(title).toHaveValue('Keep this exact draft');
    expect(screen.getByTestId('header-pdf')).toBe(pdf);
    expect(screen.getByLabelText('Header fixture PDF page')).toHaveTextContent('2');
    expect(preview).toEqual({ mounts: 1, unmounts: 0 });
    fireEvent.click(within(panel).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
    expect(trigger).toHaveFocus();
    expect(screen.getByTestId('header-pdf')).toBe(pdf);
    expect(data.writes).toEqual([]);

    const readback = data.holdReadback('a');
    fireEvent.click(trigger);
    const savedPanel = editor();
    fireEvent.change(within(savedPanel).getByRole('textbox', { name: 'Title' }), {
      target: { value: 'Saved without stealing PDF focus' },
    });
    fireEvent.click(within(savedPanel).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(readback.started()).toBe(true));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
    expect(trigger).toBeDisabled();
    const pdfAction = screen.getByRole('button', { name: 'Advance header fixture PDF' });
    pdfAction.focus();
    await act(async () => readback.deliver());
    await waitFor(() => expect(trigger).toBeEnabled());
    expect(pdfAction).toHaveFocus();
    expect(screen.getByTestId('header-pdf')).toBe(pdf);
    expect(screen.getByLabelText('Header fixture PDF page')).toHaveTextContent('2');
  });

  it.each(['item', 'account'] as const)(
    'does not transfer old edit completion focus across an %s switch and can save from the new header',
    async (change) => {
      const data = headerHTTP(true);
      await mountCollections('/library?item=a');
      await screen.findByRole('heading', { name: name('a') });
      const oldTrigger = edit();
      fireEvent.click(oldTrigger);
      const oldPanel = editor();
      fireEvent.change(within(oldPanel).getByRole('textbox', { name: 'Title' }), {
        target: { value: 'Old submitted draft' },
      });
      fireEvent.click(within(oldPanel).getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(data.writes).toHaveLength(1));
      let nextTitle: HTMLElement | undefined;
      if (change === 'account') {
        act(() => setRendererAccountOwner(ownerB));
        await screen.findByRole('heading', { name: name('a', ownerB) });
        fireEvent.click(edit());
        nextTitle = within(editor()).getByRole('textbox', { name: 'Title' });
        fireEvent.change(nextTitle, { target: { value: 'Current draft' } });
        nextTitle.focus();
      } else {
        fireEvent.click(screen.getByRole('button', { name: name('b') }));
        await screen.findByRole('heading', { name: name('b') });
        const close = within(header()).getByRole('button', { name: 'Close' });
        close.focus();
        expect(edit()).toBeDisabled();
      }
      const currentTrigger = edit();
      await act(async () => data.releaseFirst());
      expect(oldTrigger.isConnected).toBe(false);
      if (change === 'account') {
        expect(within(editor()).getByRole('textbox', { name: 'Title' })).toBe(nextTitle!);
        expect(nextTitle!).toHaveValue('Current draft');
        expect(nextTitle!).toHaveFocus();
      } else {
        await waitFor(() => expect(currentTrigger).toBeEnabled());
        expect(within(header()).getByRole('button', { name: 'Close' })).toHaveFocus();
        expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull();
        fireEvent.click(currentTrigger);
        nextTitle = within(editor()).getByRole('textbox', { name: 'Title' });
        fireEvent.change(nextTitle, { target: { value: 'Current draft' } });
      }
      fireEvent.click(within(editor()).getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(data.writes).toHaveLength(2));
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
      await waitFor(() => expect(currentTrigger).toHaveFocus());
      expect(data.writes[1].owner).toBe(change === 'account' ? ownerB : ownerA);
      expect(data.writes[1].item).toBe(change === 'account' ? 'a' : 'b');
    }
  );
});
