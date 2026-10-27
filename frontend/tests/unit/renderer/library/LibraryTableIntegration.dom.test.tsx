import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  parseLibraryColumnPreferences,
  type LibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';
import { LIBRARY_COLUMN_PREFERENCES_SETTING } from '@/renderer/services/runtime/libraryColumnPreferences';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import {
  collection,
  deferred,
  installCollectionHTTP,
  json,
  mountCollections,
  paper,
} from './libraryCollectionsFixtures';

const ownerA = 'table-integration-account-a';
const ownerB = 'table-integration-account-b';
const settingKey = LIBRARY_COLUMN_PREFERENCES_SETTING;
function accountSettings(initial: Array<[string, LibraryColumnPreferences]> = []) {
  const saved = new Map(initial);
  const heldReads = new Map<string, Promise<Response>>();
  const reads: string[] = [];
  const writes: Array<{ owner: string; value: LibraryColumnPreferences }> = [];
  return {
    saved,
    heldReads,
    reads,
    writes,
    intercept: (url: URL, request: RequestInit): Response | Promise<Response> | undefined => {
      if (url.pathname !== '/api/settings/client') return undefined;
      const owner = getRendererAccountOwnerId();
      if (request.method === 'GET') {
        expect(url.searchParams.get('keys')).toBe(settingKey);
        reads.push(owner);
        // Actual client-setting wire: a flat key/value object, never {settings:[...]}.
        return heldReads.get(owner) ?? json(saved.has(owner) ? { [settingKey]: saved.get(owner) } : {});
      }
      if (request.method === 'PUT') {
        const body = JSON.parse(String(request.body)) as Record<string, unknown>;
        expect(Object.keys(body)).toEqual([settingKey]);
        const value = parseLibraryColumnPreferences(body[settingKey]);
        if (!value) throw new Error('Unexpected invalid column-setting request');
        saved.set(owner, value);
        writes.push({ owner, value });
        return json({ success: true });
      }
      throw new Error('Unexpected column-setting HTTP method');
    },
  };
}
const tableHeaders = () =>
  within(screen.getByRole('table', { name: 'Library papers' }))
    .getAllByRole('columnheader')
    .map((element) => element.textContent);
const location = () => new URLSearchParams(screen.getByLabelText('Library location').textContent ?? '');
const detail = () =>
  within(screen.getByRole('complementary', { name: 'Literature details' })).getAllByRole('region', {
    name: 'Literature details',
  })[0];
async function openColumns() {
  fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
  const panel = await screen.findByRole('dialog', { name: 'Customize columns' });
  await waitFor(() => expect(within(panel).getByRole('checkbox', { name: 'Show Authors' })).toBeEnabled());
  return panel;
}

describe('Library table through the real Page and account setting transport', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner(ownerA);
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('saves column hiding and keyboard order through account settings without changing the detail or sending catalogue writes', async () => {
    const settings = accountSettings();
    const http = installCollectionHTTP({ collections: () => [], intercept: settings.intercept });
    await mountCollections('/library?item=a');
    const heading = await screen.findByRole('heading', { name: 'Paper A' });
    await screen.findByRole('button', { name: 'Paper A' });
    fireEvent.click(within(detail()).getByRole('button', { name: 'Edit details' }));
    const title = within(detail()).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Draft survives column edits' } });
    const panel = await openColumns();
    expect(settings.writes).toEqual([]);
    fireEvent.click(within(panel).getByRole('checkbox', { name: 'Show Authors' }));
    await waitFor(() => {
      expect(settings.writes).toHaveLength(1);
      expect(panel).toHaveAttribute('aria-busy', 'false');
    });
    const move = within(panel).getByRole('button', { name: 'Move Year' });
    move.focus();
    fireEvent.keyDown(move, { key: 'ArrowUp' });
    await waitFor(() => {
      expect(settings.writes).toHaveLength(2);
      expect(panel).toHaveAttribute('aria-busy', 'false');
    });
    expect(tableHeaders()).toEqual(['Title', 'Year', 'Status', 'Files']);
    expect(settings.saved.get(ownerA)).toEqual({
      version: 1,
      order: ['year', 'readingStatus', 'authors', 'doi', 'attachments'],
      visible: ['year', 'readingStatus', 'attachments'],
    });
    expect(settings.writes.every((write) => write.owner === ownerA)).toBe(true);
    expect(screen.getByRole('heading', { name: 'Paper A' })).toBe(heading);
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(title);
    expect(title).toHaveValue('Draft survives column edits');
    expect(location().get('item')).toBe('a');
    const writes = http.requests.filter((request) => request.method !== 'GET');
    expect(writes).toHaveLength(2);
    expect(writes.every((request) => request.method === 'PUT' && request.path === '/api/settings/client')).toBe(true);
    expect(http.requests.some((request) => request.method === 'POST' || request.method === 'PATCH')).toBe(false);
  });

  it('restores the saved layout with GET after remount and never auto-writes defaults', async () => {
    const stored: LibraryColumnPreferences = { ...DEFAULT_LIBRARY_COLUMN_PREFERENCES, visible: ['year', 'doi'] };
    const settings = accountSettings([[ownerA, stored]]);
    const http = installCollectionHTTP({ collections: () => [], intercept: settings.intercept });
    const mounted = await mountCollections('/library?item=a');
    await waitFor(() => expect(tableHeaders()).toEqual(['Title', 'Year', 'DOI']));
    const initialReads = settings.reads.length;
    expect(initialReads).toBeGreaterThan(0);
    expect(settings.writes).toEqual([]);
    mounted.unmount();
    await mountCollections('/library?item=a');
    await waitFor(() => expect(settings.reads.length).toBeGreaterThan(initialReads));
    await waitFor(() => expect(tableHeaders()).toEqual(['Title', 'Year', 'DOI']));
    expect(settings.saved.get(ownerA)).toEqual(stored);
    expect(settings.writes).toEqual([]);
    expect(http.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it('ignores a late preference response from the old account and closes its ephemeral column menu', async () => {
    const old = deferred<Response>();
    const next: LibraryColumnPreferences = { ...DEFAULT_LIBRARY_COLUMN_PREFERENCES, visible: ['readingStatus', 'doi'] };
    const settings = accountSettings([[ownerB, next]]);
    settings.heldReads.set(ownerA, old.promise);
    installCollectionHTTP({
      collections: () => [],
      items: () => [paper('a', getRendererAccountOwnerId() === ownerB ? 'Account B paper' : 'Account A paper')],
      detail: () => paper('a', getRendererAccountOwnerId() === ownerB ? 'Account B paper' : 'Account A paper'),
      intercept: settings.intercept,
    });
    await mountCollections('/library?item=a');
    await waitFor(() => expect(settings.reads).toEqual([ownerA]));
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    const oldPanel = await screen.findByRole('dialog', { name: 'Customize columns' });
    expect(within(oldPanel).getByRole('checkbox', { name: 'Show Authors' })).toBeDisabled();
    act(() => {
      setRendererAccountOwner(ownerB);
    });
    await screen.findByRole('heading', { name: 'Account B paper' });
    await waitFor(() => expect(tableHeaders()).toEqual(['Title', 'Status', 'DOI']));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Customize columns' })).toBeNull());
    await act(async () => {
      old.resolve(json({ [settingKey]: { ...DEFAULT_LIBRARY_COLUMN_PREFERENCES, visible: ['authors'] } }));
      await old.promise;
    });
    expect(settings.reads[0]).toBe(ownerA);
    expect(settings.reads).toContain(ownerB);
    expect(tableHeaders()).toEqual(['Title', 'Status', 'DOI']);
    expect(settings.writes).toEqual([]);
    expect(location().get('item')).toBe('a');
  });

  it('keeps failed reads distinct from no matches and clears only view filters while preserving project and collection links', async () => {
    let unavailable = true;
    const settings = accountSettings();
    const http = installCollectionHTTP({
      collections: () => [collection('methods', 'Methods')],
      items: (url) => (url.searchParams.has('query') || url.searchParams.has('reading_status') ? [] : [paper()]),
      intercept: (url, request) => {
        const setting = settings.intercept(url, request);
        if (setting) return setting;
        if (url.pathname === '/api/projects')
          return json({ projects: [{ project_id: 'alpha', name: 'Alpha project' }] });
        if (url.pathname === '/api/literature/items' && unavailable) return json({ error: 'Unavailable' }, 503);
        return undefined;
      },
    });
    const rendered = await mountCollections(
      '/library?project=alpha&collection=methods&q=absent&reading_status=read&cursor=old-page&item=a'
    );
    const list = screen.getByRole('region', { name: 'Library papers' });
    await within(list).findByRole('heading', { name: rendered.i18n.t('conversation.libraryTable.loadFailed') });
    expect(within(list).queryByRole('button', { name: rendered.i18n.t('common.clear') })).toBeNull();
    expect(
      within(list).queryByRole('heading', { name: rendered.i18n.t('conversation.libraryWorkbench.noMatches') })
    ).toBeNull();
    unavailable = false;
    fireEvent.click(within(list).getByRole('button', { name: 'Retry' }));
    await within(list).findByRole('heading', { name: rendered.i18n.t('conversation.libraryWorkbench.noMatches') });
    fireEvent.click(within(list).getByRole('button', { name: rendered.i18n.t('common.clear') }));
    await within(list).findByRole('button', { name: 'Paper A' });
    expect(Object.fromEntries(location())).toEqual({ project: 'alpha', collection: 'methods' });
    expect(screen.queryByRole('complementary', { name: 'Literature details' })).toBeNull();
    const latest = http.requests
      .filter((request) => new URL(request.path, 'http://localhost').pathname === '/api/literature/items')
      .at(-1)!;
    const query = new URL(latest.path, 'http://localhost').searchParams;
    expect(query.get('project_id')).toBe('alpha');
    expect(query.get('collection_id')).toBe('methods');
    expect(query.has('query') || query.has('reading_status') || query.has('cursor')).toBe(false);
    expect(http.requests.filter((request) => request.method !== 'GET')).toEqual([]);
  });
});
