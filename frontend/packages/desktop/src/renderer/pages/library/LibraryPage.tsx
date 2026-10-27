import React, { useCallback, useEffect, useRef, useState } from 'react';
import { BookOpen, ChevronRight, Loader2, RefreshCw, Search } from 'lucide-react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import type { LiteratureCollectionCommand } from '@/common/adapter/literatureCollections';
import { LibraryCollectionSidebar } from './LibraryCollectionSidebar';
import { LibraryCollectionEditor, type LibraryCollectionEditorMode } from './LibraryCollectionEditor';
import type { LiteratureCommand, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { LibraryItemDetails, libraryAuthors } from './LibraryItemDetails';
import { useLibraryWorkbench } from './useLibraryWorkbench';
import { LibraryProjectScope } from './LibraryProjectScope';
import { LibraryPdfImport } from './LibraryPdfImport';
import { LibraryPdfImportPanel } from './LibraryPdfImportPanel';
import { useLibraryPdfImport } from './useLibraryPdfImport';
import './libraryWorkbench.css';

type CollectionEditorState = { owner: object; id: number; mode: LibraryCollectionEditorMode };

export default function LibraryPage() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const query = params.get('q') ?? '';
  const cursor = params.get('cursor') ?? '';
  const selectedId = params.get('item') ?? '';
  const collectionId = params.get('collection') ?? '';
  const projectId = params.get('project') ?? '';
  const reads = useLibraryWorkbench(query, cursor, selectedId, collectionId, projectId);
  const mutation = useLiteratureMutation();
  const [action, setAction] = useState<{ account: string; failed: boolean; operationId: string | null }>({
    account: reads.account,
    failed: false,
    operationId: null,
  });
  const actionFailed = action.account === reads.account && action.failed;
  const lastAction = action.account === reads.account ? action.operationId : null;
  const formScope = JSON.stringify([reads.account, collectionId, projectId, query, cursor, selectedId]);
  const editorBoundary = useRef({ key: formScope });
  if (editorBoundary.current.key !== formScope) editorBoundary.current = { key: formScope };
  const editorSequence = useRef(0);
  const [editor, setEditor] = useState<CollectionEditorState>();
  const visibleEditor = editor?.owner === editorBoundary.current ? editor : undefined;
  const [pdfEditor, setPdfEditor] = useState({ account: reads.account, open: false });
  const pdfOpen = pdfEditor.account === reads.account && pdfEditor.open;
  const setPdfOpen = useCallback(
    (open: boolean) => {
      setPdfEditor((previous) =>
        reads.account === getRendererAccountScopeToken() ? { account: reads.account, open } : previous
      );
    },
    [reads.account]
  );
  const pdfImport = useLibraryPdfImport({
    account: reads.account,
    controller: mutation.controller,
    view: mutation.view,
    projects: reads.projects,
    open: pdfOpen,
    setOpen: setPdfOpen,
    blockedByEditor: Boolean(visibleEditor),
  });
  const openCollection = (mode: LibraryCollectionEditorMode) => {
    if (reads.account === getRendererAccountScopeToken())
      setEditor({ owner: editorBoundary.current, id: ++editorSequence.current, mode });
  };
  const selectCollection = (id: string) => {
    const next = new URLSearchParams(params);
    next.delete('project');
    if (id) next.set('collection', id);
    else next.delete('collection');
    next.delete('cursor');
    next.delete('item');
    setParams(next);
  };
  const selectProject = (id: string) => {
    const next = new URLSearchParams(params);
    if (id) next.set('project', id);
    else next.delete('project');
    next.delete('collection');
    next.delete('cursor');
    next.delete('item');
    setParams(next);
  };
  const observedReceipt = useRef(mutation.view.receipt);
  const rowButtons = useRef(new Map<string, HTMLButtonElement>());
  const selectItem = (id: string) => {
    const next = new URLSearchParams(params);
    if (id) next.set('item', id);
    else next.delete('item');
    setParams(next);
  };
  const viewImportedReference = (receipt: LiteratureReceipt) => {
    if (reads.account !== getRendererAccountScopeToken()) return;
    const next = new URLSearchParams(params);
    next.delete('q');
    next.delete('collection');
    next.delete('project');
    next.delete('cursor');
    next.set('item', receipt.item.id);
    setParams(next);
  };
  const closeDetails = () => {
    rowButtons.current.get(selectedId)?.focus();
    selectItem('');
  };
  const changePage = (nextCursor?: string) => {
    const next = new URLSearchParams(params);
    if (nextCursor) next.set('cursor', nextCursor);
    else next.delete('cursor');
    next.delete('item');
    setParams(next);
  };
  useEffect(() => {
    const receipt = mutation.view.receipt;
    if (!receipt || observedReceipt.current === receipt || reads.account !== getRendererAccountScopeToken()) return;
    observedReceipt.current = receipt;
    reads.refresh();
    if (
      receipt.schema === 'x-science.literature-receipt.v1' &&
      (receipt.item.deleted || (projectId && !receipt.item.project_ids.includes(projectId))) &&
      receipt.item.id === selectedId
    ) {
      rowButtons.current.get(selectedId)?.focus();
      const next = new URLSearchParams(params);
      next.delete('item');
      if (projectId) next.delete('cursor');
      setParams(next, { replace: true });
    } else if (
      receipt.schema === 'x-science.literature-collection-receipt.v1' &&
      receipt.action === 'delete' &&
      receipt.collection_id === collectionId
    ) {
      const next = new URLSearchParams(params);
      next.delete('collection');
      next.delete('cursor');
      next.delete('item');
      setParams(next, { replace: true });
    }
  }, [mutation.view.receipt, reads.refresh, reads.account, selectedId, collectionId, projectId, params, setParams]);
  const submit = useCallback(
    async <Command extends LiteratureCommand | LiteratureCollectionCommand>(command: Command) => {
      const account = reads.account;
      setAction({ account, failed: false, operationId: command.operation_id });
      try {
        return await mutation.controller.submit(command);
      } catch (error) {
        if (account === getRendererAccountScopeToken())
          setAction((value) => (value.account === account ? { ...value, failed: true } : value));
        throw error;
      }
    },
    [mutation.controller, reads.account]
  );
  const retryMutation = async () => {
    if (mutation.view.pending?.kind === 'pdf-upload') {
      pdfImport.retry();
      return;
    }
    const account = reads.account;
    setAction({ account, failed: false, operationId: mutation.view.pending?.operation_id ?? null });
    try {
      if (mutation.view.pending) {
        await mutation.controller.retry();
      } else {
        mutation.controller.restore();
        reads.refresh();
      }
    } catch {
      if (account === getRendererAccountScopeToken())
        setAction((value) => (value.account === account ? { ...value, failed: true } : value));
    }
  };
  const busy = mutation.view.mutating || Boolean(mutation.view.pending) || mutation.view.error === 'storage' || pdfOpen;
  const items = reads.list.value?.items ?? [];
  const selected = reads.detail.status === 'ready' ? reads.detail.value : undefined;
  const selectedProject = reads.projects.value?.find((value) => value.projectId === projectId);
  const selectedCollection =
    reads.collections.status === 'ready'
      ? reads.collections.value?.collections.find((value) => value.id === collectionId)
      : undefined;
  const acknowledged =
    lastAction && mutation.view.receipt?.operation_id === lastAction ? mutation.view.receipt : undefined;
  return (
    <main className='library-workbench' aria-labelledby='library-workbench-title'>
      <header className='library-workbench__header library-pdf-import__page-header'>
        <div>
          <p className='library-workbench__eyebrow'>{t('conversation.libraryWorkbench.eyebrow')}</p>
          <h1 id='library-workbench-title'>
            <BookOpen size={23} aria-hidden='true' />
            {t('conversation.libraryWorkbench.title')}
          </h1>
          <p>{t('conversation.libraryWorkbench.description')}</p>
        </div>
        <div className='library-pdf-import__header-actions'>
          <LibraryPdfImport model={pdfImport} />
          <button type='button' className='library-workbench__button' onClick={reads.refresh}>
            <RefreshCw size={15} aria-hidden='true' />
            {t('common.refresh')}
          </button>
        </div>
      </header>
      <LibraryPdfImportPanel model={pdfImport} refreshProjects={reads.refresh} viewReference={viewImportedReference} />
      <div className='library-workbench__toolbar'>
        <LibraryProjectScope projectId={projectId} projects={reads.projects} select={selectProject} />
        <label className='library-workbench__search'>
          <Search size={16} aria-hidden='true' />
          <input
            type='search'
            value={query}
            maxLength={512}
            onChange={(event) => {
              const next = new URLSearchParams(params);
              if (event.target.value) next.set('q', event.target.value);
              else next.delete('q');
              next.delete('cursor');
              next.delete('item');
              setParams(next, { replace: true });
            }}
            aria-label={t('conversation.libraryWorkbench.search')}
            placeholder={t('conversation.libraryWorkbench.search')}
          />
        </label>
      </div>
      {!pdfImport.noticeOwned && (mutation.view.error || actionFailed) && (
        <div role='alert' className='library-workbench__notice'>
          <span>
            {t(
              mutation.view.error === 'uncertain'
                ? 'conversation.reading.libraryUncertain'
                : mutation.view.error === 'storage'
                  ? 'conversation.reading.libraryStorage'
                  : 'conversation.reading.libraryRejected'
            )}
          </span>
          <button type='button' disabled={mutation.view.mutating} onClick={() => void retryMutation()}>
            {t('common.retry')}
          </button>
        </div>
      )}
      {acknowledged && (
        <p role='status' className='library-workbench__notice'>
          {acknowledged.schema === 'x-science.literature-receipt.v1'
            ? t('conversation.reading.libraryUpdated', { name: acknowledged.item.title })
            : acknowledged.action === 'membership'
              ? t('conversation.libraryWorkbench.membershipUpdated')
              : acknowledged.action === 'delete'
                ? t('conversation.libraryWorkbench.collectionDeleted')
                : t('conversation.libraryWorkbench.collectionUpdated', { name: acknowledged.collection.name })}
        </p>
      )}
      <div className='library-workbench__workspace'>
        <LibraryCollectionSidebar
          view={reads.collections}
          selectedId={collectionId}
          allSelected={!collectionId && !projectId}
          busy={busy}
          select={selectCollection}
          create={() => openCollection({ kind: 'create' })}
          retry={reads.refresh}
        />
        <section
          className='library-workbench__collection-content'
          aria-label={t('conversation.libraryWorkbench.collectionResults')}
        >
          <div className='library-workbench__collection-heading'>
            <div>
              <h2>
                {selectedCollection?.name ??
                  (projectId
                    ? (selectedProject?.name ??
                      t(
                        reads.projects.status === 'loading'
                          ? 'common.loading'
                          : 'conversation.libraryWorkbench.projectUnavailable'
                      ))
                    : t(
                        collectionId
                          ? 'conversation.libraryWorkbench.collection'
                          : 'conversation.libraryWorkbench.allReferences'
                      ))}
              </h2>
              {selectedCollection?.description && <p>{selectedCollection.description}</p>}
            </div>
            {selectedCollection && (
              <div className='library-workbench__attachment-actions'>
                <button
                  type='button'
                  disabled={busy}
                  onClick={() => openCollection({ kind: 'update', collection: selectedCollection })}
                >
                  {t('conversation.libraryWorkbench.editCollection')}
                </button>
                <button
                  type='button'
                  disabled={busy}
                  onClick={() => openCollection({ kind: 'delete', collection: selectedCollection })}
                >
                  {t('conversation.libraryWorkbench.deleteCollection')}
                </button>
              </div>
            )}
          </div>
          {collectionId && reads.collections.status === 'ready' && !selectedCollection && (
            <p role='alert'>{t('conversation.libraryWorkbench.collectionUnavailable')}</p>
          )}
          <div className='library-workbench__panes'>
            <section className='library-workbench__list' aria-label={t('conversation.libraryWorkbench.items')}>
              {reads.list.status === 'loading' ? (
                <div role='status' className='library-workbench__state'>
                  <Loader2 className='library-workbench__spin' size={20} aria-hidden='true' />
                  {t('common.loading')}
                </div>
              ) : reads.list.status === 'error' ? (
                <div role='alert' className='library-workbench__state'>
                  <h2>{t('conversation.libraryWorkbench.loadFailed')}</h2>
                  <button type='button' onClick={reads.refresh}>
                    {t('common.retry')}
                  </button>
                </div>
              ) : items.length === 0 ? (
                <div className='library-workbench__state'>
                  <BookOpen size={27} aria-hidden='true' />
                  <h2>
                    {t(
                      query
                        ? 'conversation.libraryWorkbench.noMatches'
                        : projectId
                          ? 'conversation.libraryWorkbench.projectEmpty'
                          : 'conversation.libraryWorkbench.empty'
                    )}
                  </h2>
                  <p>
                    {t(
                      query
                        ? 'conversation.libraryWorkbench.searchHint'
                        : projectId
                          ? 'conversation.libraryWorkbench.projectEmptyHint'
                          : 'conversation.libraryPdfImport.emptyHint'
                    )}
                  </p>
                </div>
              ) : (
                <ul aria-label={t('conversation.libraryWorkbench.items')}>
                  {items.map((item) => (
                    <li key={item.id}>
                      <button
                        ref={(node) => {
                          if (node) rowButtons.current.set(item.id, node);
                          else rowButtons.current.delete(item.id);
                        }}
                        type='button'
                        className='library-workbench__item'
                        aria-pressed={selectedId === item.id}
                        onClick={() => selectItem(item.id)}
                      >
                        <span className='library-workbench__book'>
                          <BookOpen size={17} aria-hidden='true' />
                        </span>
                        <span className='library-workbench__item-copy'>
                          <strong>{item.title}</strong>
                          <span>{libraryAuthors(item) || t('conversation.libraryWorkbench.noAuthors')}</span>
                        </span>
                        <ChevronRight size={15} aria-hidden='true' />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              <footer className='library-workbench__paging'>
                <button
                  type='button'
                  disabled={!cursor || reads.list.status === 'loading'}
                  onClick={() => changePage()}
                >
                  {t('conversation.libraryWorkbench.firstPage')}
                </button>
                <button
                  type='button'
                  disabled={reads.list.status !== 'ready' || !reads.list.value?.next_cursor}
                  onClick={() => changePage(reads.list.value?.next_cursor)}
                >
                  {t('conversation.libraryWorkbench.nextPage')}
                  <ChevronRight size={14} aria-hidden='true' />
                </button>
              </footer>
            </section>
            {selectedId ? (
              <aside aria-label={t('conversation.libraryWorkbench.details')}>
                {reads.detail.status === 'loading' ? (
                  <p role='status' className='library-workbench__state'>
                    {t('common.loading')}
                  </p>
                ) : reads.detail.status === 'error' || !selected ? (
                  <div role='alert' className='library-workbench__state'>
                    <h2>{t('conversation.libraryWorkbench.detailFailed')}</h2>
                    <button type='button' onClick={reads.refresh}>
                      {t('common.retry')}
                    </button>
                    <button type='button' onClick={closeDetails}>
                      {t('common.close')}
                    </button>
                  </div>
                ) : (
                  <LibraryItemDetails
                    key={`${reads.account}:${selected.id}`}
                    item={selected}
                    account={reads.account}
                    collections={reads.collections}
                    memberships={reads.memberships}
                    submitCollection={submit}
                    refreshCollections={reads.refresh}
                    projects={reads.projects.value ?? []}
                    projectStatus={reads.projects.status}
                    busy={busy}
                    submit={submit}
                    close={closeDetails}
                  />
                )}
              </aside>
            ) : (
              <aside className='library-workbench__selection-hint'>
                <BookOpen size={25} aria-hidden='true' />
                <p>{t('conversation.libraryWorkbench.selectHint')}</p>
              </aside>
            )}
          </div>
          {reads.projects.status === 'error' && (
            <div role='alert' className='library-workbench__notice'>
              <span>{t('conversation.libraryWorkbench.projectsFailed')}</span>
              <button type='button' onClick={reads.refresh}>
                {t('common.retry')}
              </button>
            </div>
          )}
        </section>
      </div>
      {visibleEditor && (
        <LibraryCollectionEditor
          key={`${reads.account}:${visibleEditor.id}`}
          mode={visibleEditor.mode}
          account={reads.account}
          collections={reads.collections}
          busy={busy}
          mutating={mutation.view.mutating}
          pendingOperationId={mutation.view.pending?.operation_id}
          receipt={mutation.view.receipt}
          submit={submit}
          retry={retryMutation}
          refresh={reads.refresh}
          close={() => setEditor(undefined)}
          editChild={(collection) => openCollection({ kind: 'update', collection })}
          committed={(receipt) => {
            if (editorBoundary.current !== visibleEditor.owner) return;
            setEditor(undefined);
            if (receipt.action === 'create') selectCollection(receipt.collection_id);
          }}
        />
      )}
    </main>
  );
}
