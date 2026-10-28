import React from 'react';
import { FolderOpen, Plus } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCollection, LiteratureCollectionPage } from '@/common/adapter/literatureCollections';
import type { LibraryReadView } from './useLibraryWorkbench';
import { isLibraryReadFresh } from './useLibraryWorkbench';
import { LibraryDirectoryState } from './LibraryDirectoryState';

/** Display paths follow the decoded owner tree; relation/order authority stays on the server. */
export function libraryCollectionRows(collections: LiteratureCollection[]) {
  const children = new Map<string | null, LiteratureCollection[]>();
  for (const collection of collections) {
    const siblings = children.get(collection.parent_id) ?? [];
    siblings.push(collection);
    children.set(collection.parent_id, siblings);
  }
  // The server orders sort_order/name/id. Grouping keeps that same sibling order,
  // including promoted children; do not recreate SQLite's name collation in JS.
  const ordered = (parent: string | null) => children.get(parent) ?? [];
  const pending = [...ordered(null)].reverse().map((collection) => ({ collection, path: collection.name }));
  const rows: { collection: LiteratureCollection; path: string }[] = [];
  const visited = new Set<string>();
  while (pending.length) {
    const row = pending.pop()!;
    if (visited.has(row.collection.id)) continue;
    visited.add(row.collection.id);
    rows.push(row);
    for (const child of [...ordered(row.collection.id)].reverse())
      pending.push({ collection: child, path: `${row.path} / ${child.name}` });
  }
  return rows;
}

export function LibraryCollectionSidebar({
  view,
  selectedId,
  allSelected = !selectedId,
  busy,
  select,
  create,
  retry,
}: {
  view: LibraryReadView<LiteratureCollectionPage>;
  selectedId: string;
  allSelected?: boolean;
  busy: boolean;
  select: (id: string) => void;
  create: () => void;
  retry: () => void;
}) {
  const { t } = useTranslation();
  return (
    <nav className='library-workbench__collections' aria-label={t('conversation.libraryWorkbench.collections')}>
      <button
        type='button'
        className='library-workbench__collection-row'
        aria-current={allSelected ? 'page' : undefined}
        onClick={() => select('')}
      >
        <FolderOpen size={15} aria-hidden='true' />
        {t('conversation.libraryWorkbench.allReferences')}
      </button>
      <div className='library-workbench__section-heading'>
        <h2>{t('conversation.libraryWorkbench.collections')}</h2>
        <button
          type='button'
          className='library-workbench__icon'
          aria-label={t('conversation.libraryWorkbench.newCollection')}
          disabled={busy || !isLibraryReadFresh(view)}
          onClick={create}
        >
          <Plus size={16} aria-hidden='true' />
        </button>
      </div>
      <LibraryDirectoryState
        view={view}
        errorText={t('conversation.libraryWorkbench.collectionsFailed')}
        retry={retry}
      />
      {view.status !== 'ready' ? null : view.value?.collections.length ? (
        <ul>
          {libraryCollectionRows(view.value.collections).map(({ collection, path }) => (
            <li key={collection.id}>
              <button
                type='button'
                className='library-workbench__collection-row'
                title={path}
                aria-label={path}
                aria-current={selectedId === collection.id ? 'page' : undefined}
                onClick={() => select(collection.id)}
              >
                <FolderOpen size={14} aria-hidden='true' />
                <span>{path}</span>
                <span aria-hidden='true' className='library-workbench__collection-count'>
                  {collection.item_count}
                </span>
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noCollections')}</p>
      )}
    </nav>
  );
}
