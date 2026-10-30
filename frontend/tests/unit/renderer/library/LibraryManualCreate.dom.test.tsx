import React from 'react';
import { act, cleanup, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { decodeLiteratureCommand, type LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import {
  collection,
  deferred,
  installCollectionHTTP,
  json,
  mountCollections,
  paper,
} from './libraryCollectionsFixtures';

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('manual-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const saved = {
  ...paper('manual', 'Canonical manual reference'),
  metadata: { doi: '10.1234/manual' },
  project_ids: ['project'],
};
const receipt = (operation_id: string): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1',
  operation_id,
  item: saved,
});
async function openForm() {
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  const add = await screen.findByRole('menuitem', { name: 'Add reference' });
  await waitFor(() => expect(add).not.toHaveAttribute('aria-disabled', 'true'));
  fireEvent.click(add);
  const dialog = await screen.findByRole('dialog', { name: 'Add reference' });
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Title' }), {
    target: { value: 'Draft manual reference' },
  });
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'DOI' }), { target: { value: '10.1234/manual' } });
  return dialog;
}

it('creates once from the real Add menu and reads canonical detail with one project transaction', async () => {
  let acknowledged = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (acknowledged ? [saved] : []),
    detail: () => saved,
    intercept: (url, init) => {
      if (url.pathname === '/api/projects')
        return json({ projects: [{ project_id: 'project', name: 'Research project' }] });
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        const command = JSON.parse(String(init.body));
        acknowledged = true;
        return json(receipt(command.operation_id));
      }
    },
  });
  await mountCollections('/library?project=project');
  const dialog = await openForm();
  const button = within(dialog).getByRole('button', { name: 'Create reference' });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(await screen.findByRole('heading', { name: saved.title })).toBeInTheDocument();
  expect(screen.getByLabelText('Library location')).toHaveTextContent('item=manual');
  const writes = http.requests.filter((request) => request.method !== 'GET');
  expect(writes).toHaveLength(1);
  expect(writes[0].path).toBe('/api/literature/items');
  expect(JSON.parse(writes[0].body!)).toMatchObject({
    title: 'Draft manual reference',
    metadata: { doi: '10.1234/manual' },
    project_id: 'project',
  });
  expect(http.requests.some((request) => request.path.includes('/candidates'))).toBe(false);
});

it('restores an uncertain immutable create through the original outbox after remount', async () => {
  let calls = 0;
  let committed = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (committed ? [saved] : []),
    detail: () => saved,
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        calls++;
        if (calls === 1) throw new TypeError('Controlled lost response');
        committed = true;
        return json(receipt(JSON.parse(String(init.body)).operation_id));
      }
    },
  });
  await mountCollections();
  const dialog = await openForm();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create reference' }));
  await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Retry' })).toBeEnabled());
  expect(within(dialog).getByRole('textbox', { name: 'Title' })).toBeDisabled();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add reference' })).toBeNull());
  expect(screen.queryByText('Discard this unsaved reference?')).toBeNull();
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeEnabled();
  cleanup();
  await mountCollections();
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
  await screen.findByRole('button', { name: saved.title });
  const writes = http.requests.filter((request) => request.method === 'POST');
  expect(writes).toHaveLength(2);
  expect(writes[0].body).toBe(writes[1].body);
});

it('does not transfer a late create ACK to a newer route or another account', async () => {
  const held = deferred<Response>();
  let posted = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        posted = true;
        return held.promise;
      }
    },
  });
  await mountCollections();
  const dialog = await openForm();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create reference' }));
  await waitFor(() => expect(posted).toBe(true));
  expect(dialog).toHaveAttribute('aria-busy', 'true');
  expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeDisabled();
  fireEvent.change(screen.getByRole('searchbox', { name: 'Search Library' }), { target: { value: 'new-route' } });
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add reference' })).toBeNull());
  await act(async () => {
    setRendererAccountOwner('other-owner');
  });
  await act(async () => {
    held.resolve(
      json(receipt(JSON.parse(http.requests.find((request) => request.method === 'POST')!.body!).operation_id))
    );
    await held.promise;
  });
  expect(screen.getByLabelText('Library location')).toHaveTextContent('q=new-route');
  expect(screen.getByLabelText('Library location')).not.toHaveTextContent('item=manual');
  expect(screen.queryByRole('heading', { name: saved.title })).toBeNull();
});

it('reports a typed Trash collision and navigates there without a hidden restore', async () => {
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url, init) =>
      url.pathname === '/api/literature/items' && init.method === 'POST'
        ? json({ code: 'LITERATURE_TRASH_CONFLICT' }, 409)
        : undefined,
  });
  await mountCollections();
  const dialog = await openForm();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create reference' }));
  fireEvent.click(await within(dialog).findByRole('button', { name: 'Open Trash' }));
  expect(screen.getByLabelText('Library location')).toHaveTextContent('lifecycle=deleted');
  expect(http.requests.filter((request) => request.method !== 'GET')).toHaveLength(1);
});

it('refreshes a definitely rejected collection revision without discarding the draft or reusing its operation', async () => {
  let current = { ...collection('methods', 'Methods'), revision: 7 };
  const held = deferred<Response>();
  let collectionReads = 0;
  let creates = 0;
  const http = installCollectionHTTP({
    collections: () => [current],
    items: () => [],
    detail: () => saved,
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/collections' && (init.method ?? 'GET') === 'GET') {
        collectionReads++;
        if (creates === 1) return held.promise;
      }
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        creates++;
        if (creates === 1) {
          current = { ...current, name: 'Renamed methods', revision: 8 };
          return json({ code: 'LITERATURE_CONFLICT' }, 409);
        }
        return json(receipt(JSON.parse(String(init.body)).operation_id));
      }
    },
  });
  await mountCollections('/library?collection=methods');
  const dialog = await openForm();
  const initialReads = collectionReads;
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create reference' }));
  await waitFor(() => expect(creates).toBe(1));
  await waitFor(() => expect(collectionReads).toBeGreaterThan(initialReads));
  expect(within(dialog).getByRole('textbox', { name: 'Title' })).toHaveValue('Draft manual reference');
  expect(within(dialog).getByRole('button', { name: 'Create reference' })).toBeDisabled();
  fireEvent.submit(dialog.querySelector('form')!);
  expect(creates).toBe(1);
  await act(async () => {
    held.resolve(json({ schema: 'x-science.literature-collections.v1', collections: [current] }));
    await held.promise;
  });
  await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Create reference' })).toBeEnabled());
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create reference' }));
  await screen.findByRole('heading', { name: saved.title });
  const writes = http.requests.filter((request) => request.method === 'POST');
  expect(writes).toHaveLength(2);
  const commands = writes.map((request) => JSON.parse(request.body!));
  expect(commands[0]).toMatchObject({ collection_id: 'methods', expected_collection_revision: 7 });
  expect(commands[1]).toMatchObject({
    collection_id: 'methods',
    expected_collection_revision: 8,
    title: 'Draft manual reference',
    metadata: { doi: '10.1234/manual' },
  });
  expect(commands[1].operation_id).not.toBe(commands[0].operation_id);
});

it('admits only the typed metadata command and rejects fake provider or attachment fields', () => {
  const value = {
    kind: 'manual-create',
    operation_id: '11111111-1111-1111-1111-111111111111',
    title: ' Manual ',
    metadata: { doi: '10.1234/manual' },
  };
  expect(decodeLiteratureCommand(value)).toEqual({ ...value, title: 'Manual' });
  expect(() => decodeLiteratureCommand({ ...value, provider: 'fake' })).toThrow();
  expect(() => decodeLiteratureCommand({ ...value, artifact_id: 'fake' })).toThrow();
  expect(() => decodeLiteratureCommand({ ...value, collection_id: 'collection' })).toThrow();
});

it.each(['/library?inbox=1', '/library?lifecycle=deleted'])(
  'does not offer creation in non-library scope %s',
  async (path) => {
    const http = installCollectionHTTP({
      collections: () => [],
      items: () => [],
      intercept: (url) =>
        url.pathname === '/api/literature/candidates'
          ? json({ schema: 'x-science.literature-candidates.v1', candidates: [] })
          : undefined,
    });
    await mountCollections(path);
    await screen.findByRole('heading', {
      name: path.includes('inbox') ? 'Inbox' : 'Trash',
      level: path.includes('inbox') ? 2 : 1,
    });
    expect(screen.queryByRole('button', { name: 'Add' })).toBeNull();
    expect(http.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
  }
);

it('includes current collection revision in the single create then opens canonical detail', async () => {
  const current = { ...collection('methods', 'Methods'), revision: 7 };
  let committed = false;
  const http = installCollectionHTTP({
    collections: () => [current],
    items: () => (committed ? [saved] : []),
    detail: () => saved,
    memberships: () => (committed ? ['methods'] : []),
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        committed = true;
        return json(receipt(JSON.parse(String(init.body)).operation_id));
      }
    },
  });
  await mountCollections('/library?collection=methods');
  const dialog = await openForm();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create reference' }));
  await screen.findByRole('heading', { name: saved.title });
  const writes = http.requests.filter((request) => request.method !== 'GET');
  expect(writes).toHaveLength(1);
  expect(JSON.parse(writes[0].body!)).toMatchObject({ collection_id: 'methods', expected_collection_revision: 7 });
  expect(screen.getByLabelText('Library location')).toHaveTextContent('item=manual');
  expect(screen.getByLabelText('Library location')).not.toHaveTextContent('collection=methods');
  await waitFor(() =>
    expect(http.requests.some((request) => request.path === '/api/literature/items/manual/collections')).toBe(true)
  );
});

it.each(['manual', 'pdf'])('consumes native Enter once for the Add %s destination', async (destination) => {
  installCollectionHTTP({ collections: () => [], items: () => [] });
  await mountCollections();
  const trigger = screen.getByRole('button', { name: 'Add' });
  fireEvent.click(trigger);
  const selected = await screen.findByRole('menuitem', {
    name: destination === 'manual' ? 'Add reference' : 'Import PDF',
  });
  await waitFor(() => expect(selected).not.toHaveAttribute('aria-disabled', 'true'));
  const chooser = screen.getByLabelText('Select a PDF file') as HTMLInputElement;
  const choose = vi.spyOn(chooser, 'click');
  selected.focus();
  const down = createEvent.keyDown(selected, {
    key: 'Enter',
    code: 'Enter',
    keyCode: 13,
    which: 13,
    bubbles: true,
    cancelable: true,
  });
  if (fireEvent(selected, down) && document.activeElement instanceof HTMLElement)
    fireEvent.click(document.activeElement);
  if (document.activeElement) fireEvent.keyUp(document.activeElement, { key: 'Enter', keyCode: 13 });
  expect(down.defaultPrevented).toBe(true);
  // Arco replaces a trigger when the newly opened editor disables it. Assert
  // the current accessible trigger rather than a detached former DOM node.
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toHaveAttribute('aria-expanded', 'false'));
  if (destination === 'manual') {
    await screen.findByRole('dialog', { name: 'Add reference' });
    expect(choose).not.toHaveBeenCalled();
  } else {
    expect(choose).toHaveBeenCalledTimes(1);
    expect(chooser.isConnected).toBe(true);
    expect(screen.queryByRole('dialog', { name: 'Add reference' })).toBeNull();
  }
});
