import React from 'react';
import { cleanup, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router';
import userEvent from '@testing-library/user-event';
import { readFileSync } from 'node:fs';
import { URL as NodeURL } from 'node:url';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { LIBRARY_COLUMN_PREFERENCES_SETTING } from '@/renderer/services/runtime/libraryColumnPreferences';
import { DEFAULT_LIBRARY_COLUMN_PREFERENCES } from '@/common/adapter/libraryColumnPreferences';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
import sortControlCSS from '@/renderer/pages/library/libraryWorkbench.header.css?inline';
import { installCollectionHTTP, json as fixtureJson, mountCollections, paper } from './libraryCollectionsFixtures';
import '@arco-design/web-react/lib/_util/react-19-adapter';

let sortStyles: HTMLStyleElement;
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('library-rating-sort-owner');
  localStorage.clear();
  installLiteratureLocks();
  sortStyles = document.createElement('style');
  const css =
    sortControlCSS ||
    readFileSync(
      new NodeURL(
        '../../../../packages/desktop/src/renderer/pages/library/libraryWorkbench.header.css',
        import.meta.url
      ),
      'utf8'
    );
  sortStyles.textContent = css.slice(css.indexOf('.library-sort-control'));
  document.head.append(sortStyles);
});
afterEach(() => {
  cleanup();
  sortStyles.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const items = Array.from({ length: 30 }, (_, index): LiteratureItem => ({
  id: `rating-${index}`,
  title: `Rated reference ${String(index).padStart(2, '0')}`,
  metadata_revision: 1,
  metadata: { rating: index % 6 },
  rating_view: { known: true, value: index % 6 },
  reading_status: 'unread',
  deleted: false,
  project_ids: [],
  attachments: [],
}));
// A fixed server page fixture differs from title order; the Page must preserve it.
const ranked = [
  5, 11, 17, 23, 29, 4, 10, 16, 22, 28, 3, 9, 15, 21, 27, 2, 8, 14, 20, 26, 1, 7, 13, 19, 25, 0, 6, 12, 18, 24,
].map((index) => items[index]);
function LocationProbe() {
  return <output aria-label='Rating sort location'>{useLocation().search}</output>;
}
function locationParams() {
  return new URLSearchParams(screen.getByLabelText('Rating sort location').textContent ?? '');
}
function visibleTitles() {
  return screen
    .getAllByRole('button', { name: /^Rated reference / })
    .filter((button) => button.matches('button.library-table__title'))
    .map((button) => button.textContent);
}

function installPageReader() {
  const requests: URL[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects') return json({ projects: [] });
      if (url.pathname === '/api/literature/collections')
        return json({ schema: 'x-science.literature-collections.v1', collections: [] });
      if (url.pathname !== '/api/literature/items') throw new Error(`Unexpected rating sort request ${url.pathname}`);
      requests.push(url);
      const sorted = url.searchParams.get('sort') === 'rating-desc';
      const offset = url.searchParams.has('cursor') ? 25 : 0;
      return json({
        schema: 'x-science.literature-page.v1',
        items: (sorted ? ranked : items).slice(offset, offset + 25),
        total_count: items.length,
        ...(offset === 0 ? { next_cursor: sorted ? 'rating-after-25' : 'title-after-25' } : {}),
      });
    })
  );
  return requests;
}
const mount = (path: string, language: 'en-US' | 'zh-CN' = 'en-US') =>
  renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <LibraryPage />
      <LocationProbe />
    </MemoryRouter>,
    language
  );

it.each([
  { language: 'en-US' as const, sort: 'Sort references', highest: 'Highest rated' },
  { language: 'zh-CN' as const, sort: '文献排序', highest: '评分从高到低' },
])('connects Highest rated and clears the previous page in $language', async (labels) => {
  const requests = installPageReader();
  const user = userEvent.setup();
  await mount('/library?q=Rated&page_size=25&sort=title-asc&cursor=title-after-25', labels.language);
  await screen.findByRole('button', { name: 'Rated reference 29' });
  const control = screen.getByRole('combobox', { name: labels.sort });
  await waitFor(() => expect(control).not.toHaveAttribute('aria-disabled', 'true'));
  await user.click(control);
  const menu = await screen.findByRole('listbox');
  await user.click(within(menu).getByRole('option', { name: labels.highest }));
  await waitFor(() => expect(visibleTitles()).toEqual(ranked.slice(0, 25).map((item) => item.title)));
  expect(requests.at(-1)?.searchParams.get('sort')).toBe('rating-desc');
  expect(requests.at(-1)?.searchParams.get('limit')).toBe('25');
  expect(requests.at(-1)?.searchParams.has('cursor')).toBe(false);
  expect(locationParams().get('q')).toBe('Rated');
  expect(locationParams().get('page_size')).toBe('25');
  expect(locationParams().get('sort')).toBe('rating-desc');
  expect(locationParams().has('cursor')).toBe(false);
});

it('keeps server order across rating page navigation and URL restoration', async () => {
  const requests = installPageReader();
  const user = userEvent.setup();
  let rendered = await mount('/library?q=Rated&page_size=25&sort=rating-desc');
  await screen.findByRole('button', { name: 'Rated reference 29' });
  expect(visibleTitles()).toEqual(ranked.slice(0, 25).map((item) => item.title));
  await user.click(screen.getByRole('button', { name: 'Next page' }));
  await waitFor(() => expect(visibleTitles()).toEqual(ranked.slice(25).map((item) => item.title)));
  expect(locationParams().get('cursor')).toBe('rating-after-25');
  const search = screen.getByLabelText('Rating sort location').textContent ?? '';
  rendered.unmount();
  rendered = await mount(`/library${search}`);
  await screen.findByRole('button', { name: 'Rated reference 00' });
  expect(screen.getByRole('combobox', { name: 'Sort references' })).toHaveTextContent('Highest rated');
  expect(requests.at(-1)?.searchParams.get('sort')).toBe('rating-desc');
  expect(requests.at(-1)?.searchParams.get('cursor')).toBe('rating-after-25');
  expect(visibleTitles()).toEqual(ranked.slice(25).map((item) => item.title));
});

function installInlineRatingOrder(staleAfterSave = false) {
  const rated = (id: string, title: string, value: number, revision = 1): LiteratureItem => ({
    ...paper(id, title),
    metadata_revision: revision,
    metadata: { rating: value, opaque: 'Keep this value' },
    rating_view: { known: true, value },
    reading_status: 'read',
  });
  let item = rated('a', 'Paper A', 1);
  const other = rated('b', 'Paper B', 3);
  const patches: Array<{
    operation_id: string;
    expected_revision: number;
    metadata_patch: { set: { rating: number } };
  }> = [];
  const ordered = () => (item.rating_view?.known && item.rating_view.value > 3 ? [item, other] : [other, item]);
  const http = installCollectionHTTP({
    collections: () => [],
    items: ordered,
    detail: (id) => (id === item.id ? item : other),
    intercept: (url, init) => {
      const method = init.method ?? 'GET';
      if (url.pathname === '/api/settings/client')
        return fixtureJson({
          [LIBRARY_COLUMN_PREFERENCES_SETTING]: {
            version: 3,
            order: [...DEFAULT_LIBRARY_COLUMN_PREFERENCES.order, 'notes', 'rating'],
            visible: [...DEFAULT_LIBRARY_COLUMN_PREFERENCES.visible, 'rating'],
          },
        });
      if (url.pathname === '/api/literature/items' && method === 'GET') {
        expect(url.searchParams.get('sort')).toBe('rating-desc');
        expect(url.searchParams.get('query')).toBe('Paper');
        expect(url.searchParams.get('reading_status')).toBe('read');
        if (staleAfterSave && patches.length && url.searchParams.has('cursor'))
          // A concurrent writer changed the preceding page's rating anchor.
          return fixtureJson({ code: 'LITERATURE_CURSOR_STALE' }, 409);
        return fixtureJson({ schema: 'x-science.literature-page.v1', items: ordered(), total_count: 2 });
      }
      if (method !== 'PATCH' || url.pathname !== '/api/literature/items/a') return undefined;
      const command = JSON.parse(String(init.body)) as (typeof patches)[number];
      expect(command.expected_revision).toBe(item.metadata_revision);
      expect(Object.keys(command).toSorted()).toEqual(['expected_revision', 'metadata_patch', 'operation_id']);
      patches.push(command);
      item = rated(item.id, item.title, command.metadata_patch.set.rating, item.metadata_revision + 1);
      return fixtureJson({ schema: 'x-science.literature-receipt.v1', operation_id: command.operation_id, item });
    },
  });
  return { http, patches, current: () => item };
}

const inlineTitles = () =>
  screen
    .getAllByRole('button', { name: /^Paper [AB]$/ })
    .filter((button) => button.matches('button.library-table__title'))
    .map((button) => button.textContent);
const inlineStars = () => within(screen.getByRole('group', { name: 'Rating for Paper A' }));

it('rereads Highest rated after inline save and clear without losing filters or server order', async () => {
  const fixture = installInlineRatingOrder();
  await mountCollections('/library?q=Paper&reading_status=read&page_size=25&sort=rating-desc');
  await screen.findByRole('button', { name: 'Paper A' });
  expect(inlineTitles()).toEqual(['Paper B', 'Paper A']);
  const user = userEvent.setup();
  await waitFor(() => expect(inlineStars().getByRole('button', { name: '5 stars' })).toBeEnabled());
  await user.click(inlineStars().getByRole('button', { name: '5 stars' }));
  await waitFor(() => expect(inlineTitles()).toEqual(['Paper A', 'Paper B']));
  await waitFor(() => expect(inlineStars().getByRole('button', { name: '5 stars' })).toBeEnabled());
  expect(inlineStars().getByRole('button', { name: '5 stars' })).toHaveAttribute('aria-pressed', 'true');
  await user.click(inlineStars().getByRole('button', { name: '5 stars' }));
  await waitFor(() => expect(inlineTitles()).toEqual(['Paper B', 'Paper A']));
  await waitFor(() => expect(inlineStars().getByRole('button', { name: '5 stars' })).toBeEnabled());
  expect(inlineStars().getByRole('button', { name: '5 stars' })).toHaveAttribute('aria-pressed', 'false');
  expect(fixture.patches.map((command) => command.metadata_patch.set.rating)).toEqual([5, 0]);
  expect(fixture.patches.map((command) => command.expected_revision)).toEqual([1, 2]);
  expect(new Set(fixture.patches.map((command) => command.operation_id)).size).toBe(2);
  expect(
    fixture.http.requests.filter(
      (request) => request.method === 'GET' && request.path.startsWith('/api/literature/items?')
    ).length
  ).toBeGreaterThanOrEqual(3);
  expect(fixture.current().metadata.opaque).toBe('Keep this value');
  expect(fixture.current().reading_status).toBe('read');
});

it('retains the successful inline receipt when a rating cursor goes stale and recovers without another PATCH', async () => {
  const fixture = installInlineRatingOrder(true);
  const rendered = await mountCollections(
    '/library?q=Paper&reading_status=read&page_size=25&sort=rating-desc&cursor=rating-after-anchor'
  );
  await screen.findByRole('button', { name: 'Paper A' });
  await waitFor(() => expect(inlineStars().getByRole('button', { name: '5 stars' })).toBeEnabled());
  await userEvent.setup().click(inlineStars().getByRole('button', { name: '5 stars' }));
  const firstPage = await screen.findByRole('button', { name: 'Return to first page' });
  await screen.findByText(rendered.i18n.t('conversation.reading.libraryUpdated', { name: 'Paper A' }));
  const owner = getLiteratureMutationController(getRendererAccountOwnerId());
  expect(owner.getSnapshot().error).toBeNull();
  expect(owner.getSnapshot().pending).toBeNull();
  expect(owner.getSnapshot().receipt?.operation_id).toBe(fixture.patches[0].operation_id);
  expect(screen.queryByText(rendered.i18n.t('conversation.reading.libraryRejected'))).toBeNull();
  expect(screen.queryByText(rendered.i18n.t('conversation.libraryRating.uncertain'))).toBeNull();
  expect(fixture.patches).toHaveLength(1);
  await userEvent.setup().click(firstPage);
  await waitFor(() => expect(inlineTitles()).toEqual(['Paper A', 'Paper B']));
  const params = new URLSearchParams(screen.getByLabelText('Library location').textContent ?? '');
  expect(params.get('sort')).toBe('rating-desc');
  expect(params.get('q')).toBe('Paper');
  expect(params.get('reading_status')).toBe('read');
  expect(params.get('page_size')).toBe('25');
  expect(params.has('cursor')).toBe(false);
  expect(fixture.patches).toHaveLength(1);
  expect(owner.getSnapshot().receipt?.operation_id).toBe(fixture.patches[0].operation_id);
});
