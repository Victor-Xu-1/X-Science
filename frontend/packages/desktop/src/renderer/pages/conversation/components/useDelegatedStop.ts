import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import {
  getSelectedXScienceBranch,
  getXScienceBranchSelectionRevision,
  X_SCIENCE_BRANCH_SELECTION_EVENT,
  type XScienceBranchSelectionEventDetail,
} from '@/renderer/services/xScienceConversationBranches';
import { subscribeXScienceConversationWake } from '@/renderer/services/xScienceConversationWake';
import { acquireDelegatedStopController } from '@/renderer/services/runtime/delegatedStopRegistry';
import type { DelegatedStopView } from '@/renderer/services/runtime/delegatedStopTypes';

const empty: DelegatedStopView = {
  snapshot: null,
  loading: false,
  issue: null,
  operation: null,
  recoveryLimited: false,
};
const noopSubscribe = (): (() => void) => () => {};
const emptySnapshot = (): DelegatedStopView => empty;
export function useDelegatedStop(ownerId: string, parentId: string, frameIncarnationId: string) {
  const [retry, setRetry] = useState(0);
  const token = getRendererAccountScopeToken();
  const controller = useMemo(
    () => acquireDelegatedStopController({ ownerId, parentId, frameIncarnationId }),
    [ownerId, parentId, frameIncarnationId, token, retry]
  );
  const view = useSyncExternalStore(controller?.subscribe ?? noopSubscribe, controller?.getSnapshot ?? emptySnapshot);
  useEffect(
    () =>
      registerRendererAccountReset('delegated-stop-view:' + JSON.stringify([parentId, frameIncarnationId]), () =>
        setRetry((value) => value + 1)
      ),
    [parentId, frameIncarnationId]
  );
  useEffect(() => {
    if (!controller) return;
    const select = () => {
      controller.selectBranch(getSelectedXScienceBranch(parentId), getXScienceBranchSelectionRevision(parentId));
      void controller.observe();
    };
    const branchChanged = (event: Event) => {
      if ((event as CustomEvent<XScienceBranchSelectionEventDetail>).detail?.conversationId === parentId) select();
    };
    select();
    window.addEventListener(X_SCIENCE_BRANCH_SELECTION_EVENT, branchChanged);
    const unsubscribe = subscribeXScienceConversationWake(
      parentId,
      () => {
        void controller.observe();
      },
      { includeRelatedConversations: true }
    );
    return () => {
      window.removeEventListener(X_SCIENCE_BRANCH_SELECTION_EVENT, branchChanged);
      unsubscribe();
    };
  }, [controller, parentId]);
  return {
    controller,
    view,
    scopeKey: JSON.stringify([token, ownerId, parentId, frameIncarnationId]),
    retry: () => setRetry((value) => value + 1),
    unavailable: Boolean(ownerId && parentId && frameIncarnationId && !controller),
  };
}
export type DelegatedStopPresentation = ReturnType<typeof useDelegatedStop>;
