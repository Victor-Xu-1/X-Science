import React from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureCollection } from '@/common/adapter/literatureCollections';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { renderWithI18n } from '../../i18nTestUtils';

export const paper = (id = 'a', title = 'Paper A'): LiteratureItem => ({
  id,
  title,
  metadata_revision: 1,
  metadata: { authors: ['Ada'], year: 2025 },
  deleted: false,
  project_ids: [],
  attachments: [],
});
export const collection = (id: string, name: string, parent_id: string | null = null): LiteratureCollection => ({
  id,
  name,
  parent_id,
  description: '',
  revision: 1,
  sort_order: 0,
  item_count: 0,
});
export const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
export const page = (items: LiteratureItem[], next_cursor?: string) => ({
  schema: 'x-science.literature-page.v1',
  items,
  ...(next_cursor ? { next_cursor } : {}),
});
export function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
function Location() {
  const location = useLocation();
  return <output aria-label='Library location'>{location.search}</output>;
}
export function mountCollections(path = '/library') {
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
type ReadOptions = {
  collections: () => LiteratureCollection[];
  items?: (url: URL) => LiteratureItem[];
  detail?: (id: string) => LiteratureItem;
  memberships?: (id: string) => string[];
  intercept?: (url: URL, init: RequestInit) => Response | undefined | Promise<Response | undefined>;
};
/** Controlled HTTP responses exercise the real Web reader/mutation owner, not a Store fixture. */
export function installCollectionHTTP(options: ReadOptions) {
  const requests: { path: string; method: string; body?: string }[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    const request = init ?? {};
    const method = request.method ?? 'GET';
    requests.push({ path: url.pathname + url.search, method, ...(request.body ? { body: String(request.body) } : {}) });
    const intercepted = await options.intercept?.(url, request);
    if (intercepted) return intercepted;
    if (method !== 'GET') throw new Error('Unexpected collection fixture mutation');
    if (url.pathname === '/api/projects') return json({ projects: [] });
    if (url.pathname === '/api/literature/collections')
      return json({ schema: 'x-science.literature-collections.v1', collections: options.collections() });
    if (url.pathname === '/api/literature/items') return json(page(options.items?.(url) ?? [paper()]));
    const membership = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/collections$/);
    if (membership) {
      const id = decodeURIComponent(membership[1]);
      return json({
        schema: 'x-science.literature-collection-membership.v1',
        item_id: id,
        collection_ids: options.memberships?.(id) ?? [],
      });
    }
    const item = url.pathname.match(/^\/api\/literature\/items\/([^/]+)$/);
    if (item) return json(options.detail?.(decodeURIComponent(item[1])) ?? paper(decodeURIComponent(item[1])));
    throw new Error('Unexpected collection fixture request');
  });
  vi.stubGlobal('fetch', fetch);
  return { requests, fetch };
}
export const collectionAck = (operation_id: string, action: 'create' | 'update', value: LiteratureCollection) => ({
  schema: 'x-science.literature-collection-receipt.v1',
  operation_id,
  action,
  collection_id: value.id,
  collection: value,
});
