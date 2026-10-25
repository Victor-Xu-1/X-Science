import { pdfReadingSourceKey, type PdfReadingContext, type PdfReadingSource } from '@/common/adapter/pdfReadingContext';
import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';
import { loadProjectReadingOptions, type PdfReadingOption } from '@/renderer/services/runtime/pdfReadingContext';
import { loadLibraryReadingOptions } from '@/renderer/services/runtime/literatureLibrary';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { LibraryItemEditor } from './LibraryItemEditor';
import { Trigger } from '@arco-design/web-react';
import { BookPlus, FileText, Loader2, MoreHorizontal, Search } from 'lucide-react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import './readingContext.css';

export function ReadingContextPicker({
  context,
  pending,
  onSelect,
  children,
}: {
  context: PdfReadingContext;
  pending: boolean;
  onSelect: (source: PdfReadingSource) => Promise<void>;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [source, setSource] = useState<'project' | 'library'>('project');
  const [editing, setEditing] = useState<LiteratureItem | null>(null);
  const mutation = useLiteratureMutation();
  const [lastAction, setLastAction] = useState<string | null>(null);
  const submitLibrary = (command: LiteratureCommand) => {
    setLastAction(command.operation_id);
    return mutation.controller.submit(command);
  };
  const [retry, setRetry] = useState(0);
  const [result, setResult] = useState<{
    status: 'loading' | 'ready' | 'error';
    options: Array<PdfReadingOption & { item?: LiteratureItem }>;
    unavailable: number;
    key?: string;
    nextCursor?: string;
  }>({
    status: 'loading',
    options: [],
    unavailable: 0,
  });
  const [selected, setSelected] = useState<string | null>(null);
  const selectionOwner = useRef<object | null>(null);
  const [selectError, setSelectError] = useState(false);
  const scope = JSON.stringify(context.scope);
  const account = getRendererAccountScopeToken();
  const loadKey = JSON.stringify([account, scope, source, source === 'library' ? query.trim() : '', open, retry]);
  const authority = useRef({ key: loadKey });
  // Fence synchronously during render, before a previous effect can publish.
  if (authority.current.key !== loadKey) authority.current = { key: loadKey };
  const atLimit = context.bindings.length >= 3;
  useEffect(() => {
    selectionOwner.current = null;
    setOpen(false);
    setQuery('');
    setSelected(null);
    setSelectError(false);
    setEditing(null);
    setLastAction(null);
  }, [scope, account]);
  useEffect(() => {
    const receipt = mutation.view.receipt;
    if (!receipt) return;
    setRetry((value) => value + 1);
    if (receipt.schema !== 'x-science.literature-receipt.v1') return;
    setEditing((previous) =>
      previous?.id === receipt.item.id ? (receipt.item.deleted ? null : receipt.item) : previous
    );
  }, [mutation.view.receipt]);
  useEffect(() => {
    if (!open || atLimit) return;
    const abort = new AbortController();
    const token = authority.current;
    setResult({ status: 'loading', options: [], unavailable: 0, key: loadKey });
    const timer = setTimeout(
      () => {
        const load =
          source === 'library'
            ? loadLibraryReadingOptions(context, { query, signal: abort.signal })
            : Promise.all([
                loadProjectReadingOptions(context, abort.signal),
                loadLibraryReadingOptions(context, { projectId: context.scope.project_id, signal: abort.signal }),
              ]).then(([project, library]) => ({
                options: [...project.options, ...library.options],
                unavailable: project.unavailable + library.unavailable,
                nextCursor: library.nextCursor,
              }));
        void load.then(
          (loaded) => {
            if (!abort.signal.aborted && authority.current === token)
              setResult({ status: 'ready', ...loaded, key: loadKey });
          },
          () => {
            if (!abort.signal.aborted && authority.current === token)
              setResult({ status: 'error', options: [], unavailable: 0, key: loadKey });
          }
        );
      },
      source === 'library' ? 120 : 0
    );
    return () => {
      abort.abort();
      clearTimeout(timer);
    };
  }, [open, context, retry, atLimit, loadKey]);
  const items = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    if (result.key !== loadKey) return [];
    return result.options.filter(
      (item) =>
        !context.bindings.some((binding) => pdfReadingSourceKey(binding) === pdfReadingSourceKey(item.source)) &&
        (source === 'library' || !needle || item.name.toLocaleLowerCase().includes(needle))
    );
  }, [context.bindings, query, result.options, result.key, loadKey, source]);
  const choose = async (item: PdfReadingOption) => {
    if (selected || pending || result.status !== 'ready' || result.key !== authority.current.key) return;
    const token = authority.current;
    const operation = {};
    selectionOwner.current = operation;
    setSelected(pdfReadingSourceKey(item.source));
    setSelectError(false);
    try {
      await onSelect(item.source);
      if (authority.current === token) {
        setOpen(false);
        setQuery('');
      }
    } catch {
      if (authority.current === token) setSelectError(true);
    } finally {
      // Closing invalidates visible read results, not this link's settlement.
      // An old owner must still not clear a newer scope's active selection.
      if (selectionOwner.current === operation) {
        selectionOwner.current = null;
        setSelected(null);
      }
    }
  };
  const reload = () => {
    setSelectError(false);
    setRetry((value) => value + 1);
  };
  const [loadingMore, setLoadingMore] = useState(false);
  const moreAbort = useRef<AbortController | null>(null);
  useEffect(() => {
    moreAbort.current?.abort();
    setLoadingMore(false);
    return () => moreAbort.current?.abort();
  }, [loadKey]);
  const more = async () => {
    if (!result.nextCursor || loadingMore || result.key !== authority.current.key) return;
    const token = authority.current;
    const abort = new AbortController();
    moreAbort.current = abort;
    setLoadingMore(true);
    try {
      const page = await loadLibraryReadingOptions(context, {
        query: source === 'library' ? query : undefined,
        projectId: source === 'project' ? context.scope.project_id : undefined,
        cursor: result.nextCursor,
        signal: abort.signal,
      });
      if (authority.current === token)
        setResult((previous) =>
          previous.key !== token.key
            ? previous
            : {
                ...previous,
                options: [
                  ...new Map(
                    [...previous.options, ...page.options].map((item) => [pdfReadingSourceKey(item.source), item])
                  ).values(),
                ],
                unavailable: previous.unavailable + page.unavailable,
                nextCursor: page.nextCursor,
              }
        );
    } catch {
      if (authority.current === token) setSelectError(true);
    } finally {
      if (moreAbort.current === abort) moreAbort.current = null;
      if (authority.current === token) setLoadingMore(false);
    }
  };
  const changingLibrary = mutation.view.mutating || Boolean(mutation.view.pending) || mutation.view.error === 'storage';
  return (
    <Trigger
      trigger='click'
      position='tl'
      popupVisible={open}
      onVisibleChange={setOpen}
      popupAlign={{ bottom: 8 }}
      unmountOnExit
      popup={() => (
        <section
          className='pdf-reading-picker'
          aria-label={t('conversation.reading.choose')}
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              event.stopPropagation();
              setOpen(false);
            }
          }}
        >
          {!atLimit && (
            <div className='pdf-reading-picker__tabs'>
              {(['project', 'library'] as const).map((candidate) => (
                <button
                  type='button'
                  key={candidate}
                  aria-pressed={source === candidate}
                  disabled={Boolean(selected)}
                  onClick={() => {
                    setSource(candidate);
                    setEditing(null);
                    setLoadingMore(false);
                  }}
                >
                  {t(`conversation.reading.${candidate}`)}
                </button>
              ))}
            </div>
          )}
          {!atLimit && (
            <label className='pdf-reading-picker__search'>
              <Search size={14} aria-hidden='true' />
              <input
                autoFocus
                value={query}
                maxLength={512}
                disabled={Boolean(selected)}
                onChange={(event) => setQuery(event.target.value)}
                aria-label={t('conversation.reading.search')}
                placeholder={t('conversation.reading.search')}
              />
            </label>
          )}
          {mutation.view.error && (
            <div role='alert' className='pdf-reading-notice'>
              <span>
                {t(
                  `conversation.reading.library${mutation.view.error === 'uncertain' ? 'Uncertain' : mutation.view.error === 'storage' ? 'Storage' : 'Rejected'}`
                )}
              </span>
              <button
                type='button'
                disabled={mutation.view.mutating}
                onClick={() => {
                  if (mutation.view.pending) void mutation.controller.retry().catch((): undefined => undefined);
                  else mutation.controller.restore();
                  reload();
                }}
              >
                {t('common.retry')}
              </button>
            </div>
          )}
          {lastAction &&
            mutation.view.receipt?.schema === 'x-science.literature-receipt.v1' &&
            mutation.view.receipt.operation_id === lastAction && (
              <div role='status' className='pdf-reading-notice'>
                {t('conversation.reading.libraryUpdated', { name: mutation.view.receipt.item.title })}
              </div>
            )}
          {editing && (
            <LibraryItemEditor
              key={`${editing.id}:${editing.metadata_revision}`}
              item={editing}
              projectId={context.scope.project_id}
              busy={changingLibrary}
              submit={submitLibrary}
              close={() => setEditing(null)}
            />
          )}
          {(selectError || result.unavailable > 0) && (
            <div role='alert' className='pdf-reading-notice'>
              {t(selectError ? 'conversation.reading.mutationFailed' : 'conversation.reading.someUnavailable')}
              {!selectError && (
                <button type='button' onClick={reload}>
                  {t('common.retry')}
                </button>
              )}
            </div>
          )}
          {atLimit ? (
            <p>{t('conversation.reading.limit')}</p>
          ) : result.key !== loadKey || result.status === 'loading' ? (
            <p role='status'>
              <Loader2 size={16} className='pdf-reading-spin' />
              {t('conversation.reading.checking')}
            </p>
          ) : result.status === 'error' ? (
            <div role='alert' className='pdf-reading-notice'>
              {t('conversation.reading.loadFailed')}
              <button type='button' onClick={reload}>
                {t('common.retry')}
              </button>
            </div>
          ) : items.length === 0 ? (
            <p>{t('conversation.reading.empty')}</p>
          ) : (
            <ul role='listbox' aria-label={t('conversation.reading.choose')}>
              {items.map((item) => (
                <li key={pdfReadingSourceKey(item.source)} className='pdf-library-option'>
                  <button
                    type='button'
                    role='option'
                    aria-selected={false}
                    disabled={pending || selected !== null}
                    onClick={() => void choose(item)}
                  >
                    {selected === pdfReadingSourceKey(item.source) ? (
                      <Loader2 size={16} className='pdf-reading-spin' />
                    ) : (
                      <FileText size={16} />
                    )}
                    <span title={item.name}>{item.name}</span>
                  </button>
                  {item.item ? (
                    <button
                      type='button'
                      className='pdf-library-option__action'
                      disabled={changingLibrary}
                      aria-label={t('conversation.reading.libraryEditNamed', { name: item.name })}
                      onClick={() => setEditing(item.item!)}
                    >
                      <MoreHorizontal size={15} />
                    </button>
                  ) : (
                    item.source.source_kind !== 'literature-attachment-version' && (
                      <button
                        type='button'
                        className='pdf-library-option__action'
                        disabled={changingLibrary}
                        aria-label={t('conversation.reading.libraryImportNamed', { name: item.name })}
                        onClick={() =>
                          void submitLibrary({
                            kind: 'import',
                            operation_id: crypto.randomUUID(),
                            artifact_id: item.source.artifact_id!,
                            version_id: item.source.version_id,
                            title: item.name,
                            project_id: context.scope.project_id,
                          }).catch((): undefined => undefined)
                        }
                      >
                        <BookPlus size={15} />
                      </button>
                    )
                  )}
                </li>
              ))}
            </ul>
          )}
          {result.key === loadKey && result.nextCursor && (
            <button type='button' className='pdf-library-more' disabled={loadingMore} onClick={() => void more()}>
              {t('conversation.reading.libraryMore')}
            </button>
          )}
        </section>
      )}
    >
      {children}
    </Trigger>
  );
}
