import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { loadLiteratureBibtex } from '@/renderer/services/runtime/literatureBibtex';
import { downloadTextContent } from '@/renderer/utils/file/download';
import type { LibrarySelectionState } from './librarySelectionState';

type Phase = 'idle' | 'loading' | 'success' | 'error';
type Work = { owner: object; abort: AbortController };

/** Export belongs to this exact selection and visit, independently of the mutation ledger. */
export function useLibraryBibtexExport(
  account: string,
  visitKey: string,
  selection: LibrarySelectionState,
  blocked: boolean
) {
  const selectionKey = JSON.stringify([selection.allMatchingSelected, selection.selectedIds]);
  const owner = useMemo(() => ({}), [account, visitKey, selectionKey]);
  const latest = useRef(owner);
  latest.current = owner;
  const work = useRef<Work | undefined>(undefined);
  const [state, setState] = useState<{ owner: object; phase: Phase }>({ owner, phase: 'idle' });
  const resetKey = useId();
  useEffect(() => {
    const cancel = () => {
      if (work.current?.owner === owner) {
        work.current.abort.abort();
        work.current = undefined;
      }
    };
    const unregister = registerRendererAccountReset(`library-bibtex:${resetKey}`, cancel);
    return () => {
      unregister();
      cancel();
    };
  }, [owner, resetKey]);
  const phase = state.owner === owner ? state.phase : 'idle';
  const disabled = blocked || selection.allMatchingSelected || !selection.selectedIds.length || phase === 'loading';
  const start = async () => {
    if (disabled || work.current || account !== getRendererAccountScopeToken()) return;
    const active: Work = { owner, abort: new AbortController() };
    const ids = [...selection.selectedIds];
    work.current = active;
    const current = () =>
      latest.current === owner &&
      work.current === active &&
      !active.abort.signal.aborted &&
      account === getRendererAccountScopeToken();
    setState({ owner, phase: 'loading' });
    try {
      const text = await loadLiteratureBibtex(ids, active.abort.signal);
      if (!current()) return;
      downloadTextContent(text, 'selected-references.bib', 'application/x-bibtex;charset=utf-8');
      if (current()) setState({ owner, phase: 'success' });
    } catch {
      if (current()) setState({ owner, phase: 'error' });
    } finally {
      if (work.current === active) work.current = undefined;
    }
  };
  return { phase, disabled, start };
}
export type LibraryBibtexExportModel = ReturnType<typeof useLibraryBibtexExport>;
