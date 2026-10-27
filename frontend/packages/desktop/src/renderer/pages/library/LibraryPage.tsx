import React, { useCallback, useEffect, useRef, useState } from 'react';
import { BookOpen, ChevronRight, Loader2, RefreshCw } from 'lucide-react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import type { LiteratureCollectionCommand } from '@/common/adapter/literatureCollections';
import { LibraryCollectionSidebar } from './LibraryCollectionSidebar';
import { LibraryCollectionEditor, type LibraryCollectionEditorMode } from './LibraryCollectionEditor';
import type { LiteratureCommand, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import {
  literatureReadingStatuses,
  type LiteratureReadingStatus,
  type LiteratureReadingStatusCommand,
} from '@/common/adapter/literatureReadingStatus';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { libraryAuthors } from './LibraryItemDetails';
import { LibraryDetailsPane } from './LibraryDetailsPane';
import { useLibraryWorkbench } from './useLibraryWorkbench';
import { LibraryProjectScope } from './LibraryProjectScope';
import { LibraryWorkspaceHeader } from './LibraryWorkspaceHeader';
import { LibraryReadingStatusFilter } from './LibraryReadingStatus';
import { LibraryReferenceRow } from './LibraryReferenceRow';
import { LibraryMutationNotice } from './LibraryMutationNotice';
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
  const readingStatus = literatureReadingStatuses.find((value) => value === params.get('reading_status')) ?? '';
  useEffect(() => {
    if (params.has('reading_status') && params.get('reading_status') !== readingStatus) {
      const next = new URLSearchParams(params);
      next.delete('reading_status');
      next.delete('cursor');
      next.delete('item');
      setParams(next, { replace: true });
    }
  }, [params, readingStatus, setParams]);
  const reads = useLibraryWorkbench(query, cursor, selectedId, collectionId, projectId, readingStatus);
  const mutation = useLiteratureMutation();
  const [action, setAction] = useState<{ account: string; failed: boolean; operationId: string | null }>({
    account: reads.account,
    failed: false,
    operationId: null,
  });
  const actionFailed = action.account === reads.account && action.failed;
  const lastAction = action.account === reads.account ? action.operationId : null;
  const formScope = JSON.stringify([reads.account, collectionId, projectId, query, cursor, selectedId, readingStatus]);
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
  const listElement = useRef<HTMLElement>(null);
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
    next.delete('reading_status');
    next.set('item', receipt.item.id);
    setParams(next);
  };
  const closeDetails = () => {
    const trigger = rowButtons.current.get(selectedId);
    if (trigger?.isConnected && !trigger.disabled) trigger.focus();
    else listElement.current?.focus();
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
    // Evidence is independent of metadata and is consumed by the live detail.
    // A catalogue reload would unmount it and erase a real provider failure.
    if (receipt.schema === 'x-science.literature-publication-receipt.v1') return;
    if (receipt.schema === 'x-science.literature-reading-status-receipt.v1') {
      // Keep the same detail/editor/preview mounted. A receipt is historical
      // acknowledgement; only these current reads project the stored state.
      if (listElement.current?.contains(document.activeElement)) listElement.current.focus();
      reads.refreshData();
      return;
    }
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
  }, [
    mutation.view.receipt,
    reads.refresh,
    reads.refreshData,
    reads.account,
    selectedId,
    collectionId,
    projectId,
    params,
    setParams,
  ]);
  const submit = useCallback(
    async <Command extends LiteratureCommand | LiteratureCollectionCommand | LiteratureReadingStatusCommand>(
      command: Command
    ) => {
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
        if (mutation.view.action?.kind === 'set-reading-status') reads.refreshData();
        else reads.refresh();
      }
    } catch {
      if (account === getRendererAccountScopeToken())
        setAction((value) => (value.account === account ? { ...value, failed: true } : value));
    }
  };
  const busy = mutation.view.mutating || Boolean(mutation.view.pending) || mutation.view.error === 'storage' || pdfOpen;
  const changeReadingStatus = (itemId: string, value: LiteratureReadingStatus) => {
    if (busy || reads.account !== getRendererAccountScopeToken()) return;
    void submit({
      kind: 'set-reading-status',
      operation_id: crypto.randomUUID(),
      item_ids: [itemId],
      reading_status: value,
    }).catch((): undefined => undefined);
  };
  const items = reads.list.value?.items ?? [];
  const selected = reads.detail.status === 'ready' ? reads.detail.value : undefined;
  const selectedProject = reads.projects.value?.find((value) => value.projectId === projectId);
  const selectedCollection =
    reads.collections.status === 'ready'
      ? reads.collections.value?.collections.find((value) => value.id === collectionId)
      : undefined;
  const acknowledged =
    lastAction &&
    !mutation.view.mutating &&
    !mutation.view.pending &&
    mutation.view.action?.operation_id === lastAction &&
    mutation.view.receipt?.operation_id === lastAction &&
    mutation.view.receipt.schema !== 'x-science.literature-publication-receipt.v1'
      ? mutation.view.receipt
      : undefined;
  const publicationNoticeOwned = Boolean(
    selected &&
    mutation.view.action?.kind === 'check-publication' &&
    mutation.view.action.item_id === selected.id &&
    mutation.view.action.expected_metadata_revision === selected.metadata_revision
  );
  return (
    <main className='library-workbench' aria-labelledby='library-workbench-title'>
      <LibraryMutationNotice
        hidden={pdfImport.noticeOwned || publicationNoticeOwned}
        error={mutation.view.error}
        failed={actionFailed}
        mutating={mutation.view.mutating}
        acknowledged={acknowledged}
        retry={() => void retryMutation()}
      />
      <div className='library-workbench__workspace'>
        <aside className='library-workbench__navigation' aria-label={t('conversation.libraryWorkbench.title')}>
          <div className='library-workbench__navigation-heading'>
            <BookOpen size={18} aria-hidden='true' />
            <span>{t('conversation.libraryWorkbench.title')}</span>
          </div>
          <LibraryCollectionSidebar
            view={reads.collections}
            selectedId={collectionId}
            allSelected={!collectionId && !projectId}
            busy={busy}
            select={selectCollection}
            create={() => openCollection({ kind: 'create' })}
            retry={reads.refresh}
          />
          <LibraryProjectScope projectId={projectId} projects={reads.projects} select={selectProject} />
        </aside>
        <section
          className='library-workbench__collection-content'
          aria-label={t('conversation.libraryWorkbench.collectionResults')}
        >
          <LibraryWorkspaceHeader
            title={
              selectedCollection?.name ??
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
                  ))
            }
            description={selectedCollection?.description || t('conversation.libraryWorkbench.description')}
            query={query}
            onQueryChange={(value) => {
              const next = new URLSearchParams(params);
              if (value) next.set('q', value);
              else next.delete('q');
              next.delete('cursor');
              next.delete('item');
              setParams(next, { replace: true });
            }}
            actions={
              <>
                <LibraryPdfImport model={pdfImport} />
                <button type='button' className='library-workbench__button' onClick={reads.refresh}>
                  <RefreshCw size={14} aria-hidden='true' />
                  {t('common.refresh')}
                </button>
              </>
            }
            collectionActions={
              selectedCollection && (
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
              )
            }
            filters={
              <LibraryReadingStatusFilter
                value={readingStatus}
                onChange={(value) => {
                  const next = new URLSearchParams(params);
                  if (value) next.set('reading_status', value);
                  else next.delete('reading_status');
                  next.delete('cursor');
                  next.delete('item');
                  setParams(next);
                }}
              />
            }
          />
          <LibraryPdfImportPanel
            model={pdfImport}
            refreshProjects={reads.refresh}
            viewReference={viewImportedReference}
          />
          {collectionId && reads.collections.status === 'ready' && !selectedCollection && (
            <p role='alert'>{t('conversation.libraryWorkbench.collectionUnavailable')}</p>
          )}
          <div className='library-workbench__panes' data-detail-open={Boolean(selectedId)}>
            <section
              ref={listElement}
              tabIndex={-1}
              className='library-workbench__list'
              aria-label={t('conversation.libraryWorkbench.items')}
            >
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
                  <BookOpen size={28} aria-hidden='true' />
                  <h2>
                    {t(
                      query || readingStatus
                        ? 'conversation.libraryWorkbench.noMatches'
                        : projectId
                          ? 'conversation.libraryWorkbench.projectEmpty'
                          : 'conversation.libraryWorkbench.empty'
                    )}
                  </h2>
                  <p>
                    {t(
                      query || readingStatus
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
                    <LibraryReferenceRow
                      key={item.id}
                      item={item}
                      authors={libraryAuthors(item)}
                      selected={selectedId === item.id}
                      busy={busy || reads.list.status !== 'ready'}
                      register={(node) => {
                        if (node) rowButtons.current.set(item.id, node);
                        else rowButtons.current.delete(item.id);
                      }}
                      open={() => selectItem(item.id)}
                      changeStatus={(value) => changeReadingStatus(item.id, value)}
                    />
                  ))}
                </ul>
              )}
              {(cursor || reads.list.value?.next_cursor) && (
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
              )}
            </section>
            {selectedId && (
              <LibraryDetailsPane
                reads={reads}
                busy={busy}
                submit={submit}
                changeStatus={changeReadingStatus}
                receipt={
                  mutation.view.receipt?.schema === 'x-science.literature-receipt.v1'
                    ? mutation.view.receipt
                    : undefined
                }
                close={closeDetails}
              />
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
          receipt={
            mutation.view.receipt?.schema === 'x-science.literature-receipt.v1' ||
            mutation.view.receipt?.schema === 'x-science.literature-collection-receipt.v1'
              ? mutation.view.receipt
              : undefined
          }
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
