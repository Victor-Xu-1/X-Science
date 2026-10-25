import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { loadLiteratureItem, loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import { loadXScienceProjects } from '@/renderer/services/xScienceGateway';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';

type ReadView<T> = { key: string; status: 'loading' | 'ready' | 'error'; value?: T };

/** A view-owned read; the catalogue and mutation ledger remain the existing authorities. */
function useLibraryRead<T>(
  account: string,
  key: string,
  request: (signal: AbortSignal) => Promise<T>,
  delay = 0
): ReadView<T> {
  const [view, setView] = useState<ReadView<T>>({ key, status: 'loading' });
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

export function useLibraryWorkbench(query: string, cursor: string, selectedId: string) {
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const [revision, setRevision] = useState(0);
  const key = useId();
  useEffect(
    () => registerRendererAccountReset(`library-workbench:${key}`, () => setAccount(getRendererAccountScopeToken())),
    [key]
  );
  const listRequest = useCallback(
    (signal: AbortSignal) => loadLiteraturePage({ query, cursor: cursor || undefined, signal }),
    [query, cursor]
  );
  const detailRequest = useCallback(
    (signal: AbortSignal) => (selectedId ? loadLiteratureItem(selectedId, signal) : Promise.resolve(undefined)),
    [selectedId]
  );
  const projectRequest = useCallback((signal: AbortSignal) => loadXScienceProjects({ signal }), []);
  const list = useLibraryRead(
    account,
    JSON.stringify([account, 'list', query, cursor, revision]),
    listRequest,
    query ? 200 : 0
  );
  const detail = useLibraryRead(account, JSON.stringify([account, 'item', selectedId, revision]), detailRequest);
  const projects = useLibraryRead(account, JSON.stringify([account, 'projects', revision]), projectRequest);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return { account, list, detail, projects, refresh };
}
