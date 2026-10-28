import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LibraryCollectionMembershipPicker } from '@/renderer/pages/library/LibraryCollectionMembership';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../../i18nTestUtils';
import {
  collection,
  deferred,
  installCollectionHTTP,
  json,
  mountCollections,
  paper,
} from './libraryCollectionsFixtures';

type PickerProps = React.ComponentProps<typeof LibraryCollectionMembershipPicker>;
const folders = () => [
  collection('parent', 'Parent'),
  collection('target', 'Alpha child', 'parent'),
  collection('beta', 'Beta'),
  collection('c', 'Gamma'),
  collection('d', 'Delta'),
  collection('e', 'Epsilon'),
];
function props(id = 'a'): PickerProps {
  return {
    item: paper(id),
    account: getRendererAccountScopeToken(),
    collections: {
      key: 'directory',
      status: 'ready',
      value: { schema: 'x-science.literature-collections.v1', collections: folders() },
    },
    memberships: {
      key: id,
      status: 'ready',
      value: { schema: 'x-science.literature-collection-membership.v1', item_id: id, collection_ids: [] },
    },
    busy: false,
    submit: vi.fn(),
    retry: vi.fn(),
  };
}
const membership = () => within(screen.getByRole('region', { name: 'Collection membership' }));

describe('Collection membership local name search through existing owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('membership-search-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('filters more than five names locally and submits the original target id through the Page owner', async () => {
    let included = false;
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    const http = installCollectionHTTP({
      collections: folders,
      memberships: () => (included ? ['target'] : []),
      intercept: (url, init) => {
        if (init.method !== 'PUT') return undefined;
        const body = JSON.parse(String(init.body));
        writes.push({ path: url.pathname, body });
        included = body.included;
        return json({
          schema: 'x-science.literature-collection-receipt.v1',
          operation_id: body.operation_id,
          action: 'membership',
          collection_id: 'target',
          item_id: 'a',
          included,
        });
      },
    });
    const rendered = await mountCollections();
    fireEvent.click(await screen.findByRole('button', { name: 'Paper A' }));
    await waitFor(() => expect(membership().getByRole('checkbox', { name: 'Parent / Alpha child' })).toBeEnabled());
    const search = membership().getByRole('searchbox', { name: 'Search collections' });
    const requestCount = http.requests.length;
    fireEvent.change(search, { target: { value: '  aLPHa  ' } });
    expect(membership().getAllByRole('checkbox')).toHaveLength(1);
    expect(membership().getByRole('checkbox', { name: 'Parent / Alpha child' })).toBeInTheDocument();
    fireEvent.change(search, { target: { value: 'Parent' } });
    expect(membership().getAllByRole('checkbox')).toHaveLength(1);
    expect(membership().getByRole('checkbox', { name: 'Parent' })).toBeInTheDocument();
    fireEvent.change(search, { target: { value: 'no such name' } });
    expect(membership().queryByRole('checkbox')).toBeNull();
    expect(membership().getByText('No matching collections')).toBeInTheDocument();
    fireEvent.change(search, { target: { value: '  ' } });
    expect(membership().getAllByRole('checkbox')).toHaveLength(6);
    fireEvent.change(search, { target: { value: 'alpha' } });
    expect(http.requests).toHaveLength(requestCount);
    fireEvent.click(membership().getByRole('checkbox', { name: 'Parent / Alpha child' }));
    await waitFor(() => expect(membership().getByRole('checkbox', { name: 'Parent / Alpha child' })).toBeChecked());
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({ path: '/api/literature/collections/target/items/a', body: { included: true } });
    expect(typeof writes[0].body.operation_id).toBe('string');
    expect(membership().getByRole('searchbox', { name: 'Search collections' })).toHaveValue('alpha');
    await act(async () => {
      await rendered.i18n.changeLanguage('zh-CN');
    });
    const chinese = within(screen.getByRole('region', { name: '合集成员关系' }));
    const translated = chinese.getByRole('searchbox', { name: '搜索合集' });
    fireEvent.change(translated, { target: { value: 'missing' } });
    expect(chinese.getByText('没有匹配的合集')).toBeInTheDocument();
  });

  it('keeps five-or-fewer, empty, loading and recoverable error states distinct', async () => {
    let value = props();
    value.collections = {
      key: 'directory',
      status: 'ready',
      value: { schema: 'x-science.literature-collections.v1', collections: folders().slice(0, 5) },
    };
    const rendered = await renderWithI18n(<LibraryCollectionMembershipPicker {...value} />, 'en-US');
    expect(screen.queryByRole('searchbox')).toBeNull();
    expect(membership().getAllByRole('checkbox')).toHaveLength(5);
    value = {
      ...value,
      collections: {
        key: 'directory',
        status: 'ready',
        value: { schema: 'x-science.literature-collections.v1', collections: [] },
      },
    };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByText('Create a collection to organize your references.')).toBeInTheDocument();
    expect(screen.queryByText('No matching collections')).toBeNull();
    value = { ...value, collections: { key: 'directory', status: 'loading' } };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByRole('status')).toBeInTheDocument();
    expect(screen.queryByRole('searchbox')).toBeNull();
    value = { ...value, collections: { key: 'directory', status: 'error' } };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByRole('alert')).toHaveTextContent('Could not read collection membership.');
    fireEvent.click(membership().getByRole('button', { name: 'Retry' }));
    expect(value.retry).toHaveBeenCalledTimes(1);
    expect(membership().queryByRole('checkbox')).toBeNull();
    value = props();
    value.collections = { ...value.collections, refreshing: true, refreshError: true };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByRole('alert')).toHaveTextContent(
      'Could not load collections. Your references are still available.'
    );
    expect(
      membership()
        .getAllByRole('checkbox')
        .every((checkbox) => checkbox.hasAttribute('disabled') || (checkbox as HTMLInputElement).matches(':disabled'))
    ).toBe(true);
    expect(membership().getByRole('searchbox')).toBeEnabled();
  });

  it('resets local query on item/account changes and keeps old write completion outside the new pending owner', async () => {
    const old = deferred<Awaited<ReturnType<PickerProps['submit']>>>();
    const next = deferred<Awaited<ReturnType<PickerProps['submit']>>>();
    const submit = vi
      .fn<PickerProps['submit']>()
      .mockImplementationOnce(() => old.promise)
      .mockImplementationOnce(() => next.promise);
    let value = { ...props(), submit };
    const rendered = await renderWithI18n(<LibraryCollectionMembershipPicker {...value} />, 'en-US');
    fireEvent.change(membership().getByRole('searchbox'), { target: { value: 'alpha' } });
    fireEvent.click(membership().getByRole('checkbox', { name: 'Parent / Alpha child' }));
    value = { ...props('b'), submit };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByRole('searchbox')).toHaveValue('');
    fireEvent.change(membership().getByRole('searchbox'), { target: { value: 'beta' } });
    fireEvent.click(membership().getByRole('checkbox', { name: 'Beta' }));
    expect(
      submit.mock.calls.map(([command]) =>
        command.kind === 'collection-membership' ? [command.collection_id, command.item_id] : null
      )
    ).toEqual([
      ['target', 'a'],
      ['beta', 'b'],
    ]);
    await act(async () => {
      const command = submit.mock.calls[0][0];
      old.resolve({
        schema: 'x-science.literature-collection-receipt.v1',
        operation_id: command.operation_id,
        action: 'membership',
        collection_id: 'target',
        item_id: 'a',
        included: true,
      });
      await old.promise;
    });
    expect(membership().getByRole('checkbox', { name: 'Beta' })).toBeDisabled();
    expect(membership().getByRole('searchbox')).toHaveValue('beta');
    await act(async () => {
      const command = submit.mock.calls[1][0];
      next.resolve({
        schema: 'x-science.literature-collection-receipt.v1',
        operation_id: command.operation_id,
        action: 'membership',
        collection_id: 'beta',
        item_id: 'b',
        included: true,
      });
      await next.promise;
    });
    expect(membership().getByRole('checkbox', { name: 'Beta' })).toBeEnabled();
    value = { ...value, item: { ...value.item, metadata_revision: 2 } };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByRole('searchbox')).toHaveValue('beta');
    act(() => {
      setRendererAccountOwner('membership-search-next-owner');
    });
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(screen.queryByRole('region', { name: 'Collection membership' })).toBeNull();
    value = { ...value, account: getRendererAccountScopeToken() };
    rendered.rerender(<LibraryCollectionMembershipPicker {...value} />);
    expect(membership().getByRole('searchbox')).toHaveValue('');
    expect(membership().getAllByRole('checkbox')).toHaveLength(6);
    expect(submit).toHaveBeenCalledTimes(2);
  });
});
