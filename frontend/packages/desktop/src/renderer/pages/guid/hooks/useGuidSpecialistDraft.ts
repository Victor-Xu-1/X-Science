import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { uuid } from '@/common/utils';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  useSpecialistCatalog,
  type XScienceSpecialistControl,
} from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import type { SessionSpecialistView } from '@/renderer/services/runtime/sessionSpecialistController';
import type { SpecialistSelection } from '@/renderer/services/runtime/sessionSpecialistTypes';
import type { GuidSpecialistIntent } from './useGuidDelegationHandoff';

/** A selection for the existing Guid draft, not a second conversation creator. */
export function useGuidSpecialistDraft(
  ownerId: string,
  draftGeneration: number,
  baseAssistantId: string | null,
  pending: boolean
) {
  const account = getRendererAccountScopeToken(),
    catalog = useSpecialistCatalog();
  const owner = useMemo(() => ({ active: true }), [account, ownerId, draftGeneration, baseAssistantId]);
  const latest = useRef(owner);
  latest.current = owner;
  const [state, setState] = useState<{ owner: typeof owner; intent: GuidSpecialistIntent }>();
  const intent = state?.owner === owner ? state.intent : undefined;
  useLayoutEffect(() => {
    owner.active = true;
    return () => {
      owner.active = false;
    };
  }, [owner]);
  const current = useCallback(
    () =>
      owner.active &&
      latest.current === owner &&
      Boolean(ownerId) &&
      getRendererAccountScopeToken() === account &&
      getRendererAccountOwnerId() === ownerId,
    [owner, account, ownerId]
  );
  const change = useCallback(
    (name: string | null) => {
      if (current() && !pending) setState({ owner, intent: Object.freeze({ id: uuid(), name }) });
    },
    [owner, current, pending]
  );
  const chosen = catalog.choices.find((choice) =>
    intent ? choice.name === intent.name : choice.id === baseAssistantId
  );
  const selection: SpecialistSelection | null =
    intent?.name === null || chosen
      ? {
          agent_name: '',
          selection_name: chosen?.name ?? '',
          assistant_id: chosen?.id ?? '',
          display_name: chosen?.label ?? '',
          is_default: intent?.name === null,
          snapshot_id: '',
          revision: 0,
        }
      : null;
  const control: XScienceSpecialistControl = {
    applied: selection,
    desired: selection,
    choices: catalog.choices,
    pending,
    available: current(),
    draft: true,
    unavailable: !catalog.loading && !catalog.error && !selection,
    error: null,
    retryable: false,
    catalogLoading: catalog.loading,
    catalogError: catalog.error,
    hasLiveAttempt: false,
    prepared: false,
    change,
    refresh: catalog.refresh,
    retry: catalog.refresh,
    current,
    capture: () => ({}),
  };
  return { intent, control };
}
export function useGuidSpecialistPresentation(
  draft: XScienceSpecialistControl,
  view: SessionSpecialistView,
  retry: () => Promise<void>,
  refresh: () => Promise<void>
): XScienceSpecialistControl {
  const refreshCatalog = draft.refresh;
  const refreshAll = useCallback(() => {
    void refresh();
    void refreshCatalog();
  }, [refresh, refreshCatalog]);
  if (!view.available) return draft;
  const snapshot = view.snapshot;
  const applied = snapshot ? (snapshot.applied.assistant_id ? snapshot.applied : null) : draft.applied;
  const matchesDraft = Boolean(
    snapshot?.revision &&
    draft.desired &&
    (draft.desired.is_default
      ? snapshot.desired.is_default
      : draft.desired.selection_name === snapshot.desired.selection_name)
  );
  return {
    ...draft,
    pending: draft.pending || view.loading || view.writing,
    error: view.error,
    retryable: view.retryable,
    hasLiveAttempt: view.snapshot?.has_live_attempt ?? false,
    prepared: snapshot?.phase === 'prepared' && matchesDraft,
    applied,
    unavailable: snapshot
      ? snapshot.applied_unavailable ||
        Boolean(
          applied &&
          !draft.catalogLoading &&
          !draft.catalogError &&
          !draft.choices.some((choice) => choice.id === applied.assistant_id)
        )
      : draft.unavailable,
    desired: matchesDraft ? snapshot!.desired : draft.desired,
    retry,
    refresh: refreshAll,
  };
}
