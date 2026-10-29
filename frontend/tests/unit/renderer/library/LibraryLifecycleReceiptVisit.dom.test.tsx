import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, page, paper } from './libraryCollectionsFixtures';

const release: (() => void)[] = [];
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('lifecycle-visit-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  release.splice(0).forEach((done) => done());
  cleanup();
  vi.unstubAllGlobals();
});

for (const returnToOriginal of [false, true]) {
  it(`keeps a delayed lifecycle ACK from navigating or focusing a ${returnToOriginal ? 'returned original' : 'different'} visit`, async () => {
    const ack = deferred<Response>();
    const started = deferred<void>();
    let item = paper();
    let receipt: unknown;
    release.push(() => ack.resolve(json(receipt)));
    const http = installCollectionHTTP({
      collections: () => [],
      intercept: (url, init) => {
        if (url.pathname === '/api/literature/items/a' && init.method === 'PATCH') {
          const command = JSON.parse(String(init.body));
          item = { ...item, deleted: true, metadata_revision: 2 };
          receipt = { schema: 'x-science.literature-receipt.v1', operation_id: command.operation_id, item };
          started.resolve();
          return ack.promise;
        }
        if (url.pathname === '/api/literature/items' && (init.method ?? 'GET') === 'GET') {
          if (url.searchParams.get('query') === 'different') return json(page([paper('b', 'Paper B')], 'cursor-b'));
          return json(page(item.deleted ? [] : [item]));
        }
      },
    });
    await mountCollections();
    const actions = await screen.findByRole('button', { name: 'Actions for Paper A' });
    await waitFor(() => expect(actions).toBeEnabled());
    fireEvent.click(actions);
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Move to Trash' }));
    await started.promise;
    const search = screen.getByRole('searchbox', { name: 'Search Library' });
    fireEvent.change(search, { target: { value: 'different' } });
    await screen.findByRole('button', { name: 'Paper B' });
    const next = screen.getByRole('button', { name: 'Next page' });
    await waitFor(() => expect(next).toBeEnabled());
    fireEvent.click(next);
    await waitFor(() => expect(screen.getByLabelText('Library location').textContent).toContain('cursor=cursor-b'));
    if (returnToOriginal) {
      fireEvent.change(search, { target: { value: '' } });
      await waitFor(() => expect(screen.queryByRole('button', { name: 'Paper B' })).toBeNull());
    }
    search.focus();
    const location = screen.getByLabelText('Library location').textContent;
    const readCount = http.requests.filter((request) => request.path.startsWith('/api/literature/items?')).length;
    await act(async () => {
      ack.resolve(json(receipt));
      await ack.promise;
    });
    await waitFor(() =>
      expect(
        http.requests.filter((request) => request.path.startsWith('/api/literature/items?')).length
      ).toBeGreaterThan(readCount)
    );
    expect(screen.getByLabelText('Library location').textContent).toBe(location);
    expect(search).toHaveFocus();
    expect(http.requests.filter((request) => request.method === 'PATCH')).toHaveLength(1);
  });
}
it('keeps a delayed lifecycle ACK from clearing the page after selecting another item', async () => {
  const ack = deferred<Response>();
  const started = deferred<void>();
  let receipt: unknown;
  let committed = false;
  release.push(() => ack.resolve(json(receipt)));
  const http = installCollectionHTTP({
    collections: () => [],
    detail: (id) => paper(id, id === 'b' ? 'Paper B' : 'Paper A'),
    items: () => (committed ? [paper('b', 'Paper B')] : [paper(), paper('b', 'Paper B')]),
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/items/a' && init.method === 'PATCH') {
        const command = JSON.parse(String(init.body));
        committed = true;
        receipt = {
          schema: 'x-science.literature-receipt.v1',
          operation_id: command.operation_id,
          item: { ...paper(), deleted: true, metadata_revision: 2 },
        };
        started.resolve();
        return ack.promise;
      }
    },
  });
  await mountCollections('/library?cursor=cursor-a');
  const actions = await screen.findByRole('button', { name: 'Actions for Paper A' });
  await waitFor(() => expect(actions).toBeEnabled());
  fireEvent.click(actions);
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Move to Trash' }));
  await started.promise;
  fireEvent.click(screen.getByRole('button', { name: 'Paper B' }));
  await waitFor(() => expect(screen.getByLabelText('Library location').textContent).toContain('item=b'));
  await screen.findByRole('heading', { name: 'Paper B' });
  const detailAnchor = screen.getByRole('group', { name: 'Literature details' });
  detailAnchor.focus();
  expect(detailAnchor).toHaveFocus();
  const location = screen.getByLabelText('Library location').textContent;
  const reads = http.requests.filter((request) => request.path.startsWith('/api/literature/items?')).length;
  await act(async () => {
    ack.resolve(json(receipt));
    await ack.promise;
  });
  await waitFor(() =>
    expect(http.requests.filter((request) => request.path.startsWith('/api/literature/items?')).length).toBeGreaterThan(
      reads
    )
  );
  expect(screen.getByLabelText('Library location').textContent).toBe(location);
  expect(detailAnchor).toHaveFocus();
});
it('retries an immutable lifecycle operation without granting the retry visit navigation authority', async () => {
  const writes: string[] = [];
  let item = paper();
  let receipt: unknown;
  const http = installCollectionHTTP({
    collections: () => [],
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/items/a' && init.method === 'PATCH') {
        writes.push(String(init.body));
        if (writes.length === 1) {
          const command = JSON.parse(String(init.body));
          item = { ...item, deleted: true, metadata_revision: 2 };
          receipt = { schema: 'x-science.literature-receipt.v1', operation_id: command.operation_id, item };
          return json({ code: 'UNAVAILABLE' }, 503);
        }
        return json(receipt);
      }
      if (url.pathname === '/api/literature/items' && (init.method ?? 'GET') === 'GET')
        return json(
          page(
            url.searchParams.get('query') === 'different' ? [paper('b', 'Paper B')] : item.deleted ? [] : [item],
            'cursor-b'
          )
        );
    },
  });
  await mountCollections();
  const actions = await screen.findByRole('button', { name: 'Actions for Paper A' });
  await waitFor(() => expect(actions).toBeEnabled());
  fireEvent.click(actions);
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Move to Trash' }));
  const retry = await screen.findByRole('button', { name: 'Retry' });
  await waitFor(() => expect(retry).toBeEnabled());
  const search = screen.getByRole('searchbox', { name: 'Search Library' });
  fireEvent.change(search, { target: { value: 'different' } });
  await screen.findByRole('button', { name: 'Paper B' });
  const next = screen.getByRole('button', { name: 'Next page' });
  await waitFor(() => expect(next).toBeEnabled());
  fireEvent.click(next);
  await waitFor(() => expect(screen.getByLabelText('Library location').textContent).toContain('cursor=cursor-b'));
  const location = screen.getByLabelText('Library location').textContent;
  const reads = http.requests.filter((request) => request.path.startsWith('/api/literature/items?')).length;
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(writes).toHaveLength(2));
  await waitFor(() =>
    expect(http.requests.filter((request) => request.path.startsWith('/api/literature/items?')).length).toBeGreaterThan(
      reads
    )
  );
  expect(writes[1]).toBe(writes[0]);
  expect(screen.getByLabelText('Library location').textContent).toBe(location);
});
