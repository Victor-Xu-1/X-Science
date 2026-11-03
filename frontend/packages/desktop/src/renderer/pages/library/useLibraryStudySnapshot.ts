import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  literatureStudySourceKey,
  type LiteratureStudyGenerate,
  type LiteratureStudyView,
} from '@/common/adapter/literatureStudySnapshot';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { LiteratureStudyRequestError, requestLiteratureStudy } from './literatureStudyApi';

type Owner = {
  item: LiteratureItem;
  key: string;
  active: boolean;
  busy: boolean;
  sequence: number;
  controller?: AbortController;
  operation?: LiteratureStudyGenerate;
};
type State = {
  loading: boolean;
  pending: boolean;
  view?: LiteratureStudyView;
  issue?: string;
  failedRead?: boolean;
  invalid?: boolean;
};
export function useLibraryStudySnapshot(account: string, item: LiteratureItem, language: string, visible: boolean) {
  const key = JSON.stringify([account, literatureStudySourceKey(item), language]);
  const ownerRef = useRef<Owner>({ item, key, active: true, busy: false, sequence: 0 });
  if (ownerRef.current.key !== key) {
    ownerRef.current.active = false;
    ownerRef.current.controller?.abort();
    ownerRef.current = { item, key, active: true, busy: false, sequence: 0 };
  }
  const owner = ownerRef.current;
  const [owned, setOwned] = useState<{ key: string; value: State }>({ key, value: { loading: true, pending: false } });
  const state: State = owned.key === key ? owned.value : { loading: true, pending: false };
  const stateRef = useRef(state);
  stateRef.current = state;
  const current = useCallback(
    () => owner.active && ownerRef.current === owner && getRendererAccountScopeToken() === account,
    [account, owner]
  );
  const run = useCallback(
    async (generate: boolean) => {
      if (!visible || owner.item.deleted || !current() || owner.busy) return;
      const previous = stateRef.current;
      if (generate && (!previous.view?.available || previous.invalid)) return;
      owner.busy = true;
      const sequence = ++owner.sequence;
      const controller = new AbortController();
      owner.controller = controller;
      if (generate && !owner.operation)
        owner.operation = {
          operation_id: crypto.randomUUID(),
          language,
          expected_source_key: previous.view!.source_key,
          expected_revision: previous.view!.revision,
        };
      setOwned({ key, value: { ...previous, loading: !generate, pending: generate, issue: undefined } });
      try {
        const view = await requestLiteratureStudy(
          owner.item,
          language,
          controller.signal,
          generate ? owner.operation : undefined
        );
        if (!current() || sequence !== owner.sequence) return;
        owner.operation = undefined;
        setOwned({ key, value: { view, loading: false, pending: false } });
      } catch (error) {
        if (!current() || sequence !== owner.sequence || controller.signal.aborted) return;
        const typed = error instanceof LiteratureStudyRequestError ? error : undefined;
        if (typed?.code === 'LITERATURE_STUDY_SOURCE_UNAVAILABLE') {
          // No current evidence: clear the display, but allow explicit cached
          // read/reopen recovery for this still-owned immutable source.
          owner.operation = undefined;
          setOwned({ key, value: { loading: false, pending: false, issue: typed.code, failedRead: true } });
        } else if (typed?.scopeChanged) {
          owner.operation = undefined;
          setOwned({ key, value: { loading: false, pending: false, issue: typed.code, invalid: true } });
        } else {
          const conflict = typed?.code === 'LITERATURE_STUDY_CONFLICT';
          if (conflict) owner.operation = undefined;
          setOwned({
            key,
            value: {
              ...previous,
              loading: false,
              pending: false,
              issue: typed?.code ?? 'LITERATURE_STUDY_FAILED',
              failedRead: !generate || conflict,
            },
          });
        }
      } finally {
        if (sequence === owner.sequence) owner.busy = false;
      }
    },
    [account, current, key, language, owner, visible]
  );
  useEffect(() => {
    owner.active = true;
    if (item.deleted) setOwned({ key, value: { loading: false, pending: false, invalid: true } });
    else if (visible) void run(false);
    return () => {
      owner.active = false;
      owner.sequence++;
      owner.controller?.abort();
      owner.busy = false;
    };
  }, [key, owner, visible, item.deleted, run]);
  const resetID = useId();
  useEffect(
    () =>
      registerRendererAccountReset('literature-study:' + resetID, () => {
        owner.active = false;
        owner.sequence++;
        owner.controller?.abort();
        owner.operation = undefined;
        setOwned({ key, value: { loading: false, pending: false, invalid: true } });
      }),
    [key, owner, resetID]
  );
  const retryRead = !state.view || state.failedRead;
  return { ...state, read: (): void => void run(false), generate: (): void => void run(true), retryRead };
}
