import { useCallback, useEffect, useMemo, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { resolveLocaleKey } from '@/common/utils';
import { useXScienceAssistantsLoader } from '@/renderer/pages/guid/hooks/useXScienceAssistantsLoader';
import { resolveAssistantName } from '@/renderer/utils/model/assistantDisplay';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { sessionDelegationIdentity } from '@/renderer/services/runtime/sessionDelegationPolicy';
import {
  EMPTY_SPECIALIST_VIEW,
  SessionSpecialistController,
} from '@/renderer/services/runtime/sessionSpecialistController';
import {
  sameSpecialistBranch,
  type SpecialistFailure,
  type SpecialistSelection,
  type SpecialistSendAuthority,
} from '@/renderer/services/runtime/sessionSpecialistTypes';

export type SpecialistChoice = { id: string; name: string; label: string };
export type XScienceSpecialistControl = {
  applied: SpecialistSelection | null;
  desired: SpecialistSelection | null;
  choices: SpecialistChoice[];
  pending: boolean;
  available: boolean;
  unavailable: boolean;
  error: SpecialistFailure;
  retryable: boolean;
  catalogLoading: boolean;
  catalogError: boolean;
  hasLiveAttempt: boolean;
  prepared: boolean;
  draft?: boolean;
  change: (name: string | null) => void | Promise<void>;
  refresh: () => void | Promise<void>;
  retry: () => void | Promise<void>;
  current: () => boolean;
  capture: (targetBranchId?: string | null) => SpecialistSendAuthority;
};
export function useSpecialistCatalog() {
  const catalog = useXScienceAssistantsLoader(),
    { i18n } = useTranslation();
  const locale = resolveLocaleKey(i18n.language);
  const choices = useMemo(
    () =>
      catalog.assistants
        .filter((a) => a.enabled === true && Boolean(a.agent_id))
        .map((a) => ({ id: a.id, name: a.agent_id!, label: resolveAssistantName(a, locale, a.name) })),
    [catalog.assistants, locale]
  );
  return {
    choices,
    loading: catalog.catalogStatus === 'loading',
    error: catalog.catalogStatus === 'error',
    refresh: catalog.retry,
  };
}
const noSubscribe = () => () => {};
const empty = () => EMPTY_SPECIALIST_VIEW;
const noChange = () => {};
export function useSessionSpecialist(ownerId: string, frameId: string, extra: unknown): XScienceSpecialistControl {
  const account = getRendererAccountScopeToken(),
    identity = sessionDelegationIdentity(ownerId, frameId, extra);
  const incarnation = identity?.frameIncarnationId,
    root = identity?.rootFrameId;
  const controller = useMemo(
    () =>
      incarnation && root
        ? new SessionSpecialistController({ ownerId, frameId, frameIncarnationId: incarnation, rootFrameId: root })
        : null,
    [account, ownerId, frameId, incarnation, root]
  );
  useEffect(() => controller?.attach(), [controller]);
  const view = useSyncExternalStore(controller?.subscribe ?? noSubscribe, controller?.getSnapshot ?? empty, empty);
  const catalog = useSpecialistCatalog();
  const refresh = useCallback(() => {
    void controller?.refresh();
    void catalog.refresh();
  }, [controller, catalog.refresh]);
  return useMemo(() => {
    const current = controller?.current() === true,
      snapshot = current ? view.snapshot : null;
    const applied = snapshot?.applied.assistant_id ? snapshot.applied : null;
    return {
      applied,
      desired: snapshot?.revision ? snapshot.desired : null,
      choices: catalog.choices,
      pending: view.loading || view.writing,
      available: current && view.available && Boolean(snapshot),
      unavailable: Boolean(
        snapshot?.applied_unavailable ||
        (applied &&
          !catalog.loading &&
          !catalog.error &&
          !catalog.choices.some((choice) => choice.id === applied.assistant_id))
      ),
      error:
        view.error ??
        (snapshot?.preparation_error
          ? 'unavailable'
          : snapshot?.revision && !sameSpecialistBranch(snapshot.branch, snapshot.prepared_branch)
            ? 'branch'
            : null),
      retryable: view.retryable,
      catalogLoading: catalog.loading,
      catalogError: catalog.error,
      hasLiveAttempt: snapshot?.has_live_attempt ?? false,
      prepared: snapshot?.phase === 'prepared',
      change: controller?.change ?? noChange,
      retry: controller?.retry ?? noChange,
      refresh,
      current: controller?.current ?? (() => false),
      capture: controller?.capture ?? (() => ({})),
    } satisfies XScienceSpecialistControl;
  }, [controller, view, catalog.choices, catalog.loading, catalog.error, refresh]);
}
