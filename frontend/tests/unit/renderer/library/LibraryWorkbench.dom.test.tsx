import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { SiderLibraryEntry } from '@/renderer/components/layout/Sider/SiderNav/SiderLibraryEntry';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';

const item = (id = 'a', title = 'Paper A'): LiteratureItem => ({
  id,
  title,
  metadata_revision: 1,
  metadata: { authors: ['Ada', 'Grace'], year: 2025 },
  deleted: false,
  project_ids: [],
  attachments: [],
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const page = (items: LiteratureItem[], next_cursor?: string) => ({
  schema: 'x-science.literature-page.v1',
  items,
  ...(next_cursor ? { next_cursor } : {}),
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function mount(path = '/library', onNavigate?: () => void) {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path='/guid' element={<SiderLibraryEntry onNavigate={onNavigate} />} />
        <Route path='/library' element={<LibraryPage />} />
      </Routes>
    </MemoryRouter>,
    'en-US'
  );
}
const projects = () => json({ projects: [{ project_id: 'project', name: 'Research project' }] });
const collectionReads = (url: URL) => {
  if (url.pathname === '/api/literature/collections')
    return json({ schema: 'x-science.literature-collections.v1', collections: [] });
  const membership = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/collections$/);
  if (membership)
    return json({
      schema: 'x-science.literature-collection-membership.v1',
      item_id: decodeURIComponent(membership[1]),
      collection_ids: [],
    });
  return undefined;
};

describe('Library workbench entry and original HTTP owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('navigates from the real Library entry and reads all catalogue items without Reading PDF filters', async () => {
    const requests: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost');
        requests.push(url.pathname + url.search);
        const collections = collectionReads(url);
        if (collections) return collections;
        if (url.pathname === '/api/projects') return projects();
        if (url.pathname === '/api/literature/items')
          return json(url.searchParams.has('cursor') ? page([item('b', 'Paper B')]) : page([item()], 'next-page'));
        if (url.pathname === '/api/literature/items/a') return json(item('a', 'Current persisted title'));
        throw new Error('Unexpected fixture request');
      })
    );
    const onNavigate = vi.fn();
    await mount('/guid', onNavigate);
    fireEvent.click(screen.getByRole('link', { name: 'Library' }));
    expect(onNavigate).toHaveBeenCalledTimes(1);
    // This item intentionally has no PDF: Library is not the Reading picker.
    fireEvent.click(await screen.findByRole('button', { name: /Paper A/ }));
    expect(await screen.findByRole('heading', { name: 'Current persisted title' })).toBeInTheDocument();
    expect(requests).toContain('/api/literature/items/a');
    expect(
      within(screen.getByRole('region', { name: 'Literature details' })).getByText('Ada; Grace')
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    expect(await screen.findByRole('button', { name: /Paper B/ })).toBeInTheDocument();
    expect(requests.some((path) => path.includes('cursor=next-page'))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Back to first page' }));
    expect(await screen.findByRole('button', { name: /Paper A/ })).toBeInTheDocument();
  });

  it('does not publish an older query or selected detail after route state changes', async () => {
    const oldQuery = deferred<Response>();
    const oldDetail = deferred<Response>();
    let queryStarted = false;
    let detailStarted = false;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost');
        const collections = collectionReads(url);
        if (collections) return collections;
        if (url.pathname === '/api/projects') return projects();
        if (url.pathname === '/api/literature/items') {
          if (url.searchParams.get('query') === 'old') {
            queryStarted = true;
            return oldQuery.promise;
          }
          return json(page([item(), item('b', 'Paper B')]));
        }
        if (url.pathname === '/api/literature/items/a') {
          detailStarted = true;
          return oldDetail.promise;
        }
        if (url.pathname === '/api/literature/items/b') return json(item('b', 'Current B detail'));
        throw new Error('Unexpected fixture request');
      })
    );
    await mount();
    const search = screen.getByRole('searchbox', { name: 'Search Library' });
    fireEvent.change(search, { target: { value: 'old' } });
    await waitFor(() => expect(queryStarted).toBe(true));
    fireEvent.change(search, { target: { value: 'new' } });
    fireEvent.click(await screen.findByRole('button', { name: /Paper A/ }));
    await waitFor(() => expect(detailStarted).toBe(true));
    fireEvent.click(screen.getByRole('button', { name: /Paper B/ }));
    expect(await screen.findByRole('heading', { name: 'Current B detail' })).toBeInTheDocument();
    await act(async () => {
      oldQuery.resolve(json(page([item('old', 'Late old query')])));
      oldDetail.resolve(json(item('a', 'Late A detail')));
      await Promise.all([oldQuery.promise, oldDetail.promise]);
    });
    expect(screen.queryByText('Late old query')).toBeNull();
    expect(screen.queryByText('Late A detail')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Current B detail' })).toBeInTheDocument();
  });

  it('fences a late catalogue response from the previous account', async () => {
    const old = deferred<Response>();
    let started = false;
    let nextOwner = false;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost');
        const collections = collectionReads(url);
        if (collections) return collections;
        if (url.pathname === '/api/projects') return projects();
        if (url.pathname === '/api/literature/items') {
          if (!nextOwner) {
            started = true;
            return old.promise;
          }
          return json(page([item('next', 'Next owner paper')]));
        }
        throw new Error('Unexpected fixture request');
      })
    );
    await mount();
    await waitFor(() => expect(started).toBe(true));
    act(() => {
      nextOwner = true;
      setRendererAccountOwner('next-library-owner');
    });
    expect(await screen.findByRole('button', { name: /Next owner paper/ })).toBeInTheDocument();
    await act(async () => {
      old.resolve(json(page([item('old', 'Previous owner paper')])));
      await old.promise;
    });
    expect(screen.queryByText('Previous owner paper')).toBeNull();
  });

  it('bounds a stalled project read and retries without blocking the persisted literature detail', async () => {
    vi.useFakeTimers();
    let stalled = true;
    const linked = { ...item(), project_ids: ['project'] };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), 'http://localhost');
        const collections = collectionReads(url);
        if (collections) return collections;
        if (url.pathname === '/api/projects') {
          if (!stalled) return projects();
          return new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => reject(init?.signal?.reason), { once: true });
          });
        }
        if (url.pathname === '/api/literature/items') return json(page([linked]));
        if (url.pathname === '/api/literature/items/a') return json(linked);
        throw new Error('Unexpected fixture request');
      })
    );
    await mount('/library?item=a');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByRole('heading', { name: 'Paper A' })).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(screen.getAllByText('Project names are unavailable. Metadata can still be viewed.')).toHaveLength(2);
    expect(screen.getByRole('heading', { name: 'Paper A' })).toBeInTheDocument();
    stalled = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(
      within(screen.getByRole('region', { name: 'Literature details' })).getByText('Research project')
    ).toBeInTheDocument();
  });

  it('retries the original metadata request, then reads back membership and deletion through the shared owner', async () => {
    let failing = true;
    let empty = true;
    let current = item();
    let lost = true;
    const writes: string[] = [];
    const receipts = new Map<string, unknown>();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), 'http://localhost');
        const collections = collectionReads(url);
        if (collections) return collections;
        if (url.pathname === '/api/projects') return projects();
        if (url.pathname === '/api/literature/items') {
          if (failing) return json({ code: 'UNAVAILABLE' }, 503);
          return json(page(empty || current.deleted ? [] : [current]));
        }
        if (url.pathname === '/api/literature/items/a' && init?.method === 'GET') return json(current);
        if (url.pathname === '/api/literature/items/a' && init?.method === 'PATCH') {
          const raw = String(init.body);
          writes.push(raw);
          const command = JSON.parse(raw);
          if (!receipts.has(command.operation_id)) {
            current = {
              ...current,
              ...(typeof command.title === 'string' ? { title: command.title } : {}),
              ...(typeof command.deleted === 'boolean' ? { deleted: command.deleted } : {}),
              metadata_revision: current.metadata_revision + 1,
            };
            receipts.set(command.operation_id, {
              schema: 'x-science.literature-receipt.v1',
              operation_id: command.operation_id,
              item: current,
            });
          }
          if (lost) {
            lost = false;
            throw new TypeError('controlled lost response');
          }
          return json(receipts.get(command.operation_id));
        }
        if (url.pathname === '/api/literature/items/a/projects/project' && init?.method === 'PUT') {
          const command = JSON.parse(String(init.body));
          current = { ...current, project_ids: command.included ? ['project'] : [] };
          return json({ schema: 'x-science.literature-receipt.v1', operation_id: command.operation_id, item: current });
        }
        throw new Error('Unexpected fixture request');
      })
    );
    const rendered = await mount();
    expect(await screen.findByText('Could not load Library')).toBeInTheDocument();
    failing = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Your Library is empty')).toBeInTheDocument();
    empty = false;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    fireEvent.click(await screen.findByRole('button', { name: /Paper A/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'Edit details' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: 'Updated paper' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/receipt is unknown/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('heading', { name: 'Updated paper' })).toBeInTheDocument();
    expect(writes).toHaveLength(2);
    expect(writes[0]).toBe(writes[1]);
    expect(current.metadata_revision).toBe(2);
    fireEvent.click(await screen.findByRole('button', { name: 'Edit details' }));
    const membership = screen.getByRole('combobox', { name: 'Project membership' });
    await waitFor(() => expect(membership).toBeEnabled());
    fireEvent.change(membership, { target: { value: 'project' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add to project' }));
    await waitFor(() => expect(current.project_ids).toEqual(['project']));
    await waitFor(() => expect(screen.queryByRole('combobox', { name: 'Project membership' })).toBeNull());
    await waitFor(() =>
      expect(
        within(screen.getByRole('region', { name: 'Literature details' })).getByText('Research project')
      ).toBeInTheDocument()
    );
    fireEvent.click(screen.getByRole('button', { name: 'Edit details' }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove from Library' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm removal' }));
    expect(await screen.findByText('Your Library is empty')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Updated paper' })).toBeNull();
    expect(current.deleted).toBe(true);
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('heading', { name: '文献库', level: 1 })).toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: '搜索文献库' })).toBeInTheDocument();
  });
});
