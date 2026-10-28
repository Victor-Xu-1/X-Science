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
const transfer = vi.hoisted(() => ({
  signal: undefined as AbortSignal | undefined,
  complete: undefined as (() => void) | undefined,
}));
// Keep the original Browser's request and cancellation owners, replacing only the byte transfer.
vi.mock('@/renderer/utils/file/download', () => ({
  downloadFileFromUrl: (_url: string, _name: string, options: { signal: AbortSignal }) =>
    new Promise<void>((resolve) => {
      transfer.signal = options.signal;
      transfer.complete = resolve;
    }),
}));
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
    transfer.signal = undefined;
    transfer.complete = undefined;
    scrollIntoView.mockClear();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView });
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      transfer.complete?.();
      for (const done of release.splice(0)) done();
      setRendererAccountOwner(null);
    });
    vi.unstubAllGlobals();
    if (originalScroll) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScroll);
    else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
  });

  it('edit mode: replaces the overview, confirms Back and retains the same PDF through discard and Save', async () => {
    const data = headerHTTP();
    await mountCollections('/library?item=a');
    await screen.findByRole('heading', { name: name('a') });
    const primary = await screen.findByRole('group', { name: 'Literature details' });
    const measureHeader = vi.spyOn(primary, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 320, 56));
    fireEvent.click(screen.getByRole('button', { name: /Preview PDF/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Advance header fixture PDF' }));
    fireEvent.click(screen.getByRole('button', { name: 'Download · reference.pdf' }));
    expect(transfer.signal?.aborted).toBe(false);
    const pdf = screen.getByTestId('header-pdf');
    const trigger = edit();
    fireEvent.click(trigger);
    const panel = editor();
    const title = within(panel).getByRole('textbox', { name: 'Title' });
    await waitFor(() => expect(title).toHaveFocus());
    expect(within(header()).getByRole('heading', { name: 'Edit details' })).toBeVisible();
    expect(within(header()).queryByRole('button', { name: 'Edit details' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Preview PDF/ })).toBeNull();
    expect(pdf).not.toBeVisible();
    expect(pdf.closest('[hidden][inert]')).not.toBeNull();
    expect(transfer.signal?.aborted).toBe(false);
    expect(scrollIntoView.mock.contexts.at(-1)).toBe(title);
    expect(title.style.scrollMarginBlockStart).toBe('calc(56px + var(--ui-space-6))');
    fireEvent.change(title, { target: { value: 'Keep this exact draft' } });
    const refresh = screen.getByRole('button', { name: 'Refresh' });
    const detailReads = () =>
      data.http.requests.filter((request) => request.method === 'GET' && request.path === '/api/literature/items/a')
        .length;
    const beforeRefresh = detailReads();
    // Exercise the existing refresh owner while the modal correctly keeps focus
    // inside the current draft; the covered background toolbar is not a focus destination.
    title.focus();
    fireEvent.click(refresh);
    await waitFor(() => expect(detailReads()).toBe(beforeRefresh + 1));
    await waitFor(() => expect(title).toBeEnabled());
    expect(title).toHaveFocus();
    const back = within(header()).getByRole('button', { name: 'Back' });
    act(() => back.focus());
    fireEvent.click(back);
    const keep = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    fireEvent.click(within(keep).getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(editor()).toBe(panel);
    await waitFor(() => expect(title).toHaveFocus());
    expect(within(panel).getByRole('textbox', { name: 'Title' })).toBe(title);
    expect(title).toHaveValue('Keep this exact draft');
    expect(screen.getByTestId('header-pdf')).toBe(pdf);
    expect(screen.getByLabelText('Header fixture PDF page')).toHaveTextContent('2');
    expect(preview).toEqual({ mounts: 1, unmounts: 0 });
    act(() => back.focus());
    fireEvent.click(back);
    const discard = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    expect(title).toHaveValue('Keep this exact draft');
    expect(screen.getByTestId('header-pdf')).toBe(pdf);
    expect(data.writes).toEqual([]);
    fireEvent.click(within(discard).getByRole('button', { name: 'Discard changes' }));
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(within(header()).getByRole('heading', { name: name('a') })).toBeVisible();
    expect(within(header()).queryByRole('button', { name: 'Back' })).toBeNull();
    expect(screen.getByTestId('header-pdf')).toBe(pdf);
    expect(pdf).toBeVisible();
    expect(transfer.signal?.aborted).toBe(false);
    await act(async () => transfer.complete!());
    await screen.findByText('Download started · reference.pdf');
    expect(data.writes).toEqual([]);

    fireEvent.click(trigger);
    const cleanPanel = editor();
    const cancel = within(cleanPanel).getByRole('button', { name: 'Close' });
    act(() => cancel.focus());
    fireEvent.click(cancel);
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(screen.queryByRole('alertdialog')).toBeNull();
    fireEvent.click(trigger);
    const cleanBack = within(header()).getByRole('button', { name: 'Back' });
    act(() => cleanBack.focus());
    fireEvent.click(cleanBack);
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
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

  it('does not reclaim focus when the user chooses the header Close control before a saved ACK', async () => {
    const data = headerHTTP(true);
    await mountCollections('/library?item=a');
    await screen.findByRole('heading', { name: name('a') });
    fireEvent.click(edit());
    fireEvent.change(within(editor()).getByRole('textbox', { name: 'Title' }), {
      target: { value: 'Saved without claiming focus' },
    });
    fireEvent.click(within(editor()).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(data.writes).toHaveLength(1));
    const destination = within(header()).getByRole('button', { name: 'Close' });
    act(() => destination.focus());
    await act(async () => data.releaseFirst());
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Edit details' })).toBeNull());
    await waitFor(() => expect(edit()).toBeEnabled());
    expect(destination).toHaveFocus();
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
      let currentTrigger: HTMLButtonElement;
      if (change === 'account') {
        act(() => setRendererAccountOwner(ownerB));
        await screen.findByRole('heading', { name: name('a', ownerB) });
        currentTrigger = edit() as HTMLButtonElement;
        fireEvent.click(currentTrigger);
        nextTitle = within(editor()).getByRole('textbox', { name: 'Title' });
        fireEvent.change(nextTitle, { target: { value: 'Current draft' } });
        nextTitle.focus();
      } else {
        fireEvent.click(screen.getByRole('button', { name: name('b') }));
        await screen.findByRole('heading', { name: name('b') });
        const close = within(header()).getByRole('button', { name: 'Close' });
        close.focus();
        expect(edit()).toBeDisabled();
        currentTrigger = edit() as HTMLButtonElement;
      }
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
