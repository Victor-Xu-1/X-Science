import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import {
  emptyPreviewWorkspaceSlice,
  loadPreviewWorkspace,
  previewWorkspaceStorageKey,
  savePreviewWorkspace,
  type PreviewWorkspace,
  type PreviewWorkspaceSlice,
} from './previewWorkspacePersistence';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

type WorkspaceState = {
  owner: string;
  key: string | null;
  generation: number;
  slice: PreviewWorkspaceSlice;
  inactive: Map<string, PreviewWorkspaceSlice>;
};

/** One state owner for the active canvas and its inactive project slices.
 * null is an unresolved route; undefined is a nonpersistent embedded/test canvas.
 * An action captured before a project/owner transition cannot mutate its successor.
 */
function usePreviewWorkspaceSlices(
  workspace: PreviewWorkspace | null | undefined,
  accountScope: string,
  defaultColumns: 1 | 2 | 3 = 2
) {
  const empty = () => ({ ...emptyPreviewWorkspaceSlice(), boardColumns: defaultColumns });
  const key = workspace === null ? null : workspace ? previewWorkspaceStorageKey(workspace) : 'ephemeral';
  const owner = accountScope;
  const [state, setState] = useState<WorkspaceState>(() => ({
    owner,
    key,
    generation: 0,
    slice: workspace ? loadPreviewWorkspace(workspace) : empty(),
    inactive: new Map(),
  }));
  if (state.key !== key || state.owner !== owner) {
    const inactive = state.owner === owner ? new Map(state.inactive) : new Map<string, PreviewWorkspaceSlice>();
    if (state.owner === owner && state.key) inactive.set(state.key, state.slice);
    const slice = (key && inactive.get(key)) || (workspace ? loadPreviewWorkspace(workspace) : empty());
    if (key) inactive.delete(key);
    setState({ owner, key, generation: state.generation + 1, slice, inactive });
  }

  const latest = useRef({ workspace, slice: state.slice, key });
  latest.current = { workspace, slice: state.slice, key };
  useEffect(() => {
    if (!workspace) return;
    const timer = setTimeout(() => savePreviewWorkspace(workspace, state.slice), 150);
    return () => {
      clearTimeout(timer);
      // Flush only the outgoing identity. A quick project switch must not
      // discard a pending save or write A's snapshot under B's key.
      if (latest.current.key !== key) savePreviewWorkspace(workspace, state.slice);
    };
  }, [key, workspace?.ownerId, state.slice]);
  useEffect(
    () => () => {
      const outgoing = latest.current;
      if (outgoing.workspace) savePreviewWorkspace(outgoing.workspace, outgoing.slice);
    },
    []
  );

  const generation = state.generation;
  const liveGeneration = useRef({ generation, key });
  liveGeneration.current = { generation, key };
  const ownsWorkspace = useCallback(
    () => liveGeneration.current.generation === generation && liveGeneration.current.key !== null,
    [generation]
  );
  const updateSlice = useCallback(
    (next: SetStateAction<PreviewWorkspaceSlice>) =>
      setState((current) => {
        if (current.generation !== generation || current.key === null) return current;
        const slice = typeof next === 'function' ? next(current.slice) : next;
        return Object.is(slice, current.slice) ? current : { ...current, slice };
      }),
    [generation]
  );
  const setters = useMemo(() => {
    const field =
      <K extends keyof PreviewWorkspaceSlice>(name: K): Dispatch<SetStateAction<PreviewWorkspaceSlice[K]>> =>
      (next) =>
        updateSlice((current) => {
          const value = typeof next === 'function' ? next(current[name]) : next;
          return Object.is(value, current[name]) ? current : { ...current, [name]: value };
        });
    return {
      setTabs: field('tabs'),
      setActiveTabId: field('activeTabId'),
      setBoardColumns: field('boardColumns'),
      setFocusedTabId: field('focusedTabId'),
    };
  }, [updateSlice]);
  const reset = useCallback(() => {
    setState((current) => ({
      ...current,
      generation: current.generation + 1,
      slice: emptyPreviewWorkspaceSlice(),
      inactive: new Map(),
    }));
  }, []);
  return { ...state.slice, ...setters, updateSlice, reset, generation, ownsWorkspace };
}

/** Route data is published by the existing conversation reader. A sidebar
 * preference is not evidence of the project of an unresolved conversation. */
export function usePreviewWorkspaceState(workspace: PreviewWorkspace | undefined, embedded: boolean) {
  const [binding, setBinding] = useState<{ routeKey: string; projectId?: string; workspaceId?: string } | null>(null);
  const [requiredOwner, setRequiredOwner] = useState<string | undefined>();
  const accountScope = getRendererAccountScopeToken();
  const live = useRef({ workspace, accountScope });
  live.current = { workspace, accountScope };
  const bindWorkspaceProject = useCallback(
    (conversationId: string, projectId?: string, workspaceId?: string) => {
      if (
        !workspace ||
        workspace.conversationId !== conversationId ||
        live.current.workspace?.routeKey !== workspace.routeKey ||
        live.current.workspace?.ownerId !== workspace.ownerId ||
        getRendererAccountScopeToken() !== accountScope
      )
        return;
      setBinding((current) =>
        current?.routeKey === workspace.routeKey &&
        current.projectId === projectId &&
        current.workspaceId === workspaceId
          ? current
          : { routeKey: workspace.routeKey, projectId, workspaceId }
      );
    },
    [workspace?.routeKey, workspace?.ownerId, workspace?.conversationId, accountScope]
  );
  let resolved: PreviewWorkspace | null | undefined = embedded ? undefined : workspace;
  if (!embedded && workspace) {
    if (!workspace.ownerId || (requiredOwner !== undefined && requiredOwner !== workspace.ownerId)) resolved = null;
    else if (workspace.conversationId)
      resolved =
        binding?.routeKey === workspace.routeKey
          ? { ...workspace, projectId: binding.projectId, workspaceId: binding.workspaceId }
          : null;
  }
  const state = usePreviewWorkspaceSlices(
    resolved,
    JSON.stringify([workspace?.ownerId ?? '', accountScope]),
    embedded ? 1 : 2
  );
  const resetSlices = state.reset;
  const reset = useCallback(() => {
    setRequiredOwner(getRendererAccountOwnerId());
    setBinding(null);
    resetSlices();
  }, [resetSlices]);
  return { ...state, bindWorkspaceProject, reset };
}
