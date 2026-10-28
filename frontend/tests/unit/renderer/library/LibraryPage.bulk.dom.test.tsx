import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';

const preview = vi.hoisted(() => ({ mounts: 0, unmounts: 0 }));
// Only instrument the PDF child's local lifetime; no PDF rendering or scientific acceptance is claimed.
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
      <div data-testid='bulk-pdf'>
        <output aria-label='Bulk PDF page'>{page}</output>
        <button type='button' onClick={() => setPage(2)}>
          Advance bulk fixture PDF
        </button>
      </div>
    );
  },
}));

const owner = 'library-page-bulk-owner';
const itemId = (n: number) => `paper-${String(n).padStart(3, '0')}`;
const title = (n: number) => `Synthetic ${itemId(n)}`;
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
const release: Array<() => void> = [];
type StatusWrite = { operation_id: string; item_ids: string[]; reading_status: LiteratureReadingStatus };
type Call = { method: string; url: URL; body?: StatusWrite };
function catalogue(count = 402) {
  const items = new Map<string, LiteratureItem>(
    Array.from({ length: count }, (_, i) => {
      const id = itemId(i + 1);
      return [
        id,
        {
          id,
          title: title(i + 1),
          metadata_revision: 7,
          reading_status: 'unread',
          metadata: { doi: `10.1234/${id}`, authors: ['Ada'], year: 2024 },
          deleted: false,
          project_ids: ['project'],
          attachments: i
            ? []
            : [
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
        },
      ];
    })
  );
  const calls: Call[] = [];
  const writes: StatusWrite[] = [];
  const ledger = new Map<string, { body: string; receipt: object }>();
  let nextList: Promise<void> | undefined;
  let respond: (write: StatusWrite, index: number, receipt: object) => Response | Promise<Response> = (
    _write,
    _index,
    receipt
  ) => json(receipt);
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    const method = init?.method ?? 'GET';
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as StatusWrite) : undefined;
    calls.push({ method, url, body });
    if (method === 'GET' && url.pathname === '/api/settings/client') return json({});
    if (method === 'GET' && url.pathname === '/api/projects')
      return json({ projects: [{ project_id: 'project', name: 'Owned project' }] });
    if (method === 'GET' && url.pathname === '/api/literature/collections')
      return json({ schema: 'x-science.literature-collections.v1', collections: [] });
    const membership = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/collections$/);
    if (method === 'GET' && membership)
      return json({
        schema: 'x-science.literature-collection-membership.v1',
        item_id: decodeURIComponent(membership[1]),
        collection_ids: [],
      });
    const publication = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/publication-check$/);
    if (method === 'GET' && publication) {
      const item = items.get(decodeURIComponent(publication[1]))!;
      return json({
        schema: 'x-science.literature-publication-check.v1',
        item_id: item.id,
        metadata_revision: Number(url.searchParams.get('expected_metadata_revision')),
        doi: item.metadata.doi,
        snapshot: { doi: item.metadata.doi, checked_at: '2026-10-20T08:09:10Z', notices: [] },
      });
    }
    if (method === 'GET' && url.pathname === '/api/literature/items') {
      const matches = [...items.values()].filter(
        (item) =>
          !item.deleted &&
          (!url.searchParams.has('reading_status') || item.reading_status === url.searchParams.get('reading_status')) &&
          (!url.searchParams.has('query') ||
            item.title.toLowerCase().includes(url.searchParams.get('query')!.toLowerCase())) &&
          (!url.searchParams.has('project_id') || item.project_ids.includes(url.searchParams.get('project_id')!))
      );
      if (url.searchParams.get('all_item_ids') === 'true') {
        expect(url.searchParams.has('cursor')).toBe(false);
        return json({
          schema: 'x-science.literature-selection.v1',
          item_ids: matches.map((item) => item.id),
          total_count: matches.length,
        });
      }
      const readback = nextList;
      nextList = undefined;
      if (readback) await readback;
      const offset = Number((url.searchParams.get('cursor') ?? 'offset:0').split(':')[1]);
      const limit = Number(url.searchParams.get('limit') ?? '20');
      return json({
        schema: 'x-science.literature-page.v1',
        items: matches.slice(offset, offset + limit),
        total_count: matches.length,
        ...(offset + limit < matches.length ? { next_cursor: `offset:${offset + limit}` } : {}),
      });
    }
    if (method === 'POST' && url.pathname === '/api/literature/items/reading-status') {
      if (!body || !body.item_ids.length || body.item_ids.length > 200)
        throw new Error('Invalid controlled status batch');
      const prior = ledger.get(body.operation_id);
      const raw = JSON.stringify(body);
      if (prior && prior.body !== raw) return json({ code: 'LITERATURE_CONFLICT' }, 409);
      const receipt = prior?.receipt ?? {
        schema: 'x-science.literature-reading-status-receipt.v1',
        operation_id: body.operation_id,
        item_ids: [...new Set(body.item_ids)],
        reading_status: body.reading_status,
      };
      if (!prior) {
        for (const id of body.item_ids) {
          const item = items.get(id);
          if (!item) throw new Error('Unknown controlled target');
          items.set(id, { ...item, reading_status: body.reading_status });
        }
        ledger.set(body.operation_id, { body: raw, receipt });
      }
      writes.push(body);
      return respond(body, writes.length - 1, receipt);
    }
    const detail = url.pathname.match(/^\/api\/literature\/items\/([^/]+)$/);
    if (method === 'GET' && detail) return json(items.get(decodeURIComponent(detail[1])));
    throw new Error(`Unexpected controlled Library request: ${method} ${url.pathname}`);
  });
  vi.stubGlobal('fetch', fetch);
  return {
    items,
    calls,
    writes,
    ledger,
    reply: (fn: typeof respond) => {
      respond = fn;
    },
    delayNextListUntil: (readback: Promise<void>) => {
      nextList = readback;
    },
    normalReads: () =>
      calls.filter(
        (c) =>
          c.method === 'GET' && c.url.pathname === '/api/literature/items' && !c.url.searchParams.has('all_item_ids')
      ).length,
    completeReads: () => calls.filter((c) => c.method === 'GET' && c.url.searchParams.get('all_item_ids') === 'true'),
  };
}
function Navigation() {
  const navigate = useNavigate();
  const location = useLocation();
  return (
    <>
      <output aria-label='Bulk Library location'>{location.search}</output>
      <button type='button' onClick={() => void navigate('/outside')}>
        Leave Library
      </button>
    </>
  );
}
function mount(path = '/library') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <Navigation />
      <Routes>
        <Route path='/library' element={<LibraryPage />} />
        <Route path='/outside' element={<h1>Outside Library</h1>} />
      </Routes>
    </MemoryRouter>,
    'en-US'
  );
}
const toolbar = () => screen.getByRole('toolbar', { name: 'Reference selection' });
async function selectAll() {
  // Same-scope refresh retains these controls; presence alone is not readiness.
  const page = await screen.findByRole('checkbox', { name: 'Select this page' });
  await waitFor(() => expect(page).toBeEnabled());
  fireEvent.click(page);
  const matching = await screen.findByRole('button', { name: 'Select all matching references' });
  await waitFor(() => expect(matching).toBeEnabled());
  fireEvent.click(matching);
}
const start = () =>
  fireEvent.change(within(toolbar()).getByRole('combobox', { name: 'Reading status for selection' }), {
    target: { value: 'read' },
  });
const detail = () =>
  within(screen.getByRole('dialog', { name: title(1) })).getAllByRole('region', {
    name: 'Literature details',
  })[0];

describe('Library Page bulk status through the real mutation owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner(owner);
    localStorage.clear();
    installLiteratureLocks();
    preview.mounts = 0;
    preview.unmounts = 0;
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const done of release.splice(0)) done();
      setRendererAccountOwner(null);
    });
    vi.unstubAllGlobals();
  });

  it('resolves all matching with exclusions, writes200/200/1 and preserves the open draft/PDF without per-child refreshes', async () => {
    const http = catalogue();
    const gates = [deferred(), deferred(), deferred()];
    release.push(...gates.map((gate) => gate.resolve));
    http.reply(async (_write, index, receipt) => {
      await gates[index].promise;
      return json(receipt);
    });
    await mount(`/library?item=${itemId(1)}`);
    await screen.findByRole('heading', { name: title(1) });
    await screen.findByRole('checkbox', { name: `Select ${title(1)}` });
    fireEvent.click(screen.getByRole('checkbox', { name: `Select ${title(1)}` }));
    expect(screen.queryByRole('button', { name: 'Select all matching references' })).toBeNull();
    fireEvent.click(within(detail()).getByRole('button', { name: /Preview PDF/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Advance bulk fixture PDF' }));
    const pdf = screen.getByTestId('bulk-pdf');
    fireEvent.click(within(detail()).getByRole('button', { name: 'Edit details' }));
    const draft = within(detail()).getByRole('textbox', { name: 'Title' });
    fireEvent.change(draft, { target: { value: 'Keep this unsaved title' } });
    expect(pdf).not.toBeVisible();
    expect(pdf.closest('.library-workbench__overview')).toHaveAttribute('inert');
    await selectAll();
    fireEvent.click(screen.getByRole('checkbox', { name: `Select ${title(2)}` }));
    const reads = http.normalReads();
    start();
    await waitFor(() => expect(http.writes).toHaveLength(1));
    expect(http.completeReads()).toHaveLength(1);
    await act(async () => gates[0].resolve());
    await waitFor(() => expect(http.writes).toHaveLength(2));
    expect(http.normalReads()).toBe(reads);
    await act(async () => gates[1].resolve());
    await waitFor(() => expect(http.writes).toHaveLength(3));
    expect(http.normalReads()).toBe(reads);
    await act(async () => gates[2].resolve());
    await screen.findByText('401 references saved.');
    await waitFor(() => expect(http.normalReads()).toBe(reads + 1));
    expect(http.writes.map((write) => write.item_ids.length)).toEqual([200, 200, 1]);
    expect(http.writes.flatMap((write) => write.item_ids)).toEqual(
      [...http.items.keys()].filter((id) => id !== itemId(2))
    );
    expect(new Set(http.writes.map((write) => write.operation_id)).size).toBe(3);
    expect(http.items.get(itemId(2))?.reading_status).toBe('unread');
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(draft);
    expect(draft).toHaveValue('Keep this unsaved title');
    expect(screen.getByTestId('bulk-pdf')).toBe(pdf);
    expect(screen.getByLabelText('Bulk PDF page')).toHaveTextContent('2');
    expect(preview).toEqual({ mounts: 1, unmounts: 0 });
    expect(
      http.calls.filter((call) => call.method !== 'GET').every((call) => call.url.pathname.endsWith('/reading-status'))
    ).toBe(true);
    expect([...http.items.values()].every((item) => item.metadata_revision === 7)).toBe(true);
  });

  it('clears selection on filter/page changes and sends no later batch after leaving during the current ACK', async () => {
    const http = catalogue();
    const held = deferred();
    release.push(held.resolve);
    http.reply(async (_write, _index, receipt) => {
      await held.promise;
      return json(receipt);
    });
    await mount();
    await screen.findByRole('checkbox', { name: `Select ${title(1)}` });
    fireEvent.click(screen.getByRole('checkbox', { name: `Select ${title(1)}` }));
    fireEvent.change(screen.getByRole('combobox', { name: 'Reading status' }), { target: { value: 'read' } });
    await waitFor(() => expect(screen.queryByRole('toolbar', { name: 'Reference selection' })).toBeNull());
    fireEvent.change(screen.getByRole('combobox', { name: 'Reading status' }), { target: { value: 'unread' } });
    await screen.findByRole('checkbox', { name: `Select ${title(1)}` });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select this page' }));
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    await screen.findByRole('checkbox', { name: `Select ${title(21)}` });
    expect(screen.queryByRole('toolbar', { name: 'Reference selection' })).toBeNull();
    expect(screen.getByRole('checkbox', { name: 'Select this page' })).not.toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'Back to first page' }));
    await screen.findByRole('checkbox', { name: `Select ${title(1)}` });
    await selectAll();
    start();
    await waitFor(() => expect(http.writes).toHaveLength(1));
    const original = getLiteratureMutationController(getRendererAccountOwnerId());
    fireEvent.click(screen.getByRole('button', { name: 'Leave Library' }));
    await screen.findByRole('heading', { name: 'Outside Library' });
    await act(async () => held.resolve());
    await waitFor(() => expect(original.getSnapshot().mutating).toBe(false));
    expect(http.writes).toHaveLength(1);
    expect(http.ledger.size).toBe(1);
  });

  it('reconstructs the original pending batch after a lost reply and resumes unchanged UUIDs and the unfiltered suffix', async () => {
    const http = catalogue();
    http.reply((_write, index, receipt) => {
      if (index === 0) throw new TypeError('Committed, but the reply was lost');
      return json(receipt);
    });
    const mounted = await mount('/library?reading_status=unread');
    await selectAll();
    fireEvent.click(screen.getByRole('checkbox', { name: `Select ${title(2)}` }));
    start();
    await screen.findByRole('button', { name: 'Resume batch' });
    expect(http.writes).toHaveLength(1);
    const first = http.writes[0];
    const original = getLiteratureMutationController(owner);
    expect(http.ledger.size).toBe(1);
    mounted.unmount();
    // A fresh account generation creates a fresh real controller while the original owner's slot survives.
    act(() => {
      setRendererAccountOwner(null);
      setRendererAccountOwner(owner);
    });
    expect(getLiteratureMutationController(owner)).not.toBe(original);
    await mount('/library?reading_status=unread');
    fireEvent.click(await screen.findByRole('button', { name: 'Resume batch' }));
    await screen.findByText('401 references saved.');
    expect(http.completeReads()).toHaveLength(1);
    expect(http.writes.map((write) => write.item_ids.length)).toEqual([200, 200, 200, 1]);
    expect(http.writes[1]).toEqual(first);
    expect(http.ledger.size).toBe(3);
    expect(http.writes.slice(1).flatMap((write) => write.item_ids)).toEqual(
      [...http.items.keys()].filter((id) => id !== itemId(2))
    );
    expect(http.items.get(itemId(2))?.reading_status).toBe('unread');
    expect([...http.items.values()].filter((item) => item.reading_status === 'read')).toHaveLength(401);
    expect([...http.items.values()].every((item) => item.metadata_revision === 7)).toBe(true);
  });

  it('shows a new complete-selection failure instead of the previous completed batch and keeps selection retryable', async () => {
    const http = catalogue(21);
    const readback = deferred();
    release.push(readback.resolve);
    await mount();
    const pageSelection = await screen.findByRole('checkbox', { name: 'Select this page' });
    fireEvent.click(pageSelection);
    http.delayNextListUntil(readback.promise);
    start();
    await screen.findByText('20 references saved.');
    const controller = getLiteratureMutationController(owner);
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    const completed = controller.getSnapshot().action;
    expect(completed).toBeDefined();
    expect(controller.getSnapshot().pending).toBeNull();
    expect(http.writes).toHaveLength(1);
    expect(http.ledger.size).toBe(1);
    await waitFor(() => expect(http.normalReads()).toBe(2));
    expect(screen.getByRole('checkbox', { name: 'Select this page' })).toBe(pageSelection);
    expect(pageSelection).toBeVisible();
    expect(pageSelection).toBeDisabled();

    const originalFetch = globalThis.fetch;
    const resolvingRequests: URL[] = [];
    vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      if (
        (init?.method ?? 'GET') === 'GET' &&
        url.pathname === '/api/literature/items' &&
        url.searchParams.get('all_item_ids') === 'true'
      ) {
        resolvingRequests.push(url);
        return json({ code: 'UNAVAILABLE' }, 503);
      }
      return originalFetch(input, init);
    });
    const selecting = selectAll();
    await act(async () => {
      await Promise.resolve();
    });
    expect(pageSelection).toBeDisabled();
    expect(pageSelection).not.toBeChecked();
    expect(screen.queryByRole('button', { name: 'Select all matching references' })).toBeNull();
    await act(async () => {
      readback.resolve();
      await readback.promise;
    });
    await selecting;
    start();
    await waitFor(() => expect(resolvingRequests).toHaveLength(1));
    await waitFor(() =>
      expect(within(toolbar()).getByRole('combobox', { name: 'Reading status for selection' })).toBeEnabled()
    );
    expect(http.writes).toHaveLength(1);
    expect(http.ledger.size).toBe(1);
    expect(controller.getSnapshot().action).toBe(completed);
    expect(controller.getSnapshot().pending).toBeNull();
    expect(screen.getByRole('checkbox', { name: 'Select this page' })).toBeChecked();
    expect(within(toolbar()).getByText('All matching references selected')).toBeInTheDocument();
    expect(await screen.findByText('The complete selection could not be loaded. No batch was started.')).toBeVisible();
    expect(screen.queryByText('20 references saved.')).toBeNull();
  });
});
