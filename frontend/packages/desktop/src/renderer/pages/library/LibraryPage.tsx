import React, { useCallback, useEffect, useRef, useState } from 'react';
import { BookOpen, ChevronRight, Loader2, RefreshCw, Search } from 'lucide-react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand } from '@/common/adapter/literatureLibrary';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { LibraryItemDetails, libraryAuthors } from './LibraryItemDetails';
import { useLibraryWorkbench } from './useLibraryWorkbench';
import './libraryWorkbench.css';

export default function LibraryPage() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const query = params.get('q') ?? '';
  const cursor = params.get('cursor') ?? '';
  const selectedId = params.get('item') ?? '';
  const reads = useLibraryWorkbench(query, cursor, selectedId);
  const mutation = useLiteratureMutation();
  const [action, setAction] = useState<{ account: string; failed: boolean; operationId: string | null }>({
    account: reads.account,
    failed: false,
    operationId: null,
  });
  const actionFailed = action.account === reads.account && action.failed;
  const lastAction = action.account === reads.account ? action.operationId : null;
  const observedReceipt = useRef(mutation.view.receipt);
  const rowButtons = useRef(new Map<string, HTMLButtonElement>());
  const selectItem = (id: string) => {
    const next = new URLSearchParams(params);
    if (id) next.set('item', id);
    else next.delete('item');
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
    if (!receipt || observedReceipt.current === receipt) return;
    observedReceipt.current = receipt;
    reads.refresh();
    if (receipt.item.deleted && receipt.item.id === selectedId) {
      rowButtons.current.get(selectedId)?.focus();
      const next = new URLSearchParams(params);
      next.delete('item');
      setParams(next, { replace: true });
    }
  }, [mutation.view.receipt, reads.refresh, selectedId, params, setParams]);
  const submit = useCallback(
    async (command: LiteratureCommand) => {
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
  const busy = mutation.view.mutating || Boolean(mutation.view.pending) || mutation.view.error === 'storage';
  const items = reads.list.value?.items ?? [];
  const selected = reads.detail.status === 'ready' ? reads.detail.value : undefined;
  const acknowledged =
    lastAction && mutation.view.receipt?.operation_id === lastAction ? mutation.view.receipt : undefined;
  return (
    <main className='library-workbench' aria-labelledby='library-workbench-title'>
      <header className='library-workbench__header'>
        <div>
          <p className='library-workbench__eyebrow'>{t('conversation.libraryWorkbench.eyebrow')}</p>
          <h1 id='library-workbench-title'>
            <BookOpen size={23} aria-hidden='true' />
            {t('conversation.libraryWorkbench.title')}
          </h1>
          <p>{t('conversation.libraryWorkbench.description')}</p>
        </div>
        <button type='button' className='library-workbench__button' onClick={reads.refresh}>
          <RefreshCw size={15} aria-hidden='true' />
          {t('common.refresh')}
        </button>
      </header>
      <div className='library-workbench__toolbar'>
        <label className='library-workbench__search'>
          <Search size={16} aria-hidden='true' />
          <input
            type='search'
            value={query}
            maxLength={512}
            onChange={(event) => setParams(event.target.value ? { q: event.target.value } : {}, { replace: true })}
            aria-label={t('conversation.libraryWorkbench.search')}
            placeholder={t('conversation.libraryWorkbench.search')}
          />
        </label>
      </div>
      {(mutation.view.error || actionFailed) && (
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
          {t('conversation.reading.libraryUpdated', { name: acknowledged.item.title })}
        </p>
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
              <h2>{t(query ? 'conversation.libraryWorkbench.noMatches' : 'conversation.libraryWorkbench.empty')}</h2>
              <p>{t(query ? 'conversation.libraryWorkbench.searchHint' : 'conversation.libraryWorkbench.emptyHint')}</p>
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
            <button type='button' disabled={!cursor || reads.list.status === 'loading'} onClick={() => changePage()}>
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
    </main>
  );
}
