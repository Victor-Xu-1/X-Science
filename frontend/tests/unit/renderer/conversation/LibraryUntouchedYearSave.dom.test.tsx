import React from 'react';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand } from '@/common/adapter/literatureLibrary';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';

const ownerId = 'untouched-year-owner';
const pendingKey = `x-science.literature-mutation.v1:${ownerId}`;
const unknownYear = 'unassigned-record';
const originalTitle = 'Original paper';
const response = (raw: string) => new Response(raw, { status: 200, headers: { 'Content-Type': 'application/json' } });
const itemJSON = (title = originalTitle, authors = 'Original author', revision = 7) =>
  `{"id":"paper","title":${JSON.stringify(title)},"metadata_revision":${revision},"metadata":{"authors":${JSON.stringify(authors)},"year":${JSON.stringify(unknownYear)},"opaque":{"exponent":1e400}},"deleted":false,"project_ids":[],"attachments":[]}`;

async function renderOwnerEditor() {
  const storage = literaturePendingStorage();
  const locks = installLiteratureLocks();
  const writes: Array<{ raw: string; body: Record<string, unknown> }> = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (init?.method === 'GET' && url.pathname === '/api/literature/items')
      return response(`{"schema":"x-science.literature-page.v1","items":[${itemJSON()}]}`);
    if (init?.method !== 'PATCH' || url.pathname !== '/api/literature/items/paper' || typeof init.body !== 'string')
      throw new Error(`Unexpected fixture request: ${init?.method} ${url.pathname}`);
    const body = JSON.parse(init.body) as Record<string, unknown>;
    writes.push({ raw: init.body, body });
    // Exercise the original owner, not an editor callback that bypasses its
    // durable slot. This browser-API fixture is not a real cross-tab claim.
    const pending = JSON.parse(storage.getItem(pendingKey)!) as { ownerId: string; command: LiteratureCommand };
    expect(pending.ownerId).toBe(ownerId);
    expect(pending.command).toMatchObject({ kind: 'edit', operation_id: body.operation_id });
    // Controlled transport ACK preserves the existing unknown year. It is
    // not an implementation or acceptance proof of a backend Store merge.
    const title = typeof body.title === 'string' ? body.title : originalTitle;
    return response(
      `{"schema":"x-science.literature-receipt.v1","operation_id":${JSON.stringify(body.operation_id)},"item":${itemJSON(title, 'Original author', 8)}}`
    );
  });
  vi.stubGlobal('fetch', fetchMock);
  const page = await loadLiteraturePage();
  const item = page.items[0];
  expect(item.metadata.year).toBe(unknownYear);
  const owner = new LiteratureMutationController(ownerId, { storage });
  const close = vi.fn();
  const submit = vi.fn((command: LiteratureCommand) => owner.submit(command));
  await renderWithI18n(
    <LibraryItemEditor item={item} projectId='project' busy={false} submit={submit} close={close} />,
    'en-US'
  );
  return { item, owner, storage, locks, close, submit, fetchMock, writes };
}

describe('Library untouched year save', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner(ownerId);
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('saves a changed title through the original owner without validating or sending the untouched year', async () => {
    const fixture = await renderOwnerEditor();
    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: '  Updated title  ' } });
    const save = screen.getByRole('button', { name: /^Save$/ });
    // Old source fails here because the unchanged unknown year disables Save.
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await waitFor(() => expect(fixture.close).toHaveBeenCalledTimes(1));
    expect(fixture.submit).toHaveBeenCalledTimes(1);
    expect(fixture.fetchMock).toHaveBeenCalledTimes(2);
    expect(fixture.writes).toHaveLength(1);
    expect(fixture.writes[0].body).toEqual({
      operation_id: expect.any(String),
      expected_revision: 7,
      title: 'Updated title',
    });
    expect(fixture.writes[0].raw).not.toMatch(/"year"\s*:|"metadata"\s*:|"metadata_patch"\s*:/);
    expect(fixture.item.metadata.year).toBe(unknownYear);
    expect(fixture.owner.getSnapshot()).toMatchObject({
      pending: null,
      error: null,
      receipt: { item: { metadata: { year: unknownYear } } },
    });
    expect(fixture.storage.values.size).toBe(0);
    expect(fixture.locks).toHaveBeenCalled();
    expect(fixture.locks.mock.calls.every(([key]) => key === pendingKey)).toBe(true);
  });

  it('still blocks a newly edited invalid year before the original owner or transport can receive a request', async () => {
    const fixture = await renderOwnerEditor();
    fireEvent.change(screen.getByRole('textbox', { name: 'Year' }), { target: { value: 'abcd' } });
    const save = screen.getByRole('button', { name: /^Save$/ });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(fixture.submit).not.toHaveBeenCalled();
    expect(fixture.fetchMock).toHaveBeenCalledTimes(1);
    expect(fixture.writes).toHaveLength(0);
    expect(fixture.close).not.toHaveBeenCalled();
    expect(fixture.storage.values.size).toBe(0);
    expect(fixture.owner.getSnapshot()).toMatchObject({ pending: null, mutating: false });
  });
});
