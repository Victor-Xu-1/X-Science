import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const papers: LiteratureItem[] = ['one', 'two'].map<LiteratureItem>((id) => ({
  id,
  title: `Paper ${id}`,
  metadata_revision: 1,
  reading_status: 'unread',
  metadata: {},
  deleted: false,
  project_ids: [],
  attachments: [],
}));
function Navigation() {
  const navigate = useNavigate();
  return <button onClick={() => void navigate('/library?q=changed')}>Change scope</button>;
}
const pending: Array<() => void> = [];
function catalogue() {
  let release!: (value: Response) => void;
  const response = new Promise<Response>((done) => {
    release = done;
  });
  pending.push(() => release(json({ code: 'LITERATURE_UNAVAILABLE' }, 404)));
  const requests: string[][] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects') return json({ projects: [] });
      if (url.pathname === '/api/literature/collections')
        return json({ schema: 'x-science.literature-collections.v1', collections: [] });
      if (url.pathname === '/api/literature/items')
        return json({ schema: 'x-science.literature-page.v1', items: papers, total_count: 2 });
      if (url.pathname === '/api/literature/export-bibtex' && init?.method === 'POST') {
        requests.push(JSON.parse(String(init.body)).item_ids);
        return response; // Deliberately ignores AbortSignal to exercise late-response fencing.
      }
      throw new Error(`Unexpected Library request ${url.pathname}`);
    })
  );
  return { requests, release };
}
async function mount() {
  return renderWithI18n(
    <MemoryRouter initialEntries={['/library']}>
      <Navigation />
      <Routes>
        <Route path='/library' element={<LibraryPage />} />
      </Routes>
    </MemoryRouter>,
    'en-US'
  );
}
async function selectPage() {
  const checkbox = await screen.findByRole('checkbox', { name: 'Select this page' });
  await waitFor(() => expect(checkbox).toBeEnabled());
  fireEvent.click(checkbox);
}
async function startExport() {
  const trigger = await screen.findByRole('button', { name: 'Export references' });
  fireEvent.click(trigger);
  fireEvent.click(await screen.findByText('BibTeX (.bib)'));
  return trigger;
}
describe('Selected Library BibTeX export through the real page and HTTP adapter', () => {
  let blobs: Blob[];
  let clicks: string[];
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('export-owner');
    localStorage.clear();
    installLiteratureLocks();
    blobs = [];
    clicks = [];
    vi.stubGlobal('fetch', vi.fn());
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn((blob: Blob) => {
        blobs.push(blob);
        return 'blob:references';
      }),
    });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () {
      clicks.push(this.download);
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
  it('exports every explicitly selected ID once, keeps loading until the complete reply, and releases the Blob URL', async () => {
    const http = catalogue();
    await mount();
    await selectPage();
    await startExport();
    await waitFor(() => expect(http.requests).toEqual([['one', 'two']]));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Export references' })).toBeDisabled());
    fireEvent.click(screen.getByRole('button', { name: 'Export references' }));
    expect(http.requests).toHaveLength(1);
    expect(clicks).toEqual([]);
    const bibtex = '@misc{x_one,\n  title = {Paper one}\n}\n\n@misc{x_two,\n  title = {Paper two}\n}\n';
    await act(async () =>
      http.release(json({ schema: 'x-science.literature-bibtex.v1', item_ids: ['one', 'two'], bibtex }))
    );
    await waitFor(() => expect(clicks).toEqual(['selected-references.bib']));
    expect(blobs).toHaveLength(1);
    expect(blobs[0].type).toBe('application/x-bibtex;charset=utf-8');
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:references');
    expect(screen.getByText('Exported')).toBeInTheDocument();
  });
  it.each(['scope', 'account'] as const)(
    'does not download partial/error replies or a late response after %s change, and supports retry',
    async (boundary) => {
      const http = catalogue();
      const rendered = await mount();
      await selectPage();
      await startExport();
      await waitFor(() => expect(http.requests).toHaveLength(1));
      await act(async () => http.release(json({ code: 'LITERATURE_UNAVAILABLE' }, 404)));
      expect(await screen.findByRole('alert')).toHaveTextContent('Could not export');
      expect(clicks).toEqual([]);
      const next = catalogue();
      await startExport();
      await waitFor(() => expect(next.requests).toHaveLength(1));
      if (boundary === 'scope') fireEvent.click(screen.getByRole('button', { name: 'Change scope' }));
      await act(async () => {
        if (boundary === 'account') setRendererAccountOwner('another-owner');
        next.release(
          json({
            schema: 'x-science.literature-bibtex.v1',
            item_ids: ['one', 'two'],
            bibtex: '@misc{x_one, title={Old}}',
          })
        );
      });
      expect(clicks).toEqual([]);
      expect(blobs).toEqual([]);
      await act(async () => {
        await rendered.i18n.changeLanguage('zh-CN');
      });
      expect(screen.queryByRole('alert')).toBeNull();
    }
  );
});
