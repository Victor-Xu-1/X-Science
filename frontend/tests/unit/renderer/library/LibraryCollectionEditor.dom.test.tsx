import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import {
  collection,
  collectionAck,
  deferred,
  installCollectionHTTP,
  json,
  mountCollections,
  paper,
} from './libraryCollectionsFixtures';

const open = async (name: 'New collection' | 'Edit collection' | 'Delete collection') => {
  if (name === 'New collection') {
    const button = await screen.findByRole('button', { name });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);
  } else {
    const more = await screen.findByRole('button', { name: 'More actions' });
    await waitFor(() => expect(more).toBeEnabled());
    fireEvent.click(more);
    const action = await screen.findByRole('menuitem', { name });
    expect(action).not.toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(action);
  }
  return within(await screen.findByRole('dialog', { name }));
};

describe('Collection editors use canonical receipts and retained original requests', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-editor-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('keeps the draft after a revision conflict and requires explicit latest-version adoption before another update', async () => {
    let current = collection('current', 'Initial collection');
    const writes: Record<string, unknown>[] = [];
    installCollectionHTTP({
      collections: () => [current],
      intercept: (url, init) => {
        if (url.pathname !== '/api/literature/collections/current' || init.method !== 'PATCH') return undefined;
        const body = JSON.parse(String(init.body));
        writes.push(body);
        if (body.expected_revision !== current.revision) return json({ code: 'LITERATURE_CONFLICT' }, 409);
        current = { ...current, revision: current.revision + 1, name: body.name, description: body.description };
        return json(collectionAck(body.operation_id, 'update', current));
      },
    });
    await mountCollections('/library?collection=current');
    const editor = await open('Edit collection');
    fireEvent.change(editor.getByRole('textbox', { name: 'Name' }), { target: { value: 'My unsaved draft' } });
    current = { ...current, revision: 2, name: 'Newer persisted name', description: 'Newer persisted description' };
    fireEvent.click(editor.getByRole('button', { name: 'Save changes' }));
    expect(await editor.findByText('This collection changed. Your edits have been kept.')).toBeInTheDocument();
    expect(editor.getByRole('textbox', { name: 'Name' })).toHaveValue('My unsaved draft');
    expect(editor.getByRole('button', { name: 'Save changes' })).toBeDisabled();
    const load = await editor.findByRole('button', { name: 'Load latest version' });
    await waitFor(() => expect(load).toBeEnabled());
    fireEvent.click(load);
    expect(editor.getByRole('textbox', { name: 'Name' })).toHaveValue('Newer persisted name');
    expect(editor.getByRole('textbox', { name: 'Description' })).toHaveValue('Newer persisted description');
    fireEvent.change(editor.getByRole('textbox', { name: 'Name' }), { target: { value: 'Merged collection' } });
    fireEvent.click(editor.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(await screen.findByRole('heading', { name: 'Merged collection', level: 1 })).toBeInTheDocument();
    expect(writes.map((body) => body.expected_revision)).toEqual([1, 2]);
    expect(writes[0].operation_id).not.toBe(writes[1].operation_id);
  });

  it('preserves a name-conflict draft and submits a corrected name as a new definite attempt', async () => {
    let current = collection('current', 'Initial collection');
    const writes: Record<string, unknown>[] = [];
    installCollectionHTTP({
      collections: () => [current],
      intercept: (url, init) => {
        if (url.pathname !== '/api/literature/collections/current' || init.method !== 'PATCH') return undefined;
        const body = JSON.parse(String(init.body));
        writes.push(body);
        if (writes.length === 1) return json({ code: 'LITERATURE_COLLECTION_NAME_CONFLICT' }, 409);
        current = { ...current, name: body.name, description: body.description, revision: 2 };
        return json(collectionAck(body.operation_id, 'update', current));
      },
    });
    await mountCollections('/library?collection=current');
    const editor = await open('Edit collection');
    fireEvent.change(editor.getByRole('textbox', { name: 'Name' }), { target: { value: 'Already used' } });
    fireEvent.click(editor.getByRole('button', { name: 'Save changes' }));
    expect(
      await editor.findByText('A collection with this name already exists at this level. Choose another name.')
    ).toBeInTheDocument();
    expect(editor.getByRole('textbox', { name: 'Name' })).toHaveValue('Already used');
    expect(editor.getByRole('button', { name: 'Cancel' })).toBeEnabled();
    fireEvent.change(editor.getByRole('textbox', { name: 'Name' }), { target: { value: 'Available name' } });
    fireEvent.click(editor.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(writes.map((body) => body.name)).toEqual(['Already used', 'Available name']);
    expect(writes[0].operation_id).not.toBe(writes[1].operation_id);
  });

  it('blocks close only in flight, then allows closing an unknown result while retaining the exact request for global retry', async () => {
    let collections: ReturnType<typeof collection>[] = [];
    let lost = true,
      applied = 0;
    const flight = deferred<void>();
    const writes: string[] = [];
    const receipts = new Map<string, unknown>();
    installCollectionHTTP({
      collections: () => collections,
      intercept: async (url, init) => {
        if (url.pathname !== '/api/literature/collections' || init.method !== 'POST') return undefined;
        const raw = String(init.body);
        writes.push(raw);
        const body = JSON.parse(raw);
        if (!receipts.has(body.operation_id)) {
          const created = { ...collection('created', body.name), description: body.description };
          collections = [created];
          applied += 1;
          receipts.set(body.operation_id, collectionAck(body.operation_id, 'create', created));
        }
        if (lost) {
          lost = false;
          await flight.promise;
          throw new TypeError('controlled lost collection response');
        }
        return json(receipts.get(body.operation_id));
      },
    });
    await mountCollections();
    const editor = await open('New collection');
    fireEvent.change(editor.getByRole('textbox', { name: 'Name' }), { target: { value: 'Recoverable collection' } });
    fireEvent.click(editor.getByRole('button', { name: 'Create collection' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(editor.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(editor.getByRole('button', { name: 'Close' })).toBeDisabled();
    fireEvent.keyDown(editor.getByRole('textbox', { name: 'Name' }), { key: 'Escape' });
    fireEvent.pointerDown(screen.getByRole('dialog', { name: 'New collection' }).parentElement!);
    expect(screen.getByRole('dialog', { name: 'New collection' })).toBeInTheDocument();
    await act(async () => {
      flight.resolve();
      await flight.promise;
    });
    expect(
      await editor.findByText(
        'The result is still pending confirmation. You can close this window and retry the original change from the Library notice.'
      )
    ).toBeInTheDocument();
    expect(editor.getByRole('textbox', { name: 'Name' })).toBeDisabled();
    expect(editor.getByRole('button', { name: 'Retry pending Library change' })).toBeEnabled();
    const closeButtons = editor.getAllByRole('button', { name: 'Close' });
    expect(closeButtons).toHaveLength(2);
    for (const button of closeButtons) expect(button).toBeEnabled();
    const key = 'x-science.literature-mutation.v1:library-editor-owner';
    const pending = localStorage.getItem(key);
    expect(pending).not.toBeNull();
    const original = JSON.parse(pending!).command;
    expect(original.operation_id).toBe(JSON.parse(writes[0]).operation_id);
    fireEvent.click(closeButtons[1]);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(localStorage.getItem(key)).toBe(pending);
    expect(screen.getByRole('button', { name: 'New collection' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('button', { name: 'Recoverable collection' })).toBeInTheDocument();
    expect(screen.getByLabelText('Library location').textContent).toBe('');
    expect(writes).toHaveLength(2);
    expect(writes[0]).toBe(writes[1]);
    expect(applied).toBe(1);
    expect(localStorage.getItem(key)).toBeNull();
  });

  it('shows child-promotion collisions, supports renaming the child, and leaves references after deleting only the parent', async () => {
    let collections = [
      collection('parent', 'Parent'),
      collection('child', 'Shared name', 'parent'),
      collection('root', 'Shared name'),
    ];
    const writes: { method: string; body: Record<string, unknown> }[] = [];
    installCollectionHTTP({
      collections: () => collections,
      items: () => [paper()],
      intercept: (url, init) => {
        if (url.pathname === '/api/literature/collections/child' && init.method === 'PATCH') {
          const body = JSON.parse(String(init.body));
          writes.push({ method: 'PATCH', body });
          const child = {
            ...collections.find((value) => value.id === 'child')!,
            name: body.name,
            description: body.description,
            revision: 2,
          };
          collections = collections.map((value) => (value.id === 'child' ? child : value));
          return json(collectionAck(body.operation_id, 'update', child));
        }
        if (url.pathname === '/api/literature/collections/parent' && init.method === 'DELETE') {
          const body = JSON.parse(String(init.body));
          writes.push({ method: 'DELETE', body });
          collections = collections
            .filter((value) => value.id !== 'parent')
            .map((value) => (value.id === 'child' ? { ...value, parent_id: null, revision: 3 } : value));
          return json({
            schema: 'x-science.literature-collection-receipt.v1',
            operation_id: body.operation_id,
            action: 'delete',
            collection_id: 'parent',
          });
        }
        return undefined;
      },
    });
    await mountCollections('/library?collection=parent');
    const deletion = await open('Delete collection');
    expect(
      deletion.getByText(
        'References and their attachments will remain in All references. This removes the collection only.'
      )
    ).toBeInTheDocument();
    expect(deletion.getByRole('button', { name: 'Delete collection' })).toBeDisabled();
    fireEvent.click(deletion.getByRole('button', { name: 'Rename child collection' }));
    const rename = within(await screen.findByRole('dialog', { name: 'Edit collection' }));
    fireEvent.change(rename.getByRole('textbox', { name: 'Name' }), { target: { value: 'Promoted child' } });
    fireEvent.click(rename.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(await screen.findByRole('button', { name: 'Parent / Promoted child' })).toBeInTheDocument();
    const next = await open('Delete collection');
    fireEvent.click(next.getByRole('button', { name: 'Delete collection' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(screen.getByLabelText('Library location').textContent).toBe(''));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Paper A' })).toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Promoted child' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Parent' })).toBeNull();
    expect(writes.map((value) => value.method)).toEqual(['PATCH', 'DELETE']);
    expect(writes[0].body.expected_revision).toBe(1);
    expect(writes[1].body).not.toHaveProperty('expected_revision');
  });

  it('does not select a late created collection or reopen its editor in another account', async () => {
    const reply = deferred<Response>();
    let nextOwner = false,
      started = false,
      body: Record<string, unknown> | undefined;
    installCollectionHTTP({
      collections: () => (nextOwner ? [collection('next', 'Next owner collection')] : []),
      intercept: (url, init) => {
        if (url.pathname === '/api/literature/collections' && init.method === 'POST') {
          body = JSON.parse(String(init.body));
          started = true;
          return reply.promise;
        }
        return undefined;
      },
    });
    await mountCollections();
    const editor = await open('New collection');
    fireEvent.change(editor.getByRole('textbox', { name: 'Name' }), { target: { value: 'Former owner collection' } });
    fireEvent.click(editor.getByRole('button', { name: 'Create collection' }));
    await waitFor(() => expect(started).toBe(true));
    act(() => {
      nextOwner = true;
      setRendererAccountOwner('next-library-editor-owner');
    });
    expect(await screen.findByRole('button', { name: 'Next owner collection' })).toBeInTheDocument();
    await act(async () => {
      reply.resolve(
        json(collectionAck(String(body!.operation_id), 'create', collection('former', 'Former owner collection')))
      );
      await reply.promise;
    });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByText('Former owner collection')).toBeNull();
    expect(screen.getByLabelText('Library location').textContent).toBe('');
    expect(screen.getByRole('button', { name: 'New collection' })).toBeEnabled();
  });
});
