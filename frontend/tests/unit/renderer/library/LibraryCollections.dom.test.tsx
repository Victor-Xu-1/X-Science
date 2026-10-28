import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { LibraryCollectionMembershipPicker } from '@/renderer/pages/library/LibraryCollectionMembership';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { renderWithI18n } from '../../i18nTestUtils';
import {
  collection,
  collectionAck,
  deferred,
  installCollectionHTTP,
  json,
  mountCollections,
  page,
  paper,
} from './libraryCollectionsFixtures';

const sidebar = () => within(screen.getByRole('navigation', { name: 'Collections' }));
const location = () => new URLSearchParams(screen.getByLabelText('Library location').textContent ?? '');

describe('Library collections through the original Web request owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-collection-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('rail retention: gates dependent directory writes while keeping metadata and close usable', async () => {
    const folders = [collection('methods', 'Methods')];
    const projects = [{ project_id: 'alpha', name: 'Alpha project' }];
    let held = false,
      collectionRead = deferred<Response>(),
      projectRead = deferred<Response>();
    let entry = { ...paper(), project_ids: ['alpha'] };
    const http = installCollectionHTTP({
      collections: () => folders,
      detail: () => entry,
      intercept: (url, init) => {
        if (url.pathname === '/api/projects') return held ? projectRead.promise : json({ projects });
        if (url.pathname === '/api/literature/collections' && held) return collectionRead.promise;
        if (url.pathname === '/api/literature/items/a' && init.method === 'PATCH') {
          const command = JSON.parse(String(init.body));
          entry = { ...entry, title: command.title, metadata_revision: entry.metadata_revision + 1 };
          return json({ schema: 'x-science.literature-receipt.v1', operation_id: command.operation_id, item: entry });
        }
        return undefined;
      },
    });
    await mountCollections();
    await screen.findByRole('option', { name: 'Alpha project' });
    await screen.findByRole('button', { name: 'Methods' });
    fireEvent.click(sidebar().getByRole('button', { name: 'New collection' }));
    const collectionDialog = screen.getByRole('dialog', { name: 'New collection' });
    const name = within(collectionDialog).getByRole('textbox', { name: 'Name' });
    fireEvent.change(name, { target: { value: 'Kept draft' } });
    held = true;
    // Exercise the existing read owner's refresh while its modal subscriber stays mounted.
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(within(collectionDialog).getByRole('button', { name: 'Create collection' })).toBeDisabled();
    expect(within(collectionDialog).getByRole('combobox', { name: 'Parent collection' })).toBeDisabled();
    expect(name).toBeEnabled();
    expect(name).toHaveValue('Kept draft');
    fireEvent.submit(collectionDialog.querySelector('form')!);
    expect(http.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
    fireEvent.click(within(collectionDialog).getByRole('button', { name: 'Cancel' }));
    held = false;
    await act(async () => {
      collectionRead.resolve(json({ schema: 'x-science.literature-collections.v1', collections: folders }));
      projectRead.resolve(json({ projects }));
    });
    await waitFor(() => expect(sidebar().getByRole('button', { name: 'New collection' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Paper A' }));
    const detail = await screen.findByRole('dialog', { name: 'Paper A' });
    fireEvent.click(within(detail).getByRole('button', { name: 'Edit details' }));
    let editor = within(detail).getByRole('region', { name: 'Edit details' });
    fireEvent.change(within(editor).getByRole('combobox', { name: 'Project membership' }), {
      target: { value: 'alpha' },
    });
    fireEvent.click(within(editor).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(within(detail).queryByRole('region', { name: 'Edit details' })).toBeNull());
    held = true;
    collectionRead = deferred<Response>();
    projectRead = deferred<Response>();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(within(detail).getByRole('checkbox', { name: 'Methods' })).toBeDisabled());
    fireEvent.click(within(detail).getByRole('checkbox', { name: 'Methods' }));
    await waitFor(() => expect(within(detail).getByRole('button', { name: 'Edit details' })).toBeEnabled());
    fireEvent.click(within(detail).getByRole('button', { name: 'Edit details' }));
    editor = within(detail).getByRole('region', { name: 'Edit details' });
    expect(within(editor).getByRole('combobox', { name: 'Project membership' })).toHaveValue('alpha');
    expect(within(editor).getByRole('combobox', { name: 'Project membership' })).toBeDisabled();
    expect(within(editor).getByRole('button', { name: 'Remove from project' })).toBeDisabled();
    const title = within(editor).getByRole('textbox', { name: 'Title' });
    expect(title).toBeEnabled();
    fireEvent.change(title, { target: { value: 'Metadata remains writable' } });
    fireEvent.click(within(editor).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(entry.title).toBe('Metadata remains writable'));
    expect(http.requests.filter((request) => request.method !== 'GET').map((request) => request.method)).toEqual([
      'PATCH',
    ]);
  });

  it('creates a child, selects its real filter, and reads membership changes back into the tree and detail', async () => {
    let collections = [collection('methods', 'Methods')];
    let included = false;
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    const http = installCollectionHTTP({
      collections: () => collections,
      memberships: () => (included ? ['methods'] : []),
      items: (url) =>
        !url.searchParams.get('collection_id') || (url.searchParams.get('collection_id') === 'methods' && included)
          ? [paper()]
          : [],
      intercept: (url, init) => {
        if (init.method === 'POST' && url.pathname === '/api/literature/collections') {
          const body = JSON.parse(String(init.body));
          writes.push({ path: url.pathname, body });
          const created = { ...collection('created', body.name, body.parent_id), description: body.description };
          collections = [...collections, created];
          return json(collectionAck(body.operation_id, 'create', created));
        }
        if (init.method === 'PUT' && url.pathname === '/api/literature/collections/methods/items/a') {
          const body = JSON.parse(String(init.body));
          writes.push({ path: url.pathname, body });
          included = body.included;
          collections = collections.map((value) =>
            value.id === 'methods' ? { ...value, item_count: included ? 1 : 0 } : value
          );
          return json({
            schema: 'x-science.literature-collection-receipt.v1',
            operation_id: body.operation_id,
            action: 'membership',
            collection_id: 'methods',
            item_id: 'a',
            included,
          });
        }
        return undefined;
      },
    });
    const rendered = await mountCollections();
    await waitFor(() => expect(sidebar().getByRole('button', { name: 'New collection' })).toBeEnabled());
    fireEvent.click(sidebar().getByRole('button', { name: 'New collection' }));
    const dialog = within(screen.getByRole('dialog', { name: 'New collection' }));
    fireEvent.change(dialog.getByRole('textbox', { name: 'Name' }), { target: { value: '  My   collection  ' } });
    fireEvent.change(dialog.getByRole('textbox', { name: 'Description' }), {
      target: { value: '  Selected\n references  ' },
    });
    fireEvent.change(dialog.getByRole('combobox', { name: 'Parent collection' }), { target: { value: 'methods' } });
    fireEvent.click(dialog.getByRole('button', { name: 'Create collection' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(await screen.findByRole('heading', { name: 'My collection', level: 1 })).toBeInTheDocument();
    expect(sidebar().getByRole('button', { name: 'Methods / My collection' })).toHaveAttribute('aria-current', 'page');
    expect(writes[0].body).toMatchObject({
      name: 'My collection',
      description: 'Selected references',
      parent_id: 'methods',
    });
    expect(location().get('collection')).toBe('created');
    await waitFor(() =>
      expect(
        http.requests.some(
          (request) => new URL(request.path, 'http://localhost').searchParams.get('collection_id') === 'created'
        )
      ).toBe(true)
    );
    fireEvent.click(sidebar().getByRole('button', { name: 'All references' }));
    fireEvent.click(await screen.findByRole('button', { name: /Paper A/ }));
    const checkbox = await screen.findByRole('checkbox', { name: 'Methods' });
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);
    await waitFor(() => expect(screen.getByRole('checkbox', { name: 'Methods' })).toBeChecked());
    expect(writes[1]).toMatchObject({ path: '/api/literature/collections/methods/items/a', body: { included: true } });
    expect(sidebar().getByRole('button', { name: 'Methods' })).toHaveTextContent('1');
    fireEvent.click(sidebar().getByRole('button', { name: 'Methods' }));
    expect(await screen.findByRole('button', { name: /Paper A/ })).toBeInTheDocument();
    expect(location().get('item')).toBeNull();
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('navigation', { name: '合集' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新建合集' })).toBeInTheDocument();
  });

  it('binds collection filters to paging, clears an old cursor and selection, and ignores a late former filter', async () => {
    const old = deferred<Response>();
    let oldStarted = false;
    const http = installCollectionHTTP({
      collections: () => [collection('z', 'Aardvark'), collection('a', 'Alpha'), collection('b', 'Beta')],
      intercept: (url) => {
        if (url.pathname !== '/api/literature/items') return undefined;
        if (url.searchParams.get('collection_id') === 'a') {
          oldStarted = true;
          return old.promise;
        }
        return json(
          page(
            [
              paper(
                url.searchParams.has('cursor') ? 'c' : 'b',
                url.searchParams.has('cursor') ? 'Second Beta page' : 'Beta paper'
              ),
            ],
            url.searchParams.has('cursor') ? undefined : 'beta-next'
          )
        );
      },
    });
    await mountCollections('/library?collection=a&cursor=alpha-next&item=a&q=study');
    await waitFor(() => expect(oldStarted).toBe(true));
    expect(
      sidebar()
        .getAllByRole('button')
        .map((button) => button.getAttribute('aria-label'))
        .filter((name) => ['Aardvark', 'Alpha', 'Beta'].includes(name ?? ''))
    ).toEqual(['Aardvark', 'Alpha', 'Beta']);
    fireEvent.click(sidebar().getByRole('button', { name: 'Beta' }));
    expect(await screen.findByRole('button', { name: /Beta paper/ })).toBeInTheDocument();
    expect(location().get('collection')).toBe('b');
    expect(location().get('q')).toBe('study');
    expect(location().get('cursor')).toBeNull();
    expect(location().get('item')).toBeNull();
    await act(async () => {
      old.resolve(json(page([paper('old', 'Late Alpha page')])));
      await old.promise;
    });
    expect(screen.queryByText('Late Alpha page')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    expect(await screen.findByRole('button', { name: /Second Beta page/ })).toBeInTheDocument();
    expect(location().get('cursor')).toBe('beta-next');
    fireEvent.click(screen.getByRole('button', { name: 'Back to first page' }));
    expect(await screen.findByRole('button', { name: /Beta paper/ })).toBeInTheDocument();
    expect(location().get('collection')).toBe('b');
    const beta = http.requests.filter(
      (request) =>
        request.path.startsWith('/api/literature/items?') &&
        new URL(request.path, 'http://localhost').searchParams.get('collection_id') === 'b'
    );
    expect(
      beta.some((request) => new URL(request.path, 'http://localhost').searchParams.get('cursor') === 'beta-next')
    ).toBe(true);
    expect(
      beta.every((request) => new URL(request.path, 'http://localhost').searchParams.get('cursor') !== 'alpha-next')
    ).toBe(true);
  });

  it('keeps old-account directory and same-item membership replies out of the new account', async () => {
    const oldDirectory = deferred<Response>();
    const oldMembership = deferred<Response>();
    let nextOwner = false,
      refreshing = false,
      directoryStarted = false,
      membershipStarted = false;
    installCollectionHTTP({
      collections: () => [collection('same', nextOwner ? 'Next owner collection' : 'Old owner collection')],
      detail: () => paper('a', nextOwner ? 'Next owner paper' : 'Old owner paper'),
      intercept: (url) => {
        if (url.pathname === '/api/literature/collections' && !nextOwner && refreshing) {
          directoryStarted = true;
          return oldDirectory.promise;
        }
        if (url.pathname === '/api/literature/items/a/collections' && !nextOwner) {
          membershipStarted = true;
          return oldMembership.promise;
        }
        return undefined;
      },
    });
    await mountCollections('/library?item=a');
    expect(await screen.findByRole('heading', { name: 'Old owner paper' })).toBeInTheDocument();
    await waitFor(() => expect(membershipStarted).toBe(true));
    refreshing = true;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(directoryStarted).toBe(true));
    act(() => {
      nextOwner = true;
      setRendererAccountOwner('next-library-owner');
    });
    const checkbox = await screen.findByRole('checkbox', { name: 'Next owner collection' });
    expect(checkbox).not.toBeChecked();
    expect(screen.getByRole('heading', { name: 'Next owner paper' })).toBeInTheDocument();
    await act(async () => {
      oldDirectory.resolve(
        json({ schema: 'x-science.literature-collections.v1', collections: [collection('same', 'Late old directory')] })
      );
      oldMembership.resolve(
        json({ schema: 'x-science.literature-collection-membership.v1', item_id: 'a', collection_ids: ['same'] })
      );
      await Promise.all([oldDirectory.promise, oldMembership.promise]);
    });
    expect(screen.queryByText('Late old directory')).toBeNull();
    expect(screen.queryByText('Old owner collection')).toBeNull();
    expect(screen.getByRole('checkbox', { name: 'Next owner collection' })).not.toBeChecked();
  });

  it('shows a failed directory as unavailable, then retries without inventing empty membership', async () => {
    let failing = true;
    installCollectionHTTP({
      collections: () => [collection('current', 'Current collection')],
      memberships: () => ['current'],
      intercept: (url) =>
        failing && url.pathname === '/api/literature/collections' ? json({ code: 'UNAVAILABLE' }, 503) : undefined,
    });
    await mountCollections('/library?item=a');
    expect(await screen.findByRole('heading', { name: 'Paper A' })).toBeInTheDocument();
    expect(
      await sidebar().findByText('Could not load collections. Your references are still available.')
    ).toBeInTheDocument();
    const membership = within(screen.getByRole('region', { name: 'Collection membership' }));
    expect(membership.getByRole('alert')).toHaveTextContent('Could not read collection membership.');
    expect(membership.queryByRole('checkbox')).toBeNull();
    expect(sidebar().getByRole('button', { name: 'New collection' })).toBeDisabled();
    failing = false;
    fireEvent.click(sidebar().getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('checkbox', { name: 'Current collection' })).toBeChecked();
    expect(sidebar().getByRole('button', { name: 'New collection' })).toBeEnabled();
  });

  it('lets the same mounted membership consumer submit for a new account while its old mutation is unresolved', async () => {
    const oldReply = deferred<Response>();
    const newReply = deferred<Response>();
    const writes: { owner: string; body: Record<string, unknown> }[] = [];
    installCollectionHTTP({
      collections: () => [],
      intercept: (url, init) => {
        if (url.pathname !== '/api/literature/collections/shared/items/a' || init.method !== 'PUT') return undefined;
        writes.push({ owner: getRendererAccountOwnerId(), body: JSON.parse(String(init.body)) });
        return writes.length === 1 ? oldReply.promise : newReply.promise;
      },
    });
    function SameMountedPicker() {
      const mutation = useLiteratureMutation();
      const account = getRendererAccountScopeToken();
      const name =
        getRendererAccountOwnerId() === 'library-collection-owner'
          ? 'Old account collection'
          : 'New account collection';
      return (
        <LibraryCollectionMembershipPicker
          item={paper()}
          account={account}
          collections={{
            key: account,
            status: 'ready',
            value: { schema: 'x-science.literature-collections.v1', collections: [collection('shared', name)] },
          }}
          memberships={{
            key: account,
            status: 'ready',
            value: { schema: 'x-science.literature-collection-membership.v1', item_id: 'a', collection_ids: [] },
          }}
          busy={mutation.view.mutating || Boolean(mutation.view.pending) || mutation.view.error === 'storage'}
          submit={(command) => mutation.controller.submit(command)}
          retry={() => undefined}
        />
      );
    }
    await renderWithI18n(<SameMountedPicker />, 'en-US');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Old account collection' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    act(() => {
      setRendererAccountOwner('next-library-collection-owner');
    });
    const next = await screen.findByRole('checkbox', { name: 'New account collection' });
    expect(next).toBeEnabled();
    fireEvent.click(next);
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes.map((write) => write.owner)).toEqual(['library-collection-owner', 'next-library-collection-owner']);
    expect(writes[0].body.operation_id).not.toBe(writes[1].body.operation_id);
    await act(async () => {
      oldReply.resolve(
        json({
          schema: 'x-science.literature-collection-receipt.v1',
          operation_id: writes[0].body.operation_id,
          action: 'membership',
          collection_id: 'shared',
          item_id: 'a',
          included: true,
        })
      );
      await oldReply.promise;
    });
    expect(screen.getByRole('checkbox', { name: 'New account collection' })).toBeDisabled();
    expect(screen.getByRole('group', { name: 'Collection membership' })).toHaveAttribute('aria-busy', 'true');
    await act(async () => {
      newReply.resolve(
        json({
          schema: 'x-science.literature-collection-receipt.v1',
          operation_id: writes[1].body.operation_id,
          action: 'membership',
          collection_id: 'shared',
          item_id: 'a',
          included: true,
        })
      );
      await newReply.promise;
    });
    await waitFor(() => expect(screen.getByRole('checkbox', { name: 'New account collection' })).toBeEnabled());
    expect(screen.queryByRole('checkbox', { name: 'Old account collection' })).toBeNull();
  });
});
