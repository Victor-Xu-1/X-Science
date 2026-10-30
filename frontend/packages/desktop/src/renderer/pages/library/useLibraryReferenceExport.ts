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
type Timer = { owner: object; handle: ReturnType<typeof setTimeout> };
type Holder<T> = { current: T };
export type LibraryReferenceExportModel = {
  identityKey: string;
  phase: Phase;
  triggerDisabled: boolean;
  disabled: boolean;
  start: (format: LiteratureExportFormat) => Promise<boolean>;
};
export type LibraryCurrentExportScope = Pick<LiteratureScopeInput, 'projectId' | 'collectionId'> & { name: string };

// These are invalidation fences over the same Work slot, not separate export owners.
function useExportLifetime(
  owner: object,
  kind: Kind,
  key: string,
  work: Holder<Work | undefined>,
  timer: Holder<Timer | undefined>
) {
  useEffect(() => {
    const cancel = () => {
      if (timer.current?.owner === owner) {
        clearTimeout(timer.current.handle);
        timer.current = undefined;
      }
      if (work.current?.owner === owner && work.current.kind === kind) {
        work.current.abort.abort();
        work.current = undefined;
      }
    };
    const unregister = registerRendererAccountReset(`library-export:${key}:${kind}`, cancel);
    return () => {
      unregister();
      cancel();
    };
  }, [owner, kind, key, work, timer]);
}
/** One page-owned read/export slot covers membership resolution before the first await. */
export function useLibraryReferenceExport({
  account,
  selection,
  scope,
  currentScope,
  blocked,
  selectionBlocked = false,
  scopeBlocked = false,
}: {
  account: string;
  selection: LibrarySelectionState;
  scope: LiteratureScopeInput;
  currentScope?: LibraryCurrentExportScope;
  blocked: boolean;
  selectionBlocked?: boolean;
  scopeBlocked?: boolean;
}) {
  const selectionKey = JSON.stringify(selection);
  const filterKey = literatureSelectionScopeKey(scope);
  const scopeTargetKey = JSON.stringify([scope.projectId ?? '', scope.collectionId ?? '']);
  const selectedOwner = useMemo(() => ({}), [account, selectionKey, filterKey]);
  const scopeOwner = useMemo(() => ({}), [account, scopeTargetKey]);
  const owners = { selected: selectedOwner, scope: scopeOwner };
  const latest = useRef(owners);
  latest.current = owners;
  const work = useRef<Work | undefined>(undefined);
  const timer = useRef<Timer | undefined>(undefined);
  const sequence = useRef(0);
  const initial: State = { owner: selectedOwner, id: 0, kind: 'selected', phase: 'idle' };
  const [state, setState] = useState(initial);
  const display = useRef(initial);
  const resetKey = useId();
  useExportLifetime(selectedOwner, 'selected', resetKey, work, timer);
  useExportLifetime(scopeOwner, 'scope', resetKey, work, timer);
  const publish = (next: State) => {
    display.current = next;
    setState(next);
  };
  const pending = state.owner === owners[state.kind] && state.phase === 'loading';
  const run = async (kind: Kind, format: LiteratureExportFormat): Promise<boolean> => {
    const owner = owners[kind];
    if (
      latest.current[kind] !== owner ||
      blocked ||
      (kind === 'selected' ? selectionBlocked : scopeBlocked) ||
      work.current ||
      account !== getRendererAccountScopeToken() ||
      (kind === 'scope' && !currentScope) ||
      (kind === 'selected' && !selection.allMatchingSelected && !selection.selectedIds.length)
    )
      return false;
    if (timer.current) {
      clearTimeout(timer.current.handle);
      timer.current = undefined;
    }
    const target = currentScope ? { ...currentScope } : undefined;
    const selectedScope = { ...scope };
    const capturedSelection = {
      ...selection,
      selectedIds: [...selection.selectedIds],
      excludedMatchingIds: [...selection.excludedMatchingIds],
    };
    const active: Work = { owner, id: ++sequence.current, kind, abort: new AbortController() };
    work.current = active;
    const current = () =>
      latest.current[kind] === owner &&
      work.current === active &&
      !active.abort.signal.aborted &&
      account === getRendererAccountScopeToken();
    const status = (phase: Phase) => publish({ owner, id: active.id, kind, phase });
    status('loading');
    try {
      let ids: readonly string[];
      if (kind === 'selected') {
        const complete = capturedSelection.allMatchingSelected
          ? await loadCompleteLiteratureSelection(selectedScope, active.abort.signal)
          : undefined;
        if (!current()) return false;
        ids = resolveLibrarySelection(capturedSelection, complete?.item_ids);
      } else {
        const complete = await loadCompleteLiteratureSelection(
          { projectId: target!.projectId, collectionId: target!.collectionId, lifecycle: 'active' },
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
      const stem = kind === 'selected' ? 'selected-references' : `${target!.name}-references`;
      downloadTextContent(text, literatureExportFilename(stem, format), literatureExportFormats[format].mimeType);
      status('success');
      // A short display timer cannot clear a newer operation, target or account.
      timer.current = {
        owner,
        handle: setTimeout(() => {
          if (latest.current[kind] === owner && display.current.owner === owner && display.current.id === active.id)
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
    identityKey: JSON.stringify([account, kind, kind === 'scope' ? scopeTargetKey : [selectionKey, filterKey]]),
    phase: state.owner === owners[kind] && state.kind === kind ? state.phase : 'idle',
    triggerDisabled: blocked || pending,
    disabled:
      blocked ||
      (kind === 'selected' ? selectionBlocked : scopeBlocked) ||
      pending ||
      (kind === 'scope' ? !currentScope : !selection.allMatchingSelected && !selection.selectedIds.length),
    start: (format) => run(kind, format),
  });
  return { pending, selected: model('selected'), scope: model('scope'), scopeAvailable: Boolean(currentScope) };
}
