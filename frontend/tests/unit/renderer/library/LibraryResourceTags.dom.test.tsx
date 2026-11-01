import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { decodeLiteratureItem } from '@/common/adapter/literatureLibrary';
import { LibraryItemTags, LibraryResourceTagsProvider } from '@/renderer/pages/library/LibraryResourceTags';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';

const item = (deleted = false) =>
  decodeLiteratureItem({
    id: 'integration-paper',
    title: 'Synthetic reference',
    metadata_revision: 1,
    metadata: { authors: ['Synthetic Author'], year: 2026 },
    deleted,
    project_ids: [],
    attachments: [],
  });
function Views({ account, deleted = false }: { account: string; deleted?: boolean }) {
  return (
    <LibraryResourceTagsProvider account={account}>
      <section aria-label='Table tags'>
        <LibraryItemTags item={item(deleted)} compact />
      </section>
      <section role='dialog' aria-label='Reference details'>
        <LibraryItemTags item={item(deleted)} />
      </section>
    </LibraryResourceTagsProvider>
  );
}
const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });

/** Only the HTTP boundary is synthetic; owner, strict codecs, queue, locks and controls remain real. */
function installTagHTTP(initiallyAssigned = false, initialReadFails = false) {
  let assigned = initiallyAssigned;
  let revision = initiallyAssigned ? 4 : 0;
  const requests: { method: string; path: string; body?: Record<string, unknown> }[] = [];
  const receipts = new Map<string, string>();
  const snapshot = () => ({
    schema: 'x-science.resource-tags.v1',
    revision,
    resource_types: ['literature.item'],
    tags: [{ id: 'tag-favorite', system_key: 'favorite', created_at: revision ? 1 : 0, updated_at: revision ? 1 : 0 }],
    assignments: assigned
      ? [{ tag_id: 'tag-favorite', resource_type: 'literature.item', resource_id: 'integration-paper', created_at: 1 }]
      : [],
  });
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    const method = init?.method ?? 'GET';
    const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
    requests.push({ method, path, ...(body ? { body } : {}) });
    if (method === 'GET' && path === '/api/tags') {
      if (initialReadFails && requests.length === 1)
        return new Response(
          JSON.stringify({ code: 'RESOURCE_TAG_INTERNAL', message: 'Synthetic temporary read failure' }),
          { status: 503, headers: { 'Content-Type': 'application/json' } }
        );
      return json(snapshot());
    }
    if (method !== 'PUT' || path !== '/api/tags/assignments' || !body)
      throw new Error(`Unexpected Tag fixture request: ${method} ${path}`);
    if (
      Object.keys(body).toSorted().join(',') !== 'assigned,operation_id,resource_id,resource_type,tag_id' ||
      typeof body.operation_id !== 'string' ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(body.operation_id) ||
      body.resource_type !== 'literature.item' ||
      body.resource_id !== 'integration-paper' ||
      body.tag_id !== 'tag-favorite' ||
      typeof body.assigned !== 'boolean'
    ) {
      throw new Error('Unexpected noncanonical assignment request');
    }
    const previous = receipts.get(body.operation_id);
    if (previous) return new Response(previous, { status: 200, headers: { 'Content-Type': 'application/json' } });
    if (assigned !== body.assigned) {
      assigned = body.assigned;
      revision++;
    }
    const receipt = JSON.stringify({
      schema: 'x-science.resource-tag-receipt.v1',
      operation_id: body.operation_id,
      action: 'assignment',
      tag_id: 'tag-favorite',
      snapshot: snapshot(),
    });
    receipts.set(body.operation_id, receipt);
    return new Response(receipt, { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  return { requests, fetch, snapshot };
}

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('tag-integration-owner-a');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe('Library Tags through shared Provider, owner and HTTP', () => {
  it('retries a failed initial catalog read through the global Retry button without sending any mutation', async () => {
    const backend = installTagHTTP(false, true);
    await renderWithI18n(<Views account={getRendererAccountScopeToken()} />, 'en-US');
    const error = await screen.findByRole('alert');
    expect(backend.requests).toEqual([{ method: 'GET', path: '/api/tags' }]);
    fireEvent.click(within(error).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(backend.requests).toHaveLength(2));
    const table = screen.getByRole('region', { name: 'Table tags' });
    expect(await within(table).findByRole('button', { name: 'Manage tags' })).toBeEnabled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(backend.requests).toEqual([
      { method: 'GET', path: '/api/tags' },
      { method: 'GET', path: '/api/tags' },
    ]);
    expect(localStorage.length).toBe(0);
  });

  it('shares one catalog read, assigns from the table menu and unassigns from detail with synchronized badges', async () => {
    const backend = installTagHTTP();
    await renderWithI18n(<Views account={getRendererAccountScopeToken()} />, 'en-US');
    const table = screen.getByRole('region', { name: 'Table tags' });
    const details = screen.getByRole('dialog', { name: 'Reference details' });
    const manage = await within(table).findByRole('button', { name: 'Manage tags' });
    expect(backend.requests).toEqual([{ method: 'GET', path: '/api/tags' }]);
    expect(within(details).getByRole('button', { name: 'Manage tags' })).toBeInTheDocument();
    fireEvent.click(manage);
    const input = await screen.findByRole('combobox', { name: 'Search tags' });
    fireEvent.click(screen.getByRole('option', { name: 'Favorites' }));
    await waitFor(() => expect(backend.requests.filter((request) => request.method === 'GET')).toHaveLength(2));
    await waitFor(() => expect(localStorage.length).toBe(0));
    expect(backend.snapshot().assignments).toHaveLength(1);
    expect(within(table).getByText('Favorites')).toBeInTheDocument();
    expect(within(details).getByText('Favorites')).toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Escape' });
    fireEvent.click(within(details).getByRole('button', { name: 'Remove tag Favorites' }));
    await waitFor(() => expect(backend.requests.filter((request) => request.method === 'GET')).toHaveLength(3));
    await waitFor(() => expect(localStorage.length).toBe(0));
    expect(backend.snapshot().assignments).toHaveLength(0);
    expect(within(table).queryByText('Favorites')).not.toBeInTheDocument();
    expect(within(details).queryByText('Favorites')).not.toBeInTheDocument();
    const writes = backend.requests.filter((request) => request.method === 'PUT');
    expect(writes.map((request) => request.body?.assigned)).toEqual([true, false]);
    expect(new Set(writes.map((request) => request.body?.operation_id)).size).toBe(2);
    expect(backend.requests.map(({ method, path }) => `${method} ${path}`)).toEqual([
      'GET /api/tags',
      'PUT /api/tags/assignments',
      'GET /api/tags',
      'PUT /api/tags/assignments',
      'GET /api/tags',
    ]);
  });

  it('preserves Trash badges while disabling every write entrance and sending no mutation', async () => {
    const backend = installTagHTTP(true);
    await renderWithI18n(<Views account={getRendererAccountScopeToken()} deleted />, 'en-US');
    await screen.findAllByText('Favorites');
    const table = screen.getByRole('region', { name: 'Table tags' });
    const details = screen.getByRole('dialog', { name: 'Reference details' });
    expect(within(table).getByText('Favorites')).toBeInTheDocument();
    expect(within(details).getByText('Favorites')).toBeInTheDocument();
    for (const button of screen.getAllByRole('button')) {
      expect(button).toBeDisabled();
      fireEvent.click(button);
    }
    expect(backend.requests).toEqual([{ method: 'GET', path: '/api/tags' }]);
    expect(localStorage.length).toBe(0);
    expect(backend.snapshot().assignments).toHaveLength(1);
  });

  it('does not crash, acquire a new owner or fetch when mounting a stale account Provider after an account switch', async () => {
    const stale = getRendererAccountScopeToken();
    act(() => {
      setRendererAccountOwner('tag-integration-owner-b');
    });
    const fetch = vi.fn(async () => {
      throw new Error('A stale Provider must never fetch');
    });
    vi.stubGlobal('fetch', fetch);
    await expect(renderWithI18n(<Views account={stale} />, 'en-US')).resolves.toBeDefined();
    await act(async () => {
      await Promise.resolve();
    });
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Manage tags' })).not.toBeInTheDocument();
    expect(localStorage.length).toBe(0);
  });
});
