import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
import { readEmptyResourceTagCatalog } from './libraryResourceTagFixtures';

const allIds = Array.from({ length: 205 }, (_, index) => `paper-${index}`);
const columns = [
  'item_id',
  'title',
  'authors',
  'year',
  'item_type',
  'publication',
  'volume',
  'issue',
  'pages',
  'doi',
  'pmid',
  'pmcid',
  'arxiv_id',
  'url',
  'abstract',
  'language',
];
const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
const header = columns.map(quote).join(',') + '\r\n';
const csv = (ids: string[]) =>
  header +
  ids.map((id) => [id, '研究, "quoted"\r\nsecond', ...Array<string>(14).fill('')].map(quote).join(',')).join('\r\n') +
  '\r\n';
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const pending: Array<() => void> = [];
function fixture({
  holdMembership = false,
  holdFormats = false,
  failSecond = false,
  ids = allIds,
}: { holdMembership?: boolean; holdFormats?: boolean; failSecond?: boolean; ids?: string[] } = {}) {
  const queries: URL[] = [];
  const writes: Array<{ item_ids: string[]; format: string; signal?: AbortSignal | null }> = [];
  const membership: Array<() => void> = [];
  const formats: Array<() => void> = [];
  const rows = (page: number): LiteratureItem[] =>
    ids.slice(page * 2, page * 2 + 2).map<LiteratureItem>((id) => ({
      id,
      title: id,
      metadata_revision: 1,
      metadata: {},
      reading_status: 'read',
      deleted: false,
      project_ids: ['project'],
      attachments: [],
    }));
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      const tags = readEmptyResourceTagCatalog(url, init);
      if (tags) return tags;
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects')
        return json({ projects: [{ project_id: 'project', name: 'Project / Science' }] });
      if (url.pathname === '/api/literature/collections')
        return json({
          schema: 'x-science.literature-collections.v1',
          collections: [
            {
              id: 'collection',
              revision: 1,
              name: 'Collection papers',
              description: '',
              parent_id: null,
              sort_order: 0,
              item_count: ids.length,
            },
          ],
        });
      if (url.pathname === '/api/literature/items') {
        if (url.searchParams.get('all_item_ids') === 'true') {
          queries.push(url);
          if (holdMembership)
            await new Promise<void>((resolve) => {
              membership.push(resolve);
              pending.push(resolve);
            });
          return json({ schema: 'x-science.literature-selection.v1', item_ids: ids, total_count: ids.length });
        }
        const page = url.searchParams.has('cursor') ? 1 : 0;
        return json({
          schema: 'x-science.literature-page.v1',
          items: rows(page),
          total_count: ids.length,
          next_cursor: page ? undefined : 'page2',
        });
      }
      if (url.pathname === '/api/literature/export-references' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        writes.push({ ...body, signal: init.signal });
        if (holdFormats)
          await new Promise<void>((resolve) => {
            formats.push(resolve);
            pending.push(resolve);
          });
        if (failSecond && writes.length === 2) return json({ code: 'LITERATURE_UNAVAILABLE' }, 404);
        return json({ schema: 'x-science.literature-export.v1', ...body, content: csv(body.item_ids) });
      }
      throw new Error(`Unexpected CSV request ${url.pathname}`);
    })
  );
  return {
    queries,
    writes,
    releaseMembership: (index = 0) => membership[index]?.(),
    releaseFormat: (index = 0) => formats[index]?.(),
  };
}
function Navigation() {
  const navigate = useNavigate();
  return (
    <>
      <button onClick={() => void navigate('/library?project=project&collection=collection')}>CSV container</button>
      <button onClick={() => void navigate('/library?project=project')}>CSV project</button>
    </>
  );
}
async function mount(path = '/library?project=project') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <Navigation />
      <LibraryPage />
    </MemoryRouter>,
    'en-US'
  );
}
async function selectPage(allMatching = false) {
  const page = await screen.findByRole('checkbox', { name: 'Select this page' });
  await waitFor(() => expect(page).toBeEnabled());
  fireEvent.click(page);
  if (allMatching) fireEvent.click(await screen.findByRole('button', { name: 'Select all matching references' }));
}
async function startCSV(scope = false) {
  const trigger = await screen.findByRole('button', { name: scope ? 'More actions' : 'Export references' });
  await waitFor(() => expect(trigger).toBeEnabled());
  fireEvent.click(trigger);
  if (scope) fireEvent.mouseEnter(await screen.findByText('Export references'));
  const choice = await screen.findByText('CSV (.csv)');
  await waitFor(() => expect(choice.closest('[role="menuitem"]')).not.toHaveAttribute('aria-disabled', 'true'));
  fireEvent.click(choice);
  return trigger;
}
async function bytes(blob: Blob): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener('load', () => resolve(new Uint8Array(reader.result as ArrayBuffer)), { once: true });
    reader.addEventListener('error', reject, { once: true });
    reader.readAsArrayBuffer(blob);
  });
}

describe('Library bibliographic CSV through the page and original download owner', () => {
  let downloads: string[];
  let blobs: Blob[];
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('csv-owner');
    localStorage.clear();
    installLiteratureLocks();
    downloads = [];
    blobs = [];
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn((blob: Blob) => {
        blobs.push(blob);
        return 'blob:csv';
      }),
    });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () {
      downloads.push(this.download);
    });
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      pending.splice(0).forEach((release) => release());
      setRendererAccountOwner(null);
    });
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it('exports the current explicit selection as one UTF8 CSV Blob and clears its URL', async () => {
    const http = fixture();
    const rendered = await mount();
    const first = await screen.findByRole('checkbox', { name: 'Select paper-0' });
    await waitFor(() => expect(first).toBeEnabled());
    fireEvent.click(first);
    const next = await screen.findByRole('checkbox', { name: 'Select paper-1' });
    await waitFor(() => expect(next).toBeEnabled());
    fireEvent.click(next);
    await startCSV();
    await waitFor(() => expect(downloads).toEqual(['selected-references.csv']));
    expect(http.writes.flatMap((write) => write.item_ids)).toEqual(['paper-0', 'paper-1']);
    expect(http.queries).toEqual([]);
    expect(blobs).toHaveLength(1);
    expect(blobs[0].type).toBe('text/csv;charset=utf-8');
    const body = await bytes(blobs[0]);
    expect(body.subarray(0, 3)).toEqual(new Uint8Array([239, 187, 191]));
    expect(new TextDecoder().decode(body.slice(3))).toBe(csv(['paper-0', 'paper-1']));
    expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith('blob:csv');
    expect(document.querySelector('a[download]')).toBeNull();
    await act(async () => rendered.i18n.changeLanguage('zh-CN'));
    fireEvent.click(screen.getByRole('button', { name: '导出文献' }));
    expect(await screen.findByText('CSV (.csv)')).toBeInTheDocument();
  });
  it('resolves all matching exclusions beyond the page and shares pending admission with the scope entry', async () => {
    const http = fixture({ holdMembership: true });
    await mount('/library?project=project&q=matching&reading_status=read');
    await selectPage(true);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select paper-0' }));
    await startCSV();
    await waitFor(() => expect(http.queries).toHaveLength(1));
    expect(screen.getByRole('button', { name: 'More actions' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Export references' })).toBeDisabled();
    expect(downloads).toEqual([]);
    expect(http.queries[0].searchParams.get('query')).toBe('matching');
    await act(async () => http.releaseMembership());
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(http.writes.map((write) => write.item_ids.length)).toEqual([200, 4]);
    expect(http.writes.flatMap((write) => write.item_ids)).toEqual(allIds.slice(1));
  });
  it.each([
    '/library?project=project&q=filtered&cursor=page2',
    '/library?collection=collection&q=filtered&reading_status=read',
  ])('exports complete active scope %s in canonical title order', async (path) => {
    const http = fixture();
    await mount(path);
    await startCSV(true);
    await waitFor(() => expect(downloads).toHaveLength(1));
    const params = http.queries[0].searchParams;
    expect(params.get('order')).toBe('title-asc');
    for (const key of ['query', 'reading_status', 'cursor', 'limit']) expect(params.has(key)).toBe(false);
    expect(http.writes.flatMap((write) => write.item_ids)).toEqual(allIds);
    expect(downloads[0]).toMatch(/-references\.csv$/u);
    expect(blobs).toHaveLength(1);
  });
  it('does not download a failed suffix and releases the same Work slot for retry', async () => {
    const http = fixture({ failSecond: true });
    await mount();
    await startCSV(true);
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not export');
    expect(blobs).toEqual([]);
    await startCSV(true);
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(http.writes).toHaveLength(4);
  });
  it.each(['container', 'account'] as const)(
    'fences a late CSV formatting response across %s ABA',
    async (boundary) => {
      const http = fixture({ holdFormats: true, ids: allIds.slice(0, 2) });
      await mount();
      await startCSV(true);
      await waitFor(() => expect(http.writes).toHaveLength(1));
      if (boundary === 'container') {
        fireEvent.click(screen.getByRole('button', { name: 'CSV container' }));
        await waitFor(() => expect(screen.getByRole('button', { name: 'More actions' })).toBeEnabled());
        fireEvent.click(screen.getByRole('button', { name: 'CSV project' }));
      } else
        await act(async () => {
          setRendererAccountOwner('other');
          setRendererAccountOwner('csv-owner');
        });
      await startCSV(true);
      await waitFor(() => expect(http.writes).toHaveLength(2));
      expect(http.writes[0].signal?.aborted).toBe(true);
      await act(async () => http.releaseFormat(0));
      expect(downloads).toEqual([]);
      expect(screen.getByRole('button', { name: 'More actions' })).toHaveAttribute('aria-busy', 'true');
      await act(async () => http.releaseFormat(1));
      await waitFor(() => expect(downloads).toHaveLength(1));
    }
  );
  it('aborts CSV work on unmount and ignores a transport that returns late', async () => {
    const http = fixture({ holdFormats: true, ids: allIds.slice(0, 2) });
    const view = await mount();
    await startCSV(true);
    await waitFor(() => expect(http.writes).toHaveLength(1));
    view.unmount();
    expect(http.writes[0].signal?.aborted).toBe(true);
    await act(async () => http.releaseFormat());
    expect(blobs).toEqual([]);
  });
});
