import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_LIBRARY_COLUMN_PREFERENCES } from '@/common/adapter/libraryColumnPreferences';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { LibraryTable } from '@/renderer/pages/library/LibraryTable';
import { useLibraryWorkbench } from '@/renderer/pages/library/useLibraryWorkbench';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, page, paper } from './libraryCollectionsFixtures';

type ProbeScope = {
  query?: string;
  cursor?: string;
  project?: string;
  collection?: string;
  readingStatus?: LiteratureReadingStatus | '';
};

/** Test-only uncontrolled input in the existing projection seam; no Notes UI is published. */
function ReaderProbe({ query = '', cursor = '', project = '', collection = '', readingStatus = '' }: ProbeScope) {
  const reads = useLibraryWorkbench(query, cursor, '', collection, project, readingStatus);
  return (
    <>
      <button type='button' onClick={reads.refreshData}>
        Refresh probe
      </button>
      <LibraryTable
        rows={reads.list.value?.items ?? []}
        readStatus={reads.list.status}
        refreshing={reads.list.refreshing}
        refreshError={reads.list.refreshError}
        selectedId=''
        preferences={DEFAULT_LIBRARY_COLUMN_PREFERENCES}
        authors={() => ''}
        statusCell={(item) => (
          <input
            aria-label={`Draft ${item.id}`}
            defaultValue={item.title}
            readOnly={Boolean(reads.list.refreshing || reads.list.refreshError)}
          />
        )}
        open={() => {}}
        registerTitleButton={() => {}}
        onRetry={reads.refreshData}
      />
    </>
  );
}

const ownerA = 'list-refresh-account-a';
const ownerB = 'list-refresh-account-b';
const refreshFailure = 'Refresh failed. Showing the last loaded references.';

describe('Library list same-scope authoritative refresh', () => {
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

  it('keeps the actual row input and focus through a same-scope refresh and successful readback', async () => {
    const held = deferred<Response>();
    let reads = 0;
    installCollectionHTTP({
      collections: () => [],
      intercept: (url) => (url.pathname === '/api/literature/items' && ++reads === 2 ? held.promise : undefined),
    });
    await renderWithI18n(<ReaderProbe />, 'en-US');
    const input = await screen.findByRole('textbox', { name: 'Draft a' });
    const row = input.closest('tr');
    const table = screen.getByRole('table');
    fireEvent.change(input, { target: { value: 'Local draft survives' } });
    input.focus();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh probe' }));
    await waitFor(() => expect(reads).toBe(2));
    expect(input, 'same-scope refresh must retain the actual input, not recreate it').toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Draft a' })).toBe(input);
    expect(input.closest('tr')).toBe(row);
    expect(screen.getByRole('table')).toBe(table);
    expect(input).toHaveFocus();
    expect(input).toHaveValue('Local draft survives');
    expect(table.closest('[role="region"]')).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByText('Refreshing references…')).toBeInTheDocument();
    await act(async () => held.resolve(json(page([paper('a', 'Updated server title')]))));
    await screen.findByRole('button', { name: 'Updated server title' });
    expect(screen.getByRole('textbox', { name: 'Draft a' })).toBe(input);
    expect(input).toHaveValue('Local draft survives');
    expect(input).toHaveFocus();
    expect(table.closest('[role="region"]')).toHaveAttribute('aria-busy', 'false');
  });

  it('retains real rows on a recoverable refresh failure and retries only the original reader', async () => {
    let reads = 0;
    const http = installCollectionHTTP({
      collections: () => [],
      intercept: (url) =>
        url.pathname === '/api/literature/items' && ++reads === 2 ? json({ error: 'Unavailable' }, 503) : undefined,
    });
    await renderWithI18n(<ReaderProbe />, 'en-US');
    const input = await screen.findByRole('textbox', { name: 'Draft a' });
    fireEvent.change(input, { target: { value: 'Unsent draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh probe' }));
    const message = await screen.findByText(refreshFailure);
    expect(screen.getByRole('textbox', { name: 'Draft a' })).toBe(input);
    expect(input).toHaveValue('Unsent draft');
    expect(input).toHaveAttribute('readonly');
    const retry = within(message.closest<HTMLElement>('[role="alert"]')!).getByRole('button', { name: 'Retry' });
    expect(retry).toBeEnabled();
    fireEvent.click(retry);
    await waitFor(() => expect(reads).toBe(3));
    await waitFor(() => expect(screen.queryByText(refreshFailure)).toBeNull());
    expect(screen.getByRole('textbox', { name: 'Draft a' })).toBe(input);
    expect(input).toHaveValue('Unsent draft');
    expect(input).not.toHaveAttribute('readonly');
    expect(http.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it.each([
    { name: 'query', scope: { query: 'new query' } },
    { name: 'cursor', scope: { cursor: 'next-page' } },
    { name: 'project', scope: { project: 'another-project' } },
    { name: 'collection', scope: { collection: 'another-collection' } },
    { name: 'status', scope: { readingStatus: 'read' as const } },
  ])('retires the old projection immediately on a $name scope change', async ({ scope }) => {
    const held = deferred<Response>();
    let reads = 0;
    installCollectionHTTP({
      collections: () => [],
      intercept: (url) => (url.pathname === '/api/literature/items' && ++reads === 2 ? held.promise : undefined),
    });
    const mounted = await renderWithI18n(<ReaderProbe />, 'en-US');
    const oldInput = await screen.findByRole('textbox', { name: 'Draft a' });
    fireEvent.change(oldInput, { target: { value: 'Old scope draft' } });
    mounted.rerender(<ReaderProbe {...scope} />);
    expect(oldInput).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    await waitFor(() => expect(reads).toBe(2));
    await act(async () => held.resolve(json(page([paper('a', 'New scope title')]))));
    const newInput = await screen.findByRole('textbox', { name: 'Draft a' });
    expect(newInput).not.toBe(oldInput);
    expect(newInput).toHaveValue('New scope title');
  });

  it('drops the old account projection and ignores its late refresh even for the same item ID', async () => {
    const held = deferred<Response>();
    let oldReads = 0;
    installCollectionHTTP({
      collections: () => [],
      items: () => [paper('a', getRendererAccountOwnerId() === ownerA ? 'Account A' : 'Account B')],
      intercept: (url) =>
        url.pathname === '/api/literature/items' && getRendererAccountOwnerId() === ownerA && ++oldReads === 2
          ? held.promise
          : undefined,
    });
    await renderWithI18n(<ReaderProbe />, 'en-US');
    const oldInput = await screen.findByRole('textbox', { name: 'Draft a' });
    fireEvent.change(oldInput, { target: { value: 'Private old draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh probe' }));
    await waitFor(() => expect(oldReads).toBe(2));
    act(() => setRendererAccountOwner(ownerB));
    expect(oldInput).not.toBeInTheDocument();
    const newInput = await screen.findByRole('textbox', { name: 'Draft a' });
    expect(newInput).not.toBe(oldInput);
    expect(newInput).toHaveValue('Account B');
    await act(async () => held.resolve(json(page([paper('a', 'Late private account A')]))));
    expect(screen.getByRole('textbox', { name: 'Draft a' })).toBe(newInput);
    expect(newInput).toHaveValue('Account B');
  });

  it('removes retained data on a denied read rather than presenting it as a recoverable refresh error', async () => {
    let reads = 0;
    installCollectionHTTP({
      collections: () => [],
      intercept: (url) =>
        url.pathname === '/api/literature/items' && ++reads === 2 ? json({ error: 'Denied' }, 403) : undefined,
    });
    const mounted = await renderWithI18n(<ReaderProbe />, 'en-US');
    const input = await screen.findByRole('textbox', { name: 'Draft a' });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh probe' }));
    await screen.findByRole('heading', { name: mounted.i18n.t('conversation.libraryTable.loadFailed') });
    expect(input).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.queryByText(refreshFailure)).toBeNull();
  });

  it('keeps initial failure distinct and blocks stale row actions through the actual Page while retaining retry', async () => {
    let phase: 'initial-error' | 'ready' | 'refresh' = 'initial-error';
    const held = deferred<Response>();
    const http = installCollectionHTTP({
      collections: () => [],
      intercept: (url) => {
        if (url.pathname !== '/api/literature/items') return undefined;
        if (phase === 'initial-error') return json({ error: 'Unavailable' }, 503);
        if (phase === 'refresh') return held.promise;
        return undefined;
      },
    });
    const mounted = await mountCollections();
    await screen.findByRole('heading', { name: mounted.i18n.t('conversation.libraryTable.loadFailed') });
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.queryByText(refreshFailure)).toBeNull();
    phase = 'ready';
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('button', { name: 'Paper A' });
    const select = screen.getByRole('checkbox', { name: 'Select this page' });
    const status = screen.getByRole('combobox', {
      name: mounted.i18n.t('conversation.libraryReadingStatus.forPaper', { title: 'Paper A' }),
    });
    expect(select).toBeEnabled();
    expect(status).toBeEnabled();
    phase = 'refresh';
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByText('Refreshing references…');
    expect(select).toBeInTheDocument();
    expect(status).toBeInTheDocument();
    expect(select).toBeDisabled();
    expect(status).toBeDisabled();
    await act(async () => held.resolve(json({ error: 'Unavailable' }, 503)));
    const message = await screen.findByText(refreshFailure);
    expect(select).toBeDisabled();
    expect(status).toBeDisabled();
    const retry = within(message.closest<HTMLElement>('[role="alert"]')!).getByRole('button', { name: 'Retry' });
    expect(retry).toBeEnabled();
    phase = 'ready';
    fireEvent.click(retry);
    await waitFor(() => expect(select).toBeEnabled());
    expect(status).toBeEnabled();
    expect(http.requests.every((request) => request.method === 'GET')).toBe(true);
  });
});
