import React from 'react';
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router';
import userEvent from '@testing-library/user-event';
import { readFileSync } from 'node:fs';
import { URL as NodeURL } from 'node:url';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
import sortControlCSS from '@/renderer/pages/library/libraryWorkbench.header.css?inline';

let sortStyles: HTMLStyleElement;
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('library-year-sort-owner');
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
  expect(css).toContain('.library-sort-popup-transition-appear');
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
  id: `year-${index}`,
  title: `Year reference ${String(index).padStart(2, '0')}`,
  metadata_revision: 1,
  metadata: { year: index % 2 ? String(1990 + index) : 1990 + index },
  reading_status: 'unread',
  deleted: false,
  project_ids: [],
  attachments: [],
}));
const orders = { 'year-asc': items, 'year-desc': [...items].reverse() };
function LocationProbe() {
  return <output aria-label='Year sort location'>{useLocation().search}</output>;
}
function currentLocation() {
  return new URLSearchParams(screen.getByLabelText('Year sort location').textContent ?? '');
}
function visibleTitles() {
  return screen
    .getAllByRole('button', { name: /^Year reference / })
    .filter((button) => button.matches('button.library-table__title'))
    .map((button) => button.textContent);
}

it.each([
  {
    language: 'en-US' as const,
    label: 'Sort references',
    ascending: 'Year: oldest first',
    descending: 'Year: newest first',
    nextPage: 'Next page',
    selectPage: 'Select this page',
    hint: 'Unrecognized years appear last.',
    options: [
      'Recently updated',
      'Recently added',
      'First added',
      'Title: A–Z',
      'Title: Z–A',
      'Year: newest first',
      'Year: oldest first',
      'Current order',
    ],
  },
  {
    language: 'zh-CN' as const,
    label: '文献排序',
    ascending: '年份：从旧到新',
    descending: '年份：从新到旧',
    nextPage: '下一页',
    selectPage: '选择当前页',
    hint: '无法识别的年份排在最后。',
    options: [
      '最近更新',
      '最近添加',
      '最早添加',
      '标题：A–Z',
      '标题：Z–A',
      '年份：从新到旧',
      '年份：从旧到新',
      '当前顺序',
    ],
  },
])('connects year sorting, cursor reset and selection ownership in $language', async (labels) => {
  const requests: URL[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/settings/client') return json({});
      if (url.pathname === '/api/projects') return json({ projects: [] });
      if (url.pathname === '/api/literature/collections')
        return json({ schema: 'x-science.literature-collections.v1', collections: [] });
      if (url.pathname !== '/api/literature/items') throw new Error(`Unexpected year request ${url.pathname}`);
      requests.push(url);
      const mode = url.searchParams.get('sort') as keyof typeof orders;
      if (!(mode in orders)) throw new Error('Year mode did not reach the original page reader');
      const offset = url.searchParams.has('cursor') ? 25 : 0;
      return json({
        schema: 'x-science.literature-page.v1',
        items: orders[mode].slice(offset, offset + 25),
        total_count: items.length,
        ...(offset === 0 ? { next_cursor: 'year-after-25' } : {}),
      });
    })
  );
  const mount = (path: string) =>
    renderWithI18n(
      <MemoryRouter initialEntries={[path]}>
        <LibraryPage />
        <LocationProbe />
      </MemoryRouter>,
      labels.language
    );
  const user = userEvent.setup();
  let rendered = await mount('/library?q=Year&page_size=25&sort=year-asc');
  await screen.findByRole('button', { name: 'Year reference 00' });
  let control = screen.getByRole('combobox', { name: labels.label });
  await waitFor(() => expect(control).not.toHaveAttribute('aria-disabled', 'true'));
  expect(control).toHaveTextContent(labels.ascending);
  expect(control).toHaveAttribute('aria-description', labels.hint);
  expect(control.closest('.library-sort-control')).toHaveAttribute('title', labels.hint);
  expect(visibleTitles()).toEqual(orders['year-asc'].slice(0, 25).map((item) => item.title));
  await user.click(screen.getByRole('button', { name: labels.nextPage }));
  await screen.findByRole('button', { name: 'Year reference 29' });
  expect(currentLocation().get('cursor')).toBe('year-after-25');
  control = screen.getByRole('combobox', { name: labels.label });
  await waitFor(() => expect(control).not.toHaveAttribute('aria-disabled', 'true'));
  await user.click(control);
  const menu = await screen.findByRole('listbox');
  expect(menu.closest('.library-sort-popup')).not.toHaveStyle({ pointerEvents: 'none' });
  expect(
    within(menu)
      .getAllByRole('option')
      .map((option) => option.textContent)
  ).toEqual(labels.options);
  await user.click(within(menu).getByRole('option', { name: labels.descending }));
  await waitFor(() => expect(visibleTitles()).toEqual(orders['year-desc'].slice(0, 25).map((item) => item.title)));
  expect(requests.at(-1)?.searchParams.get('sort')).toBe('year-desc');
  expect(requests.at(-1)?.searchParams.get('limit')).toBe('25');
  expect(requests.at(-1)?.searchParams.has('cursor')).toBe(false);
  expect(currentLocation().get('q')).toBe('Year');
  expect(currentLocation().get('sort')).toBe('year-desc');
  expect(currentLocation().has('cursor')).toBe(false);
  fireEvent.click(screen.getByRole('checkbox', { name: labels.selectPage }));
  expect(screen.queryByRole('combobox', { name: labels.label })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('checkbox', { name: labels.selectPage }));
  expect(screen.getByRole('combobox', { name: labels.label })).toHaveTextContent(labels.descending);
  const search = screen.getByLabelText('Year sort location').textContent ?? '';
  rendered.unmount();
  rendered = await mount(`/library${search}`);
  await screen.findByRole('button', { name: 'Year reference 29' });
  expect(screen.getByRole('combobox', { name: labels.label })).toHaveTextContent(labels.descending);
  expect(requests.at(-1)?.searchParams.get('sort')).toBe('year-desc');
  expect(visibleTitles()).toEqual(orders['year-desc'].slice(0, 25).map((item) => item.title));
});
