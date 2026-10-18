import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { useCallback, useEffect, useRef, useState } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { XScienceSettingsRequestError } from '@/renderer/services/xScienceWorkspaceSettings';
import { loadHostFolders, type XScienceHostGrant } from '@/renderer/services/runtime/hostFiles';

type FolderState = { status: 'loading' | 'loaded' | 'error'; grants: XScienceHostGrant[]; revision: number };

export function useHostFiles() {
  const [state, setState] = useState<FolderState>({ status: 'loading', grants: [], revision: 0 });
  const [busy, setBusy] = useState(false);
  const [mutationError, setMutationError] = useState<'operatorRequired' | 'changeFailed' | null>(null);
  const [refreshRequired, setRefreshRequired] = useState(false);
  const owner = useRef(getRendererAccountScopeToken());
  const epoch = useRef(0);
  const sequence = useRef(0);
  const read = useRef<AbortController | null>(null);
  const mutating = useRef(false);
  const isCurrent = useCallback(
    (generation: number) => epoch.current === generation && owner.current === getRendererAccountScopeToken(),
    []
  );

  const refresh = useCallback(async (): Promise<boolean> => {
    const generation = epoch.current;
    const request = ++sequence.current;
    read.current?.abort();
    const controller = new AbortController();
    read.current = controller;
    setState((current) => ({ ...current, status: 'loading' }));
    try {
      const grants = await loadHostFolders(controller.signal);
      if (!isCurrent(generation) || request !== sequence.current) return false;
      setState((current) => ({ status: 'loaded', grants, revision: current.revision + 1 }));
      setRefreshRequired(false);
      return true;
    } catch {
      if (isCurrent(generation) && request === sequence.current)
        setState((current) => ({ ...current, status: 'error' }));
      return false;
    }
  }, [isCurrent]);

  useEffect(() => {
    epoch.current += 1;
    void refresh();
    return () => {
      epoch.current += 1;
      sequence.current += 1;
      read.current?.abort();
    };
  }, [refresh]);

  const changeAccess = useCallback(
    async (operation: () => Promise<boolean | void>): Promise<boolean> => {
      if (mutating.current) return false;
      mutating.current = true;
      const generation = epoch.current;
      sequence.current += 1;
      read.current?.abort();
      setBusy(true);
      setMutationError(null);
      try {
        const changed = await operation();
        if (!isCurrent(generation)) return false;
        if (changed !== false) setRefreshRequired(true);
        await refresh();
        return changed !== false;
      } catch (error: unknown) {
        if (isCurrent(generation)) {
          const forbidden =
            (isBackendHttpError(error) || error instanceof XScienceSettingsRequestError) && error.status === 403;
          setMutationError(forbidden ? 'operatorRequired' : 'changeFailed');
          setRefreshRequired(true);
          setState((current) => ({ ...current, status: 'error' }));
        }
        return false;
      } finally {
        mutating.current = false;
        if (isCurrent(generation)) setBusy(false);
      }
    },
    [isCurrent, refresh]
  );

  return { ...state, busy, mutationError, refreshRequired, refresh, changeAccess };
}
