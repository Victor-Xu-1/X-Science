import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, page, paper } from './libraryCollectionsFixtures';

const release: (() => void)[] = [];
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('row-actions-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  release.splice(0).forEach((done) => done());
  cleanup();
  vi.unstubAllGlobals();
});

async function openActions(title = 'Paper A') {
  const trigger = await screen.findByRole('button', { name: `Actions for ${title}` });
  await waitFor(() => expect(trigger).toBeEnabled());
  fireEvent.click(trigger);
}

it('opens Edit through the same item detail owner and returns focus without entering an edit form', async () => {
  const http = installCollectionHTTP({ collections: () => [] });
  await mountCollections();
  await openActions();
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit' }));
  const dialog = await screen.findByRole('dialog', { name: 'Paper A' });
  expect(within(dialog).getByRole('button', { name: 'Edit details' })).toHaveAttribute('aria-expanded', 'false');
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull());
  expect(screen.getByLabelText('Library location').textContent).toBe('?item=a');
  expect(http.requests.filter((request) => request.path === '/api/literature/items/a')).toHaveLength(1);
  fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  const title = screen.getByRole('button', { name: 'Paper A' });
  await waitFor(() => expect(title).toHaveFocus());
  fireEvent.click(title);
  await screen.findByRole('dialog', { name: 'Paper A' });
  expect(screen.getByLabelText('Library location').textContent).toBe('?item=a');
  expect(http.requests.filter((request) => request.path === '/api/literature/items/a')).toHaveLength(2);
  expect(http.requests.every((request) => request.method === 'GET')).toBe(true);
});

it('keeps Edit disabled in Trash and never opens deleted item details', async () => {
  const http = installCollectionHTTP({ collections: () => [], items: () => [{ ...paper(), deleted: true }] });
  await mountCollections('/library?lifecycle=deleted');
  await openActions();
  const edit = await screen.findByRole('menuitem', { name: 'Edit' });
  expect(edit).toHaveAttribute('aria-disabled', 'true');
  fireEvent.click(edit);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.getByRole('button', { name: 'Paper A' })).toBeDisabled();
  expect(await screen.findByRole('menuitem', { name: 'Restore' })).toBeInTheDocument();
  expect(http.requests.some((request) => request.path === '/api/literature/items/a')).toBe(false);
});

it('invalidates an open Edit menu during refresh and ignores the old account response', async () => {
  const old = deferred<Response>();
  const started = deferred<void>();
  release.push(() => old.resolve(json(page([]))));
  let held = false;
  let replacement = false;
  const http = installCollectionHTTP({
    collections: () => [],
    intercept: (url, init) => {
      if (url.pathname !== '/api/literature/items' || (init.method ?? 'GET') !== 'GET') return;
      if (replacement) return json(page([]));
      if (held) {
        started.resolve();
        return old.promise;
      }
    },
  });
  await mountCollections();
  await openActions();
  await screen.findByRole('menuitem', { name: 'Edit' });
  held = true;
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await started.promise;
  expect(screen.getByRole('button', { name: 'Actions for Paper A' })).toBeDisabled();
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull());
  await act(async () => {
    replacement = true;
    setRendererAccountOwner('replacement-row-owner');
  });
  await act(async () => {
    old.resolve(json(page([paper()])));
    await old.promise;
  });
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Paper A' })).toBeNull());
  expect(http.requests.some((request) => request.path === '/api/literature/items/a')).toBe(false);
});
