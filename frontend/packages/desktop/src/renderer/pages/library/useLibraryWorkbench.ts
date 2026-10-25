import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { loadLiteratureItem, loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import {
  loadLiteratureCollections,
  loadLiteratureItemCollections,
} from '@/renderer/services/runtime/literatureCollections';
import { loadXScienceProjects } from '@/renderer/services/xScienceGateway';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';

export type LibraryReadView<T> = { key: string; status: 'loading' | 'ready' | 'error'; value?: T };

/** A view-owned read; the catalogue and mutation ledger remain the existing authorities. */
function useLibraryRead<T>(
  account: string,
  key: string,
  request: (signal: AbortSignal) => Promise<T>,
  delay = 0
): LibraryReadView<T> {
  const [view, setView] = useState<LibraryReadView<T>>({ key, status: 'loading' });
  const owner = useRef({ key });
  if (owner.current.key !== key) owner.current = { key };
  useEffect(() => {
    const token = owner.current;
    const abort = new AbortController();
    let deadline: ReturnType<typeof setTimeout> | undefined;
    setView({ key, status: 'loading' });
    const timer = setTimeout(() => {
      deadline = setTimeout(() => {
        abort.abort('library_read_timeout');
        if (owner.current === token && account === getRendererAccountScopeToken()) setView({ key, status: 'error' });
      }, 15_000);
      void Promise.resolve()
        .then(() => request(abort.signal))
        .then(
          (value) => {
            if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
              setView({ key, status: 'ready', value });
          },
          () => {
            if (!abort.signal.aborted && owner.current === token && account === getRendererAccountScopeToken())
              setView({ key, status: 'error' });
          }
        )
        .finally(() => clearTimeout(deadline));
    }, delay);
    return () => {
      abort.abort();
      clearTimeout(timer);
      clearTimeout(deadline);
    };
  }, [account, key, request, delay]);
  return view.key === key && account === getRendererAccountScopeToken() ? view : { key, status: 'loading' };
}

export function useLibraryWorkbench(query: string, cursor: string, selectedId: string, collectionId = '') {
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const [revision, setRevision] = useState(0);
  const key = useId();
  useEffect(
    () => registerRendererAccountReset(`library-workbench:${key}`, () => setAccount(getRendererAccountScopeToken())),
    [key]
  );
  const listRequest = useCallback(
    (signal: AbortSignal) =>
      loadLiteraturePage({ query, cursor: cursor || undefined, collectionId: collectionId || undefined, signal }),
    [query, cursor, collectionId]
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
    JSON.stringify([account, 'list', query, cursor, collectionId, revision]),
    listRequest,
    query ? 200 : 0
  );
  const detail = useLibraryRead(account, JSON.stringify([account, 'item', selectedId, revision]), detailRequest);
  const projects = useLibraryRead(account, JSON.stringify([account, 'projects', revision]), projectRequest);
  const collections = useLibraryRead(account, JSON.stringify([account, 'collections', revision]), collectionRequest);
  const memberships = useLibraryRead(
    account,
    JSON.stringify([account, 'memberships', selectedId, revision]),
    membershipRequest
  );
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { account, list, detail, projects, collections, memberships, refresh };
}
