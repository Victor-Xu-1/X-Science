import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { loadLiteratureItem, loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import { LiteratureDetailUnavailableError } from '@/common/adapter/literatureLibrary';
import {
  loadLiteratureCollections,
  loadLiteratureItemCollections,
} from '@/renderer/services/runtime/literatureCollections';
import { loadXScienceProjects } from '@/renderer/services/xScienceGateway';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { isBackendHttpError } from '@/common/adapter/httpBridge';

export type LibraryReadView<T> = {
  key: string;
  status: 'loading' | 'ready' | 'error';
  value?: T;
  refreshing?: boolean;
  refreshError?: boolean;
};
type ReadState<T> = LibraryReadView<T> & { retainKey?: string };

/** A view-owned read; the catalogue and mutation ledger remain the existing authorities. */
function useLibraryRead<T>(
  account: string,
  key: string,
  request: (signal: AbortSignal) => Promise<T>,
  delay = 0,
  retainKey?: string
): LibraryReadView<T> {
  const [view, setView] = useState<ReadState<T>>({ key, status: 'loading' });
  const owner = useRef({ key });
  if (owner.current.key !== key) owner.current = { key };
  useEffect(() => {
    const token = owner.current;
    const abort = new AbortController();
    let deadline: ReturnType<typeof setTimeout> | undefined;
    const retained = (previous: ReadState<T>) =>
      retainKey !== undefined &&
      previous.retainKey === retainKey &&
      previous.status === 'ready' &&
      previous.value !== undefined;
    const failure = (error?: unknown) => {
      if (owner.current !== token || account !== getRendererAccountScopeToken()) return;
      const unavailable =
        error instanceof LiteratureDetailUnavailableError ||
        (isBackendHttpError(error) && [400, 401, 403, 404].includes(error.status));
      setView((previous) =>
        !unavailable && retained(previous)
          ? { ...previous, key, refreshing: false, refreshError: true }
          : { key, status: 'error', ...(retainKey === undefined ? {} : { retainKey }) }
      );
    };
    setView((previous) =>
      retained(previous)
        ? { ...previous, key, refreshing: true, refreshError: false }
        : { key, status: 'loading', ...(retainKey === undefined ? {} : { retainKey }) }
    );
    const timer = setTimeout(() => {
      deadline = setTimeout(() => {
        abort.abort('library_read_timeout');
        failure();
      }, 15_000);
      void Promise.resolve()
        .then(() => request(abort.signal))
        .then(
          (value) => {
            if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
              setView({ key, status: 'ready', value, ...(retainKey === undefined ? {} : { retainKey }) });
          },
          (error: unknown) => {
            if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
              failure(error);
          }
        )
        .finally(() => clearTimeout(deadline));
    }, delay);
    return () => {
      abort.abort();
      clearTimeout(timer);
      clearTimeout(deadline);
    };
  }, [account, key, request, delay, retainKey]);
  if (account !== getRendererAccountScopeToken()) return { key, status: 'loading' };
  const publicView = { ...view };
  delete publicView.retainKey;
  if (view.key === key) return publicView;
  return retainKey !== undefined && view.retainKey === retainKey && view.status === 'ready' && view.value !== undefined
    ? { ...publicView, key, refreshing: true, refreshError: false }
    : { key, status: 'loading' };
}

export function useLibraryWorkbench(
  query: string,
  cursor: string,
  selectedId: string,
  collectionId = '',
  projectId = '',
  readingStatus: LiteratureReadingStatus | '' = ''
) {
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const [revision, setRevision] = useState(0);
  const [dataRevision, setDataRevision] = useState(0);
  const key = useId();
  useEffect(
    () => registerRendererAccountReset(`library-workbench:${key}`, () => setAccount(getRendererAccountScopeToken())),
    [key]
  );
  const listRequest = useCallback(
    (signal: AbortSignal) =>
      loadLiteraturePage({
        query,
        cursor: cursor || undefined,
        collectionId: collectionId || undefined,
        projectId: projectId || undefined,
        readingStatus: readingStatus || undefined,
        signal,
      }),
    [query, cursor, collectionId, projectId, readingStatus]
  );
  const detailRequest = useCallback(
    (signal: AbortSignal) => (selectedId ? loadLiteratureItem(selectedId, signal) : Promise.resolve(undefined)),
    [selectedId]
  );
  const collectionRequest = useCallback((signal: AbortSignal) => loadLiteratureCollections(signal), []);
  const membershipRequest = useCallback(
    (signal: AbortSignal) =>
      selectedId ? loadLiteratureItemCollections(selectedId, signal) : Promise.resolve(undefined),
    [selectedId]
  );
  const projectRequest = useCallback((signal: AbortSignal) => loadXScienceProjects({ signal }), []);
  const list = useLibraryRead(
    account,
    JSON.stringify([account, 'list', query, cursor, collectionId, projectId, readingStatus, revision, dataRevision]),
    listRequest,
    query ? 200 : 0
  );
  const detail = useLibraryRead(
    account,
    JSON.stringify([account, 'item', selectedId, revision, dataRevision]),
    detailRequest,
    0,
    JSON.stringify([account, 'item', selectedId, revision])
  );
  const projects = useLibraryRead(account, JSON.stringify([account, 'projects', revision]), projectRequest);
  const collections = useLibraryRead(account, JSON.stringify([account, 'collections', revision]), collectionRequest);
  const memberships = useLibraryRead(
    account,
    JSON.stringify([account, 'memberships', selectedId, revision]),
    membershipRequest
  );
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const refreshData = useCallback(() => setDataRevision((value) => value + 1), []);
  return { account, list, detail, projects, collections, memberships, refresh, refreshData };
}
