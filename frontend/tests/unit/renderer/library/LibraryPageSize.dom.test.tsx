import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';

const json = (value: unknown) =>
  new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } });
const items = Array.from({ length: 120 }, (_, index): LiteratureItem => ({
  id: `paper-${String(index + 1).padStart(3, '0')}`,
  title: `Paper ${String(index + 1).padStart(3, '0')}`,
  metadata_revision: 1,
  metadata: {},
  reading_status: 'unread',
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
      <button onClick={() => void navigate('/library?page_size=50')}>Open 50-item visit</button>
    </>
  );
}
async function mount(path = '/library') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <LibraryPage />
      <LocationProbe />
    </MemoryRouter>,
    'en-US'
  );
}
async function findPaper(title: string) {
  const button = await screen.findByText(title, { selector: 'button' });
  expect(button).toHaveAccessibleName(title);
  expect(button).toBeVisible();
  return button;
}
function installCatalogue({ holdFirstPage = false } = {}) {
  const pages: URL[] = [];
  const signals: Array<AbortSignal | null | undefined> = [];
  const allIds: URL[] = [];
  const exports: Array<{ item_ids: string[]; format: string }> = [];
  let releaseFirstPage: (() => void) | undefined;
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
        const limit = Number(url.searchParams.get('limit') ?? 20);
        const offset = Number(url.searchParams.get('cursor')?.replace('after-', '') ?? 0);
        if (holdFirstPage && pages.length === 1)
          await new Promise<void>((resolve) => {
            releaseFirstPage = resolve;
          });
        return json({
          schema: 'x-science.literature-page.v1',
          items: items.slice(offset, offset + limit),
          total_count: items.length,
          ...(offset + limit < items.length ? { next_cursor: `after-${offset + limit}` } : {}),
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
      throw new Error(`Unexpected page-size fixture request ${url.pathname}`);
    })
  );
  return { pages, signals, allIds, exports, releaseFirstPage: () => releaseFirstPage?.() };
}

describe('Library URL-owned page size through the original HTTP reader', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-page-size-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('requests 25/50/100 and resets the old cursor when the page size changes', async () => {
    const { pages } = installCatalogue();
    await mount();
    await findPaper('Paper 001');
    expect(pages[0].searchParams.get('limit')).toBe('25');
    const size = screen.getByRole('combobox', { name: 'References per page' });
    expect(size).toHaveValue('25');
    fireEvent.change(size, { target: { value: '50' } });
    await findPaper('Paper 050');
    expect(pages.at(-1)?.searchParams.get('limit')).toBe('50');
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    await findPaper('Paper 100');
    expect(pages.at(-1)?.searchParams.get('cursor')).toBe('after-50');
    fireEvent.change(size, {
      target: { value: '100' },
    });
    await findPaper('Paper 001');
    await findPaper('Paper 100');
    expect(pages.at(-1)?.searchParams.get('limit')).toBe('100');
    expect(pages.at(-1)?.searchParams.has('cursor')).toBe(false);
    const search = screen.getByLabelText('Library location').textContent ?? '';
    expect(new URLSearchParams(search).get('page_size')).toBe('100');
    expect(size).toHaveValue('100');
  });

  it('restores the 100-item URL window after a fresh mount and remount', async () => {
    const { pages } = installCatalogue();
    const rendered = await mount('/library?page_size=100');
    await findPaper('Paper 001');
    await findPaper('Paper 100');
    expect(pages[0].searchParams.get('limit')).toBe('100');
    expect(pages[0].searchParams.has('cursor')).toBe(false);
    expect(screen.getByRole('combobox', { name: 'References per page' })).toHaveValue('100');
    const search = screen.getByLabelText('Library location').textContent ?? '';
    expect(new URLSearchParams(search).get('page_size')).toBe('100');
    rendered.unmount();
    await mount(`/library${search}`);
    await findPaper('Paper 001');
    await findPaper('Paper 100');
    expect(screen.getByRole('combobox', { name: 'References per page' })).toHaveValue('100');
    expect(pages.at(-1)?.searchParams.get('limit')).toBe('100');
    expect(pages.at(-1)?.searchParams.has('cursor')).toBe(false);
  });

  it('clears the prior selection and range anchor, while all-matching export still reads the full catalogue', async () => {
    const http = installCatalogue();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:page-size') });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() });
    const downloads = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    await mount();
    const second = await screen.findByRole('checkbox', { name: 'Select Paper 002' });
    await waitFor(() => expect(second).toBeEnabled());
    fireEvent.click(second);
    fireEvent.change(screen.getByRole('combobox', { name: 'References per page' }), { target: { value: '50' } });
    await screen.findByRole('button', { name: 'Paper 050' });
    expect(screen.getByRole('checkbox', { name: 'Select Paper 002' })).not.toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Paper 005' }), { shiftKey: true });
    expect(screen.getByRole('checkbox', { name: 'Select Paper 005' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Select Paper 003' })).not.toBeChecked();
    expect(screen.getByText('1 selected')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select this page' }));
    fireEvent.click(screen.getByRole('button', { name: 'Select all matching references' }));
    fireEvent.click(screen.getByRole('button', { name: 'Export references' }));
    const ris = await screen.findByText('RIS (.ris)');
    await waitFor(() => expect(ris.closest('[role="menuitem"]')).not.toHaveAttribute('aria-disabled', 'true'));
    fireEvent.click(ris);
    await waitFor(() => expect(downloads).toHaveBeenCalledTimes(1));
    expect(http.allIds).toHaveLength(1);
    expect(http.allIds[0].searchParams.has('limit')).toBe(false);
    expect(http.allIds[0].searchParams.has('cursor')).toBe(false);
    expect(http.exports.flatMap((body) => body.item_ids)).toEqual(items.map((item) => item.id));
  });

  it('invalidates the original read owner when only the URL page size changes, ignoring its late response', async () => {
    const http = installCatalogue({ holdFirstPage: true });
    try {
      await mount();
      await waitFor(() => expect(http.pages).toHaveLength(1));
      fireEvent.click(screen.getByRole('button', { name: 'Open 50-item visit' }));
      await screen.findByRole('button', { name: 'Paper 050' });
      expect(http.pages.map((page) => page.searchParams.get('limit'))).toEqual(['25', '50']);
      expect(http.signals[0]?.aborted).toBe(true);
      await act(async () => http.releaseFirstPage());
      expect(screen.getByRole('button', { name: 'Paper 050' })).toBeInTheDocument();
      expect(screen.getByRole('combobox', { name: 'References per page' })).toHaveValue('50');
    } finally {
      await act(async () => http.releaseFirstPage());
    }
  });

  it('retains the non-UI 20-item default, bounds invalid URL sizes and switches the new label to Chinese', async () => {
    const { pages } = installCatalogue();
    await loadLiteraturePage();
    expect(pages[0].searchParams.get('limit')).toBe('20');
    await expect(loadLiteraturePage({ pageSize: 500 as never })).rejects.toThrow(
      'Invalid Literature Library page size'
    );
    expect(pages).toHaveLength(1);
    const rendered = await mount('/library?page_size=500');
    await screen.findByRole('button', { name: 'Paper 025' });
    expect(pages.at(-1)?.searchParams.get('limit')).toBe('25');
    await act(async () => rendered.i18n.changeLanguage('zh-CN'));
    expect(screen.getByRole('combobox', { name: '每页文献数' })).toHaveValue('25');
    expect(pages).toHaveLength(2);
  });
});
