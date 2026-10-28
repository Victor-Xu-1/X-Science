import React, { useEffect, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type {
  LiteratureCollectionPage,
  LiteratureCollectionMembership,
  LiteratureCollectionCommand,
  LiteratureCollectionReceipt,
} from '@/common/adapter/literatureCollections';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { LibraryReadView } from './useLibraryWorkbench';
import { isLibraryReadFresh } from './useLibraryWorkbench';
import { LibraryDirectoryState } from './LibraryDirectoryState';
import { libraryCollectionRows } from './LibraryCollectionSidebar';

export function LibraryCollectionMembershipPicker({
  item,
  account,
  collections,
  memberships,
  busy,
  submit,
  retry,
}: {
  item: LiteratureItem;
  account: string;
  collections: LibraryReadView<LiteratureCollectionPage>;
  memberships: LibraryReadView<LiteratureCollectionMembership | undefined>;
  busy: boolean;
  submit: (command: LiteratureCollectionCommand) => Promise<LiteratureCollectionReceipt>;
  retry: () => void;
}) {
  const { t } = useTranslation();
  const key = JSON.stringify([account, item.id, item.metadata_revision]);
  const searchScope = JSON.stringify([account, item.id]);
  const [search, setSearch] = useState({ scope: searchScope, value: '' });
  if (search.scope !== searchScope) setSearch({ scope: searchScope, value: '' });
  const owner = useRef<{ key: string; active: boolean; pending: object | null }>({ key, active: true, pending: null });
  if (owner.current.key !== key) {
    owner.current.active = false;
    owner.current = { key, active: true, pending: null };
  }
  useEffect(() => {
    const current = owner.current;
    current.active = true;
    return () => {
      current.active = false;
    };
  }, [key]);
  const [status, setStatus] = useState<{ owner: object; failed: boolean; pending: boolean }>();
  const currentStatus = status?.owner === owner.current ? status : undefined;
  if (account !== getRendererAccountScopeToken()) return null;
  const ready =
    collections.status === 'ready' && memberships.status === 'ready' && memberships.value?.item_id === item.id;
  const failed =
    collections.status === 'error' ||
    memberships.status === 'error' ||
    (memberships.status === 'ready' && memberships.value?.item_id !== item.id);
  const entries = collections.value?.collections ?? [];
  const searchable = entries.length > 5;
  const normalizedQuery = searchable && search.scope === searchScope ? search.value.trim().toLocaleLowerCase() : '';
  const visibleRows = libraryCollectionRows(entries).filter(({ collection }) =>
    collection.name.toLocaleLowerCase().includes(normalizedQuery)
  );
  const change = async (collectionId: string, included: boolean) => {
    if (!ready || !isLibraryReadFresh(collections) || busy || owner.current.pending) return;
    const token = owner.current;
    const request = {};
    token.pending = request;
    const current = () => token.active && owner.current === token && account === getRendererAccountScopeToken();
    setStatus({ owner: token, pending: true, failed: false });
    try {
      await submit({
        kind: 'collection-membership',
        operation_id: crypto.randomUUID(),
        collection_id: collectionId,
        item_id: item.id,
        included,
      });
      if (current()) setStatus({ owner: token, pending: false, failed: false });
    } catch {
      if (current()) setStatus({ owner: token, pending: false, failed: true });
    } finally {
      if (token.pending === request) token.pending = null;
    }
  };
  return (
    <section
      className='library-workbench__membership'
      aria-label={t('conversation.libraryWorkbench.collectionMembership')}
    >
      <h3>{t('conversation.libraryWorkbench.collections')}</h3>
      {collections.status === 'ready' && (
        <LibraryDirectoryState
          view={collections}
          errorText={t('conversation.libraryWorkbench.collectionsFailed')}
          retry={retry}
        />
      )}
      {failed ? (
        <div role='alert'>
          <p>{t('conversation.libraryWorkbench.membershipsFailed')}</p>
          <button type='button' onClick={retry}>
            {t('common.retry')}
          </button>
        </div>
      ) : !ready ? (
        <p role='status'>{t('common.loading')}</p>
      ) : !collections.value?.collections.length ? (
        <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noCollections')}</p>
      ) : (
        <>
          {searchable && (
            <label className='library-workbench__search library-workbench__membership-search'>
              <Search size={16} aria-hidden='true' />
              <input
                type='search'
                aria-label={t('conversation.libraryWorkbench.searchCollections')}
                placeholder={t('conversation.libraryWorkbench.searchCollections')}
                autoComplete='off'
                value={search.scope === searchScope ? search.value : ''}
                onChange={(event) => setSearch({ scope: searchScope, value: event.currentTarget.value })}
              />
            </label>
          )}
          {visibleRows.length ? (
            <fieldset
              disabled={busy || !isLibraryReadFresh(collections) || currentStatus?.pending}
              aria-busy={Boolean(currentStatus?.pending || collections.refreshing)}
            >
              <legend className='sr-only'>{t('conversation.libraryWorkbench.collectionMembership')}</legend>
              {visibleRows.map(({ collection, path }) => (
                <label key={collection.id}>
                  <input
                    type='checkbox'
                    checked={memberships.value!.collection_ids.includes(collection.id)}
                    onChange={(event) => void change(collection.id, event.target.checked)}
                  />
                  <span>{path}</span>
                </label>
              ))}
            </fieldset>
          ) : (
            <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noMatchingCollections')}</p>
          )}
        </>
      )}
      {currentStatus?.failed && <p role='alert'>{t('conversation.libraryWorkbench.membershipFailed')}</p>}
    </section>
  );
}
