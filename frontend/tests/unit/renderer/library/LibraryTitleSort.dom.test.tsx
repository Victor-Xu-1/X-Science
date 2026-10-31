import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
import userEvent from '@testing-library/user-event';
import { readFileSync } from 'node:fs';
import { URL as NodeURL } from 'node:url';
import sortControlCSS from '@/renderer/pages/library/libraryWorkbench.header.css?inline';

// Arco's public handler uses the deprecated numeric fields supplied by real
// browser keys; user-event intentionally leaves them zero in jsdom.
function pressSortKey(key: string, keyCode: number) {
  fireEvent.keyDown(document.activeElement!, { key, code: key === ' ' ? 'Space' : key, keyCode, which: keyCode });
}

let sortStyles: HTMLStyleElement;
function installSortStyles() {
  sortStyles = document.createElement('style');
  // Vitest may stub CSS imports. Read the same source only in that test mode;
  // keep the static import above so related-test discovery retains this edge.
  const css =
    sortControlCSS ||
    readFileSync(
      new NodeURL(
        '../../../../packages/desktop/src/renderer/pages/library/libraryWorkbench.header.css',
        import.meta.url
      ),
      'utf8'
    );
  expect(css.length).toBeGreaterThan(0);
  expect(css).toContain('.library-sort-popup-transition-appear');
  sortStyles.textContent = css.slice(css.indexOf('.library-sort-control'));
  document.head.append(sortStyles);
}
async function openSortMenu(user: ReturnType<typeof userEvent.setup>, name = 'Sort references') {
  const control = screen.getByRole('combobox', { name });
  await waitFor(() => expect(control).not.toHaveAttribute('aria-disabled', 'true'));
  await user.click(control);
  const menu = await screen.findByRole('listbox');
  expect(control).toHaveAttribute('aria-expanded', 'true');
  expect(menu.closest('.library-sort-popup')).not.toHaveStyle({ pointerEvents: 'none' });
  return menu;
}
async function chooseSort(user: ReturnType<typeof userEvent.setup>, label: string, name = 'Sort references') {
  const menu = await openSortMenu(user, name);
  await user.click(within(menu).getByRole('option', { name: label }));
  await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
}

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const items = Array.from({ length: 60 }, (_, index): LiteratureItem => ({
  id: `paper-${String(index + 1).padStart(2, '0')}`,
  title: `Sort reference ${String(index + 1).padStart(2, '0')}`,
  metadata_revision: 1,
  metadata: {},
  reading_status: 'read',
  deleted: false,
  project_ids: [],
  attachments: [],
}));

function LocationProbe() {
  const { search } = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output aria-label='Library location'>{search}</output>
      <button type='button'>Outside sort</button>
      <button onClick={() => void navigate('/library?q=Sort&page_size=50&sort=title-asc')}>Open ascending visit</button>
      <button onClick={() => void navigate('/library?q=Sort&page_size=50&sort=title-desc')}>
        Open descending visit
      </button>
    </>
  );
}
async function mount(path = '/library?q=Sort&page_size=50') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <LibraryPage />
      <LocationProbe />
    </MemoryRouter>,
    'en-US'
  );
}
function location() {
  return new URLSearchParams(screen.getByLabelText('Library location').textContent ?? '');
}
function installCatalogue({ staleNext = false, holdFirstStale = false } = {}) {
  const pages: URL[] = [];
  const signals: Array<AbortSignal | null | undefined> = [];
  const allIds: URL[] = [];
  const exports: Array<{ item_ids: string[]; format: string }> = [];
  let releaseFirst: (() => void) | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects') return json({ projects: [] });
      if (url.pathname === '/api/literature/collections')
        return json({ schema: 'x-science.literature-collections.v1', collections: [] });
      if (url.pathname === '/api/literature/items' && url.searchParams.has('all_item_ids')) {
        allIds.push(url);
        return json({
          schema: 'x-science.literature-selection.v1',
          item_ids: items.map((item) => item.id),
          total_count: items.length,
        });
      }
      if (url.pathname === '/api/literature/items') {
        pages.push(url);
        signals.push(init?.signal);
        if (holdFirstStale && pages.length === 1) {
          await new Promise<void>((resolve) => {
            releaseFirst = resolve;
          });
          return json({ code: 'LITERATURE_CURSOR_STALE' }, 409);
        }
        if (staleNext && url.searchParams.has('cursor')) return json({ code: 'LITERATURE_CURSOR_STALE' }, 409);
        const sort = url.searchParams.get('sort');
        // The original reader must render the server's order without sorting the current page.
        const ordered =
          sort === 'title-asc'
            ? items
            : sort === 'title-desc'
              ? [...items].reverse()
              : [items[59], ...items.slice(0, 59)];
        const offset = url.searchParams.has('cursor') ? 50 : 0;
        const limit = Number(url.searchParams.get('limit'));
        return json({
          schema: 'x-science.literature-page.v1',
          items: ordered.slice(offset, offset + limit),
          total_count: items.length,
          ...(offset + limit < items.length ? { next_cursor: 'after-50' } : {}),
        });
      }
      if (url.pathname === '/api/literature/export-references' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body)) as { item_ids: string[]; format: string };
        exports.push(body);
        return json({
          schema: 'x-science.literature-export.v1',
          ...body,
          content: body.item_ids.map((id) => `TY  - JOUR\nTI  - ${id}\nER  - `).join('\n\n'),
        });
      }
      throw new Error(`Unexpected title-sort fixture request ${url.pathname}`);
    })
  );
  return { pages, signals, allIds, exports, releaseFirst: () => releaseFirst?.() };
}

describe('Library title order through the original URL and HTTP owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-title-sort-owner');
    localStorage.clear();
    installLiteratureLocks();
    installSortStyles();
  });
  afterEach(() => {
    cleanup();
    sortStyles.remove();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('changes global order, resets the cursor and restores URL state', async () => {
    const http = installCatalogue();
    const user = userEvent.setup();
    let rendered = await mount();
    await screen.findByRole('button', { name: 'Sort reference 60' });
    expect(http.pages[0].searchParams.has('sort')).toBe(false);
    const sort = screen.getByRole('combobox', { name: 'Sort references' });
    const menu = await openSortMenu(user);
    expect(
      within(menu)
        .getAllByRole('option')
        .map((option) => option.textContent)
    ).toEqual([
      'Recently updated',
      'Recently added',
      'First added',
      'Title: A–Z',
      'Title: Z–A',
      'Year: newest first',
      'Year: oldest first',
      'Current order',
    ]);
    expect(within(menu).getByRole('option', { name: 'Current order' })).toHaveAttribute('aria-selected', 'true');
    pressSortKey('Escape', 27);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(sort).toHaveFocus();
    expect(sort).toHaveTextContent(/^Current order$/);
    for (const [key, keyCode] of [
      ['ArrowDown', 40],
      ['ArrowUp', 38],
      [' ', 32],
    ] as const) {
      pressSortKey(key, keyCode);
      const opened = await screen.findByRole('listbox');
      expect(opened.closest('.library-sort-popup')).not.toHaveStyle({ pointerEvents: 'none' });
      pressSortKey('Escape', 27);
      await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
      expect(sort).toHaveFocus();
    }
    await openSortMenu(user);
    const outside = screen.getByRole('button', { name: 'Outside sort' });
    await user.click(outside);
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
    expect(outside).toHaveFocus();
    expect(http.pages).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    await screen.findByRole('button', { name: 'Sort reference 59' });
    await chooseSort(user, 'Current order');
    expect(http.pages).toHaveLength(2);
    expect(location().get('cursor')).toBe('after-50');
    await chooseSort(user, 'Title: A–Z');
    await screen.findByRole('button', { name: 'Sort reference 01' });
    expect(http.pages.at(-1)?.searchParams.get('sort')).toBe('title-asc');
    expect(http.pages.at(-1)?.searchParams.has('cursor')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    await screen.findByRole('button', { name: 'Sort reference 60' });
    sort.focus();
    pressSortKey(' ', 32);
    await screen.findByRole('listbox');
    pressSortKey('ArrowDown', 40);
    pressSortKey(' ', 32);
    await screen.findByRole('button', { name: 'Sort reference 11' });
    expect(location().get('q')).toBe('Sort');
    expect(location().get('page_size')).toBe('50');
    expect(location().get('sort')).toBe('title-desc');
    expect(location().has('cursor')).toBe(false);
    const search = screen.getByLabelText('Library location').textContent ?? '';
    rendered.unmount();
    rendered = await mount(`/library${search}`);
    await screen.findByRole('button', { name: 'Sort reference 11' });
    expect(screen.getByRole('combobox', { name: 'Sort references' })).toHaveTextContent(/^Title: Z–A$/);
  }, 30_000);

  it('clears old sort selection and range while keeping selected export membership separate', async () => {
    const http = installCatalogue();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:title-sort') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    const downloads = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    await mount('/library?q=Sort&page_size=50&sort=title-desc');
    await screen.findByRole('button', { name: 'Sort reference 11' });
    const chosen = screen.getByRole('checkbox', { name: 'Select Sort reference 40' });
    await waitFor(() => expect(chosen).toBeEnabled());
    fireEvent.click(chosen);
    expect(screen.queryByRole('combobox', { name: 'Sort references' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Open ascending visit' }));
    await screen.findByRole('button', { name: 'Sort reference 01' });
    expect(screen.getByRole('checkbox', { name: 'Select Sort reference 40' })).not.toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Sort reference 45' }), { shiftKey: true });
    expect(screen.getByRole('checkbox', { name: 'Select Sort reference 41' })).not.toBeChecked();
    expect(screen.getByText('1 selected')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select this page' }));
    fireEvent.click(screen.getByRole('button', { name: 'Select all matching references' }));
    fireEvent.click(screen.getByRole('button', { name: 'Export references' }));
    const ris = await screen.findByText('RIS (.ris)');
    await waitFor(() => expect(ris.closest('[role="menuitem"]')).not.toHaveAttribute('aria-disabled', 'true'));
    fireEvent.click(ris);
    await waitFor(() => expect(downloads).toHaveBeenCalledTimes(1));
    expect(http.allIds).toHaveLength(1);
    // Selected-all resolves original membership order; scope export has its separate title-order caller.
    expect(http.allIds[0].searchParams.has('order')).toBe(false);
    for (const key of ['sort', 'cursor', 'limit']) expect(http.allIds[0].searchParams.has(key)).toBe(false);
    expect(http.exports.flatMap((body) => body.item_ids)).toEqual(items.map((item) => item.id));
  }, 30_000);

  it('offers explicit first-page recovery for a stale anchor, preserving filters and bilingual order controls', async () => {
    const http = installCatalogue({ staleNext: true });
    const rendered = await mount('/library?q=Sort&sort=title-asc&reading_status=read&page_size=50');
    await screen.findByRole('button', { name: 'Sort reference 01' });
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    const recover = await screen.findByRole('button', { name: 'Return to first page' });
    expect(screen.queryByRole('button', { name: 'Sort reference 01' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
    expect(http.pages).toHaveLength(2);
    expect(location().get('cursor')).toBe('after-50');
    fireEvent.click(recover);
    await screen.findByRole('button', { name: 'Sort reference 01' });
    expect(location().has('cursor')).toBe(false);
    expect(location().get('sort')).toBe('title-asc');
    expect(location().get('q')).toBe('Sort');
    expect(location().get('reading_status')).toBe('read');
    expect(location().get('page_size')).toBe('50');
    expect(http.pages).toHaveLength(3);
    await act(async () => rendered.i18n.changeLanguage('zh-CN'));
    expect(screen.getByRole('combobox', { name: '文献排序' })).toHaveTextContent(/^标题：A–Z$/);
    const translatedMenu = await openSortMenu(userEvent.setup(), '文献排序');
    expect(within(translatedMenu).getByRole('option', { name: '当前顺序' })).toBeInTheDocument();
    expect(http.pages).toHaveLength(3);
  });

  it('aborts the old sort request and ignores its late stale error after a new URL visit', async () => {
    const http = installCatalogue({ holdFirstStale: true });
    try {
      await mount('/library?q=Sort&sort=title-asc&cursor=old-title-page&page_size=50');
      await waitFor(() => expect(http.pages).toHaveLength(1));
      const disabledSort = screen.getByRole('combobox', { name: 'Sort references' });
      expect(disabledSort).toHaveAttribute('aria-disabled', 'true');
      const user = userEvent.setup();
      expect(disabledSort).toHaveAttribute('tabindex', '-1');
      await user.click(disabledSort);
      disabledSort.focus();
      pressSortKey('Enter', 13);
      pressSortKey(' ', 32);
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(http.pages).toHaveLength(1);
      expect(disabledSort).toHaveTextContent(/^Title: A–Z$/);
      expect(location().get('sort')).toBe('title-asc');
      fireEvent.click(screen.getByRole('button', { name: 'Open descending visit' }));
      await screen.findByRole('button', { name: 'Sort reference 60' });
      expect(http.signals[0]?.aborted).toBe(true);
      await act(async () => http.releaseFirst());
      expect(screen.getByRole('combobox', { name: 'Sort references' })).toHaveTextContent(/^Title: Z–A$/);
      expect(screen.queryByRole('button', { name: 'Return to first page' })).not.toBeInTheDocument();
      expect(location().get('sort')).toBe('title-desc');
      expect(location().has('cursor')).toBe(false);
      expect(http.pages).toHaveLength(2);
    } finally {
      await act(async () => http.releaseFirst());
    }
  });
});
