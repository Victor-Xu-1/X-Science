import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { loadLiteratureReferenceExport } from '@/renderer/services/runtime/literatureReferenceExport';
import { loadCompleteLiteratureSelection } from '@/renderer/services/runtime/literatureSelection';
import {
  literatureExportFilename,
  literatureExportFormats,
  type LiteratureExportFormat,
} from '@/common/adapter/literatureReferenceExport';
import { literatureSelectionScopeKey, type LiteratureScopeInput } from '@/common/adapter/literatureSelection';
import { downloadTextContent } from '@/renderer/utils/file/download';
import { resolveLibrarySelection, type LibrarySelectionState } from './librarySelectionState';
type Phase = 'idle' | 'loading' | 'success' | 'error';
type Kind = 'selected' | 'scope';
type State = { owner: object; id: number; kind: Kind; phase: Phase };
type Work = { owner: object; id: number; kind: Kind; abort: AbortController };
export type LibraryReferenceExportModel = {
  phase: Phase;
  disabled: boolean;
  start: (format: LiteratureExportFormat) => Promise<boolean>;
};
export type LibraryCurrentExportScope = Pick<LiteratureScopeInput, 'projectId' | 'collectionId'> & { name: string };
/** One page-owned read/export slot includes membership resolution before the first await. */
export function useLibraryReferenceExport({
  account,
  visitKey,
  selection,
  scope,
  currentScope,
  blocked,
}: {
  account: string;
  visitKey: string;
  selection: LibrarySelectionState;
  scope: LiteratureScopeInput;
  currentScope?: LibraryCurrentExportScope;
  blocked: boolean;
}) {
  const selectionKey = JSON.stringify(selection);
  const scopeKey = literatureSelectionScopeKey(scope);
  const currentScopeKey = JSON.stringify(currentScope);
  const owner = useMemo(() => ({}), [account, visitKey, selectionKey, scopeKey, currentScopeKey]);
  const latest = useRef(owner);
  latest.current = owner;
  const work = useRef<Work | undefined>(undefined);
  const sequence = useRef(0);
  const initial: State = { owner, id: 0, kind: 'selected', phase: 'idle' };
  const [state, setState] = useState(initial);
  const display = useRef(initial);
  const timer = useRef<{ owner: object; handle: ReturnType<typeof setTimeout> } | undefined>(undefined);
  const resetKey = useId();
  const clearTimer = (forOwner?: object) => {
    if (timer.current && (!forOwner || timer.current.owner === forOwner)) {
      clearTimeout(timer.current.handle);
      timer.current = undefined;
    }
  };
  const publish = (next: State) => {
    display.current = next;
    setState(next);
  };
  useEffect(() => {
    const cancel = () => {
      clearTimer(owner);
      if (work.current?.owner === owner) {
        work.current.abort.abort();
        work.current = undefined;
      }
    };
    const unregister = registerRendererAccountReset(`library-export:${resetKey}`, cancel);
    return () => {
      unregister();
      cancel();
    };
  }, [owner, resetKey]);
  const pending = state.owner === owner && state.phase === 'loading';
  const run = async (kind: Kind, format: LiteratureExportFormat): Promise<boolean> => {
    if (
      blocked ||
      work.current ||
      account !== getRendererAccountScopeToken() ||
      (kind === 'scope' && !currentScope) ||
      (kind === 'selected' && !selection.allMatchingSelected && !selection.selectedIds.length)
    )
      return false;
    clearTimer();
    const active: Work = { owner, id: ++sequence.current, kind, abort: new AbortController() };
    work.current = active;
    const current = () =>
      latest.current === owner &&
      work.current === active &&
      !active.abort.signal.aborted &&
      account === getRendererAccountScopeToken();
    const status = (phase: Phase) => publish({ owner, id: active.id, kind, phase });
    status('loading');
    try {
      let ids: readonly string[];
      if (kind === 'selected') {
        const complete = selection.allMatchingSelected
          ? await loadCompleteLiteratureSelection(scope, active.abort.signal)
          : undefined;
        if (!current()) return false;
        ids = resolveLibrarySelection(selection, complete?.item_ids);
      } else {
        const complete = await loadCompleteLiteratureSelection(
          { projectId: currentScope!.projectId, collectionId: currentScope!.collectionId, lifecycle: 'active' },
          active.abort.signal,
          { order: 'title-asc' }
        );
        if (!current()) return false;
        ids = complete.item_ids;
      }
      if (!ids.length) {
        status('idle');
        return false;
      }
      const text = await loadLiteratureReferenceExport(ids, format, active.abort.signal);
      if (!current()) return false;
      const stem = kind === 'selected' ? 'selected-references' : `${currentScope!.name}-references`;
      downloadTextContent(text, literatureExportFilename(stem, format), literatureExportFormats[format].mimeType);
      status('success');
      // Presentation only: an old completion cannot clear a newer operation or visit.
      timer.current = {
        owner,
        handle: setTimeout(() => {
          if (latest.current === owner && display.current.owner === owner && display.current.id === active.id)
            status('idle');
        }, 1500),
      };
      return true;
    } catch {
      if (current()) status('error');
      return false;
    } finally {
      if (work.current === active) work.current = undefined;
    }
  };
  const model = (kind: Kind): LibraryReferenceExportModel => ({
    phase: state.owner === owner && state.kind === kind ? state.phase : 'idle',
    disabled:
      blocked ||
      pending ||
      (kind === 'scope' ? !currentScope : !selection.allMatchingSelected && !selection.selectedIds.length),
    start: (format) => run(kind, format),
  });
  return {
    pending,
    identityKey: JSON.stringify([account, visitKey, selectionKey, scopeKey, currentScopeKey]),
    selected: model('selected'),
    scope: model('scope'),
    scopeAvailable: Boolean(currentScope),
  };
}
