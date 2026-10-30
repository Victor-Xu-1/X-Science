import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
const json = (value: unknown) => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const ids = Array.from({ length: 203 }, (_, index) => `paper-${index}`);
const rows: LiteratureItem[] = ids.slice(0, 2).map<LiteratureItem>((id) => ({ id, title: id, metadata_revision: 1, metadata: {}, reading_status: 'read', deleted: false, project_ids: ['project'], attachments: [] }));
const releases: Array<() => void> = [];
function fixture(hold = false) {
  let release!: () => void; const gate = new Promise<void>((done) => { release = done; }); releases.push(release);
  const queries: URL[] = []; const writes: Array<{ format: string; item_ids: string[] }> = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/settings/client') return json({});
    if (url.pathname === '/api/projects') return json({ projects: [{ project_id: 'project', name: 'Project / Science' }] });
    if (url.pathname === '/api/literature/collections') return json({ schema: 'x-science.literature-collections.v1', collections: [{ id: 'collection', revision: 1, name: 'Collection papers', description: '', parent_id: null, sort_order: 0, item_count: ids.length }] });
    if (url.pathname === '/api/literature/items') {
      if (url.searchParams.get('all_item_ids') === 'true') { queries.push(url); if (hold) await gate; return json({ schema: 'x-science.literature-selection.v1', item_ids: ids, total_count: ids.length }); }
      return json({ schema: 'x-science.literature-page.v1', items: rows, total_count: ids.length, next_cursor: 'next' });
    }
    if (url.pathname === '/api/literature/export-references' && init?.method === 'POST') {
      const body = JSON.parse(String(init.body)); writes.push(body);
      return json({ schema: 'x-science.literature-export.v1', ...body, content: body.item_ids.map((id: string) => `TY  - GEN\nID  - ${id}\nER  - `).join('\n\n') });
    }
    throw new Error(`Unexpected scope-export request ${url.pathname}`);
  }));
  return { release, queries, writes };
}
async function mount(path: string) { return renderWithI18n(<MemoryRouter initialEntries={[path]}><LibraryPage /></MemoryRouter>, 'en-US'); }
describe('Library reference export scopes through the actual page', () => {
  let downloads: string[];
  beforeEach(() => { resetRendererAccountScopeForTest(); setRendererAccountOwner('scope-export-owner'); localStorage.clear(); installLiteratureLocks(); downloads = [];
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:scope-export') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () { downloads.push(this.download); });
  });
  afterEach(async () => { cleanup(); await act(async () => { releases.splice(0).forEach((release) => release()); setRendererAccountOwner(null); }); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
  it('resolves all matching filters and exclusions beyond the current page before one serialized RIS download', async () => {
    const http = fixture(true); await mount('/library?project=project&q=matching&reading_status=read');
    const page = await screen.findByRole('checkbox', { name: 'Select this page' }); await waitFor(() => expect(page).toBeEnabled()); fireEvent.click(page);
    fireEvent.click(await screen.findByRole('button', { name: 'Select all matching references' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select paper-0' }));
    const exportButton = await screen.findByRole('button', { name: 'Export references' }); fireEvent.click(exportButton);
    fireEvent.click(await screen.findByText('RIS (.ris)'));
    await waitFor(() => expect(http.queries).toHaveLength(1));
    expect(http.writes).toHaveLength(0); expect(downloads).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Export references' })); expect(http.queries).toHaveLength(1);
    expect(http.queries[0].searchParams.get('query')).toBe('matching'); expect(http.queries[0].searchParams.get('reading_status')).toBe('read'); expect(http.queries[0].searchParams.get('project_id')).toBe('project'); expect(http.queries[0].searchParams.has('cursor')).toBe(false);
    await act(async () => http.release()); await waitFor(() => expect(downloads).toEqual(['selected-references.ris']));
    expect(http.writes.map((body) => body.item_ids.length)).toEqual([200, 2]); expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids.slice(1));
  });
  it('exports the active project and collection in title order through More actions while ignoring list query, filter and cursor', async () => {
    const http = fixture(); await mount('/library?project=project&collection=collection&q=filtered&reading_status=read&cursor=next');
    const more = await screen.findByRole('button', { name: 'More actions' }); await waitFor(() => expect(more).toBeEnabled()); fireEvent.click(more);
    fireEvent.mouseEnter(await screen.findByText('Export references'));
    fireEvent.click(await screen.findByText('RIS (.ris)'));
    await waitFor(() => expect(downloads).toEqual(['Project - Science-references.ris']));
    expect(http.queries).toHaveLength(1); const params = http.queries[0].searchParams;
    expect(params.get('project_id')).toBe('project'); expect(params.get('collection_id')).toBe('collection'); expect(params.get('order')).toBe('title-asc');
    for (const key of ['query', 'reading_status', 'cursor', 'limit']) expect(params.has(key)).toBe(false);
    expect(http.writes.flatMap((body) => body.item_ids)).toEqual(ids);
  });
});
