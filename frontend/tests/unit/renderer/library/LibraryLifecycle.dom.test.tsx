import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, page, paper } from './libraryCollectionsFixtures';

const releaseReads: (() => void)[] = [];

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('lifecycle-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  releaseReads.splice(0).forEach((release) => release());
  cleanup();
  vi.unstubAllGlobals();
});

function lifecycleHTTP(loseFirstReply = false) {
  let item = {
    ...paper(),
    metadata: { personal_note: 'original note', authors: ['Ada'] },
    project_ids: ['existing-project'],
  };
  const writes: Record<string, unknown>[] = [];
  const receipts = new Map<string, unknown>();
  const http = installCollectionHTTP({
    collections: () => [],
    items: (url) => (item.deleted === (url.searchParams.get('lifecycle') === 'deleted') ? [item] : []),
    detail: () => item,
    intercept: (url, init) => {
      if (init.method !== 'PATCH') return;
      expect(url.pathname).toBe('/api/literature/items/a');
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      writes.push(body);
      const prior = receipts.get(String(body.operation_id));
      if (prior) return json(prior);
      if (body.expected_revision !== item.metadata_revision) return json({ code: 'LITERATURE_CONFLICT' }, 409);
      item = { ...item, deleted: body.deleted as boolean, metadata_revision: item.metadata_revision + 1 };
      const receipt = { schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item };
      receipts.set(String(body.operation_id), receipt);
      return writes.length === 1 && loseFirstReply ? json({ code: 'UNAVAILABLE' }, 503) : json(receipt);
    },
  });
  return { http, writes, item: () => item };
}

async function rowAction(name: string) {
  const trigger = await screen.findByRole('button', { name: 'Actions for Paper A' });
  await waitFor(() => expect(trigger).toBeEnabled());
  fireEvent.click(trigger);
  fireEvent.click(await screen.findByRole('menuitem', { name }));
}

it('opens the real Trash query and keeps deleted detail unavailable', async () => {
  const http = installCollectionHTTP({
    collections: () => [],
    items: (url) =>
      url.searchParams.get('lifecycle') === 'deleted'
        ? [{ ...paper('deleted', 'Deleted paper'), deleted: true }]
        : [paper()],
  });
  await mountCollections();
  await screen.findByRole('button', { name: 'Paper A' });
  fireEvent.click(await screen.findByRole('button', { name: 'Trash' }));
  expect(await screen.findByRole('button', { name: 'Deleted paper' })).toBeDisabled();
  expect(http.requests.some((request) => request.path.includes('lifecycle=deleted'))).toBe(true);
  expect(http.requests.some((request) => request.path === '/api/literature/items/deleted')).toBe(false);
});

it('moves and restores one reference through the original Page owner and survives reload with links and notes', async () => {
  const fixture = lifecycleHTTP();
  let rendered = await mountCollections();
  await screen.findByRole('button', { name: 'Paper A' });
  await rowAction('Move to Trash');
  await waitFor(() => expect(fixture.writes).toHaveLength(1));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Paper A' })).toBeNull());
  fireEvent.click(screen.getByRole('button', { name: 'Trash' }));
  expect(await screen.findByRole('button', { name: 'Paper A' })).toBeDisabled();
  await rowAction('Restore');
  await waitFor(() => expect(fixture.writes).toHaveLength(2));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Paper A' })).toBeNull());
  rendered.unmount();
  rendered = await mountCollections();
  expect(await screen.findByRole('button', { name: 'Paper A' })).toBeEnabled();
  expect(fixture.writes[0]).toMatchObject({ deleted: true, expected_revision: 1 });
  expect(fixture.writes[1]).toMatchObject({ deleted: false, expected_revision: 2 });
  expect(fixture.writes[1].operation_id).not.toBe(fixture.writes[0].operation_id);
  expect(Object.keys(fixture.writes[0]).toSorted()).toEqual(['deleted', 'expected_revision', 'operation_id']);
  expect(fixture.item()).toMatchObject({
    metadata: { personal_note: 'original note', authors: ['Ada'] },
    project_ids: ['existing-project'],
    metadata_revision: 3,
  });
});

it('retries an uncertain lifecycle ACK with the same immutable original operation instead of a new delete', async () => {
  const fixture = lifecycleHTTP(true);
  await mountCollections();
  await screen.findByRole('button', { name: 'Paper A' });
  await rowAction('Move to Trash');
  const retry = await screen.findByRole('button', { name: 'Retry' });
  await waitFor(() => expect(retry).toBeEnabled());
  expect(screen.getByRole('button', { name: 'Actions for Paper A' })).toBeDisabled();
  fireEvent.click(retry);
  await waitFor(() => expect(fixture.writes).toHaveLength(2));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Paper A' })).toBeNull());
  expect(fixture.writes[1]).toEqual(fixture.writes[0]);
  expect(fixture.item().metadata_revision).toBe(2);
  expect(screen.queryByRole('alert')).toBeNull();
});

it('keeps a stale revision refusal visible without deleting or rewriting the intent', async () => {
  const fixture = lifecycleHTTP();
  await mountCollections();
  await screen.findByRole('button', { name: 'Paper A' });
  fixture.item().metadata_revision = 2; // Another backend writer changed the revision after this read.
  await rowAction('Move to Trash');
  await screen.findByRole('alert');
  expect(fixture.writes).toHaveLength(1);
  expect(fixture.writes[0].expected_revision).toBe(1);
  expect(fixture.item().deleted).toBe(false);
  expect(screen.getByRole('button', { name: 'Paper A' })).toBeEnabled();
});

it('fences retained refresh and late active/account reads from the Trash visit without a lifecycle write', async () => {
  const oldActive = deferred<Response>();
  const oldTrash = deferred<Response>();
  const refreshStarted = deferred<void>();
  releaseReads.push(
    () => oldActive.resolve(json(page([]))),
    () => oldTrash.resolve(json(page([])))
  );
  let activeStarted = false,
    holdTrash = false,
    replacement = false;
  const deleted = { ...paper('deleted', 'Deleted paper'), deleted: true };
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url, init) => {
      if (url.pathname !== '/api/literature/items' || (init.method ?? 'GET') !== 'GET') return;
      if (replacement) return json(page([]));
      if (url.searchParams.get('lifecycle') !== 'deleted' && !activeStarted) {
        activeStarted = true;
        return oldActive.promise;
      }
      if (url.searchParams.get('lifecycle') === 'deleted') {
        if (holdTrash) {
          refreshStarted.resolve();
          return oldTrash.promise;
        }
        return json(page([deleted]));
      }
    },
  });
  await mountCollections();
  await waitFor(() => expect(activeStarted).toBe(true));
  fireEvent.click(screen.getByRole('button', { name: 'Trash' }));
  expect(await screen.findByRole('button', { name: 'Deleted paper' })).toBeDisabled();
  await act(async () => {
    oldActive.resolve(json(page([paper()])));
    await oldActive.promise;
  });
  expect(screen.queryByRole('button', { name: 'Paper A' })).toBeNull();
  holdTrash = true;
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await refreshStarted.promise;
  expect(screen.getByRole('button', { name: 'Actions for Deleted paper' })).toBeDisabled();
  await act(async () => {
    replacement = true;
    setRendererAccountOwner('replacement-owner');
  });
  await act(async () => {
    oldTrash.resolve(json(page([deleted])));
    await oldTrash.promise;
  });
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Deleted paper' })).toBeNull());
  expect(http.requests.filter((request) => request.method === 'PATCH')).toHaveLength(0);
});

it('invalidates a collection editor when leaving the active scope for Trash', async () => {
  installCollectionHTTP({ collections: () => [], items: () => [] });
  await mountCollections();
  const create = await screen.findByRole('button', { name: 'New collection' });
  await waitFor(() => expect(create).toBeEnabled());
  fireEvent.click(create);
  await screen.findByRole('dialog');
  fireEvent.click(screen.getByRole('button', { name: 'Trash' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
});
