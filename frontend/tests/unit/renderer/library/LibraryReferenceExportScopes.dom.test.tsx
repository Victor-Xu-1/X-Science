import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const ids = Array.from({ length: 203 }, (_, index) => `paper-${index}`);
const rows: LiteratureItem[] = ids.slice(0, 2).map<LiteratureItem>((id) => ({
  id,
  title: id,
  metadata_revision: 1,
  metadata: {},
  reading_status: 'read',
  deleted: false,
  project_ids: ['project'],
  attachments: [],
}));
const releases: Array<() => void> = [];
function fixture({
  hold = false,
  staleCursor = false,
  emptyMembership = false,
  emptyPage = false,
  collectionCount = ids.length,
  failedPage = false,
  holdFormats = false,
}: {
  hold?: boolean;
  staleCursor?: boolean;
  emptyMembership?: boolean;
  emptyPage?: boolean;
  collectionCount?: number;
  failedPage?: boolean;
  holdFormats?: boolean;
} = {}) {
  const gates: Array<() => void> = [];
  const formatGates: Array<() => void> = [];
  const pageFailures: URL[] = [];
  let metadataAvailable = true;
  const metadataReads: boolean[] = [];
  const queries: URL[] = [];
  const writes: Array<{ format: string; item_ids: string[] }> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects') {
        metadataReads.push(metadataAvailable);
        return json({ projects: metadataAvailable ? [{ project_id: 'project', name: 'Project / Science' }] : [] });
      }
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
              item_count: collectionCount,
            },
          ],
        });
      if (url.pathname === '/api/literature/items') {
        if (url.searchParams.get('all_item_ids') === 'true') {
          queries.push(url);
          if (hold)
            await new Promise<void>((done) => {
              gates.push(done);
              releases.push(done);
            });
          return json({
            schema: 'x-science.literature-selection.v1',
            item_ids: emptyMembership ? [] : ids,
            total_count: emptyMembership ? 0 : ids.length,
          });
        }
        if (failedPage) {
          pageFailures.push(url);
          return json({ code: 'LITERATURE_UNAVAILABLE' }, 500);
        }
        return json({
          schema: 'x-science.literature-page.v1',
          items: emptyPage ? [] : rows,
          total_count: emptyPage ? 0 : ids.length,
          next_cursor: emptyPage
            ? undefined
            : staleCursor
              ? 'next'
              : url.searchParams.has('cursor')
                ? 'after-next'
                : 'next',
        });
      }
      if (url.pathname === '/api/literature/export-references' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        writes.push(body);
        if (holdFormats)
          await new Promise<void>((done) => {
            formatGates.push(done);
            releases.push(done);
          });
        return json({
          schema: 'x-science.literature-export.v1',
          ...body,
          content: body.item_ids
            .map((id: string) =>
              body.format === 'bibtex' ? `@misc{${id}, title={Stored}}` : `TY  - JOUR\nTI  - ${id}\nER  - `
            )
            .join('\n\n'),
        });
      }
      throw new Error(`Unexpected scope-export request ${url.pathname}`);
    })
  );
  return {
    release: (index = 0) => gates[index]?.(),
    releaseFormat: (index = 0) => formatGates[index]?.(),
    setMetadataAvailable: (available: boolean) => {
      metadataAvailable = available;
    },
    pageFailures,
    metadataReads,
    queries,
    writes,
  };
}
function Navigation() {
  const navigate = useNavigate();
  return (
    <>
      <button onClick={() => void navigate('/library?project=project&q=changed')}>Change export visit</button>
      <button onClick={() => void navigate('/library?project=project&collection=collection')}>
        Change export container
      </button>
      <button onClick={() => void navigate('/library?project=project')}>Back export project</button>
    </>
  );
}
async function chooseFormat(label: string) {
  const text = await screen.findByText(label);
  await waitFor(() => expect(text.closest('[role="menuitem"]')).not.toHaveAttribute('aria-disabled', 'true'));
  fireEvent.click(text);
}
async function mount(path: string) {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <Navigation />
      <LibraryPage />
    </MemoryRouter>,
    'en-US'
  );
}
describe('Library reference export scopes through the actual page', () => {
  let downloads: string[];
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('scope-export-owner');
    localStorage.clear();
    installLiteratureLocks();
    downloads = [];
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:scope-export') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () {
      downloads.push(this.download);
    });
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      releases.splice(0).forEach((release) => release());
      setRendererAccountOwner(null);
    });
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it('resolves all matching filters and exclusions beyond the current page before one serialized RIS download', async () => {
    const http = fixture({ hold: true });
    await mount('/library?project=project&q=matching&reading_status=read');
    const page = await screen.findByRole('checkbox', { name: 'Select this page' });
    await waitFor(() => expect(page).toBeEnabled());
    fireEvent.click(page);
    fireEvent.click(await screen.findByRole('button', { name: 'Select all matching references' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select paper-0' }));
    const exportButton = await screen.findByRole('button', { name: 'Export references' });
    fireEvent.click(exportButton);
    await chooseFormat('RIS (.ris)');
    await waitFor(() => expect(http.queries).toHaveLength(1));
    expect(http.writes).toHaveLength(0);
    expect(downloads).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Export references' }));
    expect(http.queries).toHaveLength(1);
    expect(http.queries[0].searchParams.get('query')).toBe('matching');
    expect(http.queries[0].searchParams.get('reading_status')).toBe('read');
    expect(http.queries[0].searchParams.get('project_id')).toBe('project');
    expect(http.queries[0].searchParams.has('cursor')).toBe(false);
    expect(http.queries[0].searchParams.has('order')).toBe(false);
    await act(async () => http.release());
    await waitFor(() => expect(downloads).toEqual(['selected-references.ris']));
    expect(http.writes.map((body) => body.item_ids.length)).toEqual([200, 2]);
    expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids.slice(1));
  });
  it.each(['bibtex', 'ris'] as const)(
    'exports the active project and collection as %s in title order while ignoring list query, filter and cursor',
    async (format) => {
      const http = fixture();
      await mount('/library?project=project&collection=collection&q=filtered&reading_status=read&cursor=next');
      const more = await screen.findByRole('button', { name: 'More actions' });
      await waitFor(() => expect(more).toBeEnabled());
      fireEvent.click(more);
      fireEvent.mouseEnter(await screen.findByText('Export references'));
      await chooseFormat(format === 'ris' ? 'RIS (.ris)' : 'BibTeX (.bib)');
      await waitFor(() =>
        expect(downloads).toEqual([`Project - Science-references.${format === 'ris' ? 'ris' : 'bib'}`])
      );
      expect(http.queries).toHaveLength(1);
      const params = http.queries[0].searchParams;
      expect(params.get('project_id')).toBe('project');
      expect(params.get('collection_id')).toBe('collection');
      expect(params.get('order')).toBe('title-asc');
      for (const key of ['query', 'reading_status', 'cursor', 'limit']) expect(params.has(key)).toBe(false);
      expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids);
    }
  );
  it('keeps current-scope export independent of a failed/stale paginated cursor', async () => {
    const http = fixture({ failedPage: true });
    await mount('/library?project=project&cursor=next&q=filtered');
    const more = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(more).toBeEnabled());
    expect(http.pageFailures).toHaveLength(1);
    fireEvent.click(more);
    fireEvent.mouseEnter(await screen.findByText('Export references'));
    await chooseFormat('RIS (.ris)');
    await waitFor(() => expect(downloads).toEqual(['Project - Science-references.ris']));
    expect(http.queries[0].searchParams.has('query')).toBe(false);
    expect(http.queries[0].searchParams.has('cursor')).toBe(false);
  });
  it.each(['account', 'visit'] as const)(
    'retires a late allmatching resolver on %s change without releasing the newer owner',
    async (boundary) => {
      const http = fixture({ hold: true });
      await mount('/library?project=project');
      const select = async () => {
        const page = await screen.findByRole('checkbox', { name: 'Select this page' });
        await waitFor(() => expect(page).toBeEnabled());
        fireEvent.click(page);
        fireEvent.click(await screen.findByRole('button', { name: 'Select all matching references' }));
      };
      const start = async () => {
        fireEvent.click(await screen.findByRole('button', { name: 'Export references' }));
        await chooseFormat('RIS (.ris)');
      };
      await select();
      await start();
      await waitFor(() => expect(http.queries).toHaveLength(1));
      if (boundary === 'account')
        await act(async () => {
          setRendererAccountOwner('next-export-owner');
        });
      else fireEvent.click(screen.getByRole('button', { name: 'Change export visit' }));
      await select();
      await start();
      await waitFor(() => expect(http.queries).toHaveLength(2));
      await act(async () => http.release(0));
      expect(http.writes).toHaveLength(0);
      expect(downloads).toEqual([]);
      expect(screen.getByRole('button', { name: 'Export references' })).toHaveAttribute('aria-busy', 'true');
      await act(async () => http.release(1));
      await waitFor(() => expect(downloads).toEqual(['selected-references.ris']));
      expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids);
    }
  );
  it('keeps all-library scope absent, treats empty canonical membership as idle, and never downloads an empty file', async () => {
    fixture();
    await mount('/library');
    await screen.findByRole('checkbox', { name: 'Select this page' });
    expect(screen.queryByRole('button', { name: 'More actions' })).toBeNull();
    cleanup();
    const http = fixture({ emptyMembership: true });
    await mount('/library?project=project');
    const more = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(more).toBeEnabled());
    fireEvent.click(more);
    fireEvent.mouseEnter(await screen.findByText('Export references'));
    await chooseFormat('RIS (.ris)');
    await waitFor(() => expect(http.queries).toHaveLength(1));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'More actions' })).toHaveAttribute('data-state', 'idle')
    );
    expect(http.writes).toEqual([]);
    expect(downloads).toEqual([]);
    expect(screen.queryByRole('alert')).toBeNull();
  });
  it('opens on hover without stealing focus, supports keyboard/Escape and clears short success state', async () => {
    const http = fixture();
    await mount('/library');
    const page = await screen.findByRole('checkbox', { name: 'Select this page' });
    await waitFor(() => expect(page).toBeEnabled());
    fireEvent.click(page);
    const entry = screen.getByRole('button', { name: 'Export references' });
    page.focus();
    fireEvent.pointerEnter(entry, { pointerType: 'mouse' });
    await screen.findByText('RIS (.ris)');
    expect(document.activeElement).toBe(page);
    fireEvent.pointerLeave(entry, { pointerType: 'mouse' });
    await waitFor(() => expect(screen.queryByText('RIS (.ris)')).toBeNull());
    entry.focus();
    fireEvent.keyDown(entry, { key: 'ArrowDown', keyCode: 40, which: 40 });
    const bibtex = await screen.findByText('BibTeX (.bib)');
    await waitFor(() => expect(document.activeElement).toBe(bibtex.closest('[role="menuitem"]')));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape', keyCode: 27, which: 27 });
    await waitFor(() => expect(screen.queryByText('BibTeX (.bib)')).toBeNull());
    expect(document.activeElement).toBe(entry);
    fireEvent.keyDown(entry, { key: 'ArrowDown', keyCode: 40, which: 40 });
    const chosen = await screen.findByText('BibTeX (.bib)');
    fireEvent.keyDown(chosen.closest('[role="menuitem"]')!, { key: 'Enter', keyCode: 13, which: 13 });
    await waitFor(() => expect(downloads).toEqual(['selected-references.bib']));
    expect(http.writes).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Export references' })).toHaveAttribute('data-state', 'success');
    await waitFor(
      () => expect(screen.getByRole('button', { name: 'Export references' })).toHaveAttribute('data-state', 'idle'),
      { timeout: 2500 }
    );
  });

  it('exports populated scope despite an empty filtered search and respects only known unfiltered zero counts', async () => {
    const http = fixture({ emptyPage: true });
    await mount('/library?project=project&collection=collection&q=notfound&reading_status=read');
    let more = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(more).toBeEnabled());
    fireEvent.click(more);
    fireEvent.mouseEnter(await screen.findByText('Export references'));
    await chooseFormat('RIS (.ris)');
    await waitFor(() => expect(downloads).toEqual(['Project - Science-references.ris']));
    expect(http.queries[0].searchParams.has('query')).toBe(false);
    expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids);
    cleanup();
    const zero = fixture({ emptyPage: true, emptyMembership: true, collectionCount: 0 });
    await mount('/library?collection=collection');
    more = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(more).toBeEnabled());
    fireEvent.click(more);
    const edit = await screen.findByText('Edit collection');
    expect(edit.closest('[role="menuitem"]')).not.toHaveAttribute('aria-disabled', 'true');
    fireEvent.mouseEnter(screen.getByText('Export references'));
    const ris = await screen.findByText('RIS (.ris)');
    expect(ris.closest('[role="menuitem"]')).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(edit);
    expect(await screen.findByDisplayValue('Collection papers')).toBeInTheDocument();
    expect(zero.queries).toEqual([]);
  });
  it('keeps a captured whole-scope request alive across unrelated list-query changes', async () => {
    const http = fixture({ hold: true });
    await mount('/library?project=project');
    const more = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(more).toBeEnabled());
    fireEvent.click(more);
    fireEvent.mouseEnter(await screen.findByText('Export references'));
    await chooseFormat('RIS (.ris)');
    await waitFor(() => expect(http.queries).toHaveLength(1));
    fireEvent.click(screen.getByRole('button', { name: 'Change export visit' }));
    await act(async () => http.release());
    await waitFor(() => expect(downloads).toEqual(['Project - Science-references.ris']));
    expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids);
  });
  it('fences a whole-scope container ABA without letting old completion release a newer request', async () => {
    const http = fixture({ hold: true });
    await mount('/library?project=project');
    const start = async () => {
      const more = await screen.findByRole('button', { name: 'More actions' });
      await waitFor(() => expect(more).toBeEnabled());
      fireEvent.click(more);
      fireEvent.mouseEnter(await screen.findByText('Export references'));
      await chooseFormat('RIS (.ris)');
    };
    await start();
    await waitFor(() => expect(http.queries).toHaveLength(1));
    fireEvent.click(screen.getByRole('button', { name: 'Change export container' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'More actions' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Back export project' }));
    await start();
    await waitFor(() => expect(http.queries).toHaveLength(2));
    await act(async () => http.release(0));
    expect(http.writes).toEqual([]);
    expect(downloads).toEqual([]);
    expect(screen.getByRole('button', { name: 'More actions' })).toHaveAttribute('aria-busy', 'true');
    await act(async () => http.release(1));
    await waitFor(() => expect(downloads).toEqual(['Project - Science-references.ris']));
  });
  it('retires held scope formatting when metadata disappears and availability ABA cannot revive it', async () => {
    const http = fixture({ holdFormats: true });
    await mount('/library?project=project');
    const start = async () => {
      const more = await screen.findByRole('button', { name: 'More actions' });
      await waitFor(() => expect(more).toBeEnabled());
      fireEvent.click(more);
      fireEvent.mouseEnter(await screen.findByText('Export references'));
      await chooseFormat('RIS (.ris)');
    };
    await start();
    await waitFor(() => expect(http.writes).toHaveLength(1));
    http.setMetadataAvailable(false);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'More actions' })).toBeNull());
    await waitFor(() => expect(http.metadataReads.at(-1)).toBe(false));
    http.setMetadataAvailable(true);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'More actions' })).toBeEnabled());
    await start();
    await waitFor(() => expect(http.writes).toHaveLength(2));
    await act(async () => http.releaseFormat(0));
    expect(downloads).toEqual([]);
    expect(screen.getByRole('button', { name: 'More actions' })).toHaveAttribute('aria-busy', 'true');
    await act(async () => http.releaseFormat(1));
    await waitFor(() => expect(http.writes).toHaveLength(3));
    await act(async () => http.releaseFormat(2));
    await waitFor(() => expect(downloads).toEqual(['Project - Science-references.ris']));
  });
});
