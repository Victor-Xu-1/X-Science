import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router';
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

const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const items = Array.from({ length: 30 }, (_, index): LiteratureItem => ({
  id: `date-${index}`,
  title: `Date reference ${String(index).padStart(2, '0')}`,
  metadata_revision: 1,
  metadata: {},
  reading_status: 'unread',
  deleted: false,
  project_ids: [],
  attachments: [],
}));
const orders = {
  'created-desc': [...items].reverse(),
  'created-asc': items,
  'updated-desc': [items[7], ...items.filter((item) => item !== items[7])],
};
function LocationProbe() {
  return <output aria-label='Date sort location'>{useLocation().search}</output>;
}
function currentLocation() {
  return new URLSearchParams(screen.getByLabelText('Date sort location').textContent ?? '');
}
function visibleTitles() {
  return screen
    .getAllByRole('button', { name: /^Date reference / })
    .filter((button) => button.matches('button.library-table__title'))
    .map((button) => button.textContent);
}
async function mount(path: string, language: 'en-US' | 'zh-CN' = 'en-US') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <LibraryPage />
      <LocationProbe />
    </MemoryRouter>,
    language
  );
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('library-date-sort-owner');
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

it('connects all date options to the original URL reader, resets cursors and preserves server order in both languages', async () => {
  const requests: URL[] = [];
  const user = userEvent.setup();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects') return json({ projects: [] });
      if (url.pathname === '/api/literature/collections')
        return json({ schema: 'x-science.literature-collections.v1', collections: [] });
      if (url.pathname !== '/api/literature/items') throw new Error(`Unexpected date-sort request ${url.pathname}`);
      requests.push(url);
      const mode = url.searchParams.get('sort') as keyof typeof orders;
      if (!(mode in orders)) throw new Error('Date enum did not reach the original reader');
      const offset = url.searchParams.has('cursor') ? 25 : 0;
      return json({
        schema: 'x-science.literature-page.v1',
        items: orders[mode].slice(offset, offset + 25),
        total_count: items.length,
        ...(offset === 0 ? { next_cursor: 'after-25' } : {}),
      });
    })
  );
  let rendered = await mount('/library?q=Date&page_size=25&sort=created-desc');
  await screen.findByRole('button', { name: 'Date reference 29' });
  let control = screen.getByRole('combobox', { name: 'Sort references' });
  expect(control).toHaveTextContent(/^Recently added$/);
  const englishMenu = await openSortMenu(user);
  expect(
    within(englishMenu)
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
  pressSortKey('Escape', 27);
  await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
  expect(control).toHaveAttribute('aria-description', 'Unrecognized dates appear last.');
  expect(control.closest('.library-sort-control')).toHaveAttribute('title', 'Unrecognized dates appear last.');
  expect(visibleTitles()).toEqual(orders['created-desc'].slice(0, 25).map((item) => item.title));
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
  await screen.findByRole('button', { name: 'Date reference 00' });
  expect(currentLocation().get('cursor')).toBe('after-25');
  for (const mode of ['created-asc', 'updated-desc', 'created-desc'] as const) {
    control = screen.getByRole('combobox', { name: 'Sort references' });
    await waitFor(() => expect(control).not.toHaveAttribute('aria-disabled', 'true'));
    await chooseSort(
      user,
      ({ 'created-asc': 'First added', 'updated-desc': 'Recently updated', 'created-desc': 'Recently added' } as const)[
        mode
      ]
    );
    await waitFor(() => expect(visibleTitles()).toEqual(orders[mode].slice(0, 25).map((item) => item.title)));
    expect(requests.at(-1)?.searchParams.get('sort')).toBe(mode);
    expect(requests.at(-1)?.searchParams.get('limit')).toBe('25');
    expect(requests.at(-1)?.searchParams.has('cursor')).toBe(false);
    expect(currentLocation().get('q')).toBe('Date');
    expect(currentLocation().get('sort')).toBe(mode);
    expect(currentLocation().has('cursor')).toBe(false);
  }
  await act(async () => {
    await rendered.i18n.changeLanguage('zh-CN');
  });
  control = screen.getByRole('combobox', { name: '文献排序' });
  const chineseMenu = await openSortMenu(user, '文献排序');
  expect(
    within(chineseMenu)
      .getAllByRole('option')
      .map((option) => option.textContent)
  ).toEqual([
    '最近更新',
    '最近添加',
    '最早添加',
    '标题：A–Z',
    '标题：Z–A',
    '年份：从新到旧',
    '年份：从旧到新',
    '当前顺序',
  ]);
  pressSortKey('Escape', 27);
  await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument());
  expect(control).toHaveAttribute('aria-description', '无法识别的日期排在最后。');
  expect(control.closest('.library-sort-control')).toHaveAttribute('title', '无法识别的日期排在最后。');
  const restoredSearch = screen.getByLabelText('Date sort location').textContent ?? '';
  rendered.unmount();
  rendered = await mount(`/library${restoredSearch}`, 'zh-CN');
  await screen.findByRole('button', { name: 'Date reference 29' });
  expect(screen.getByRole('combobox', { name: '文献排序' })).toHaveTextContent(/^最近添加$/);
  expect(requests.at(-1)?.searchParams.get('sort')).toBe('created-desc');
  expect(visibleTitles()).toEqual(orders['created-desc'].slice(0, 25).map((item) => item.title));
}, 30_000);
