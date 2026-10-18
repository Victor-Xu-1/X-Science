import { useCallback, useMemo, useState, useSyncExternalStore } from 'react';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { getContextCompactionController } from '@/renderer/services/compaction/contextCompactionRegistry';
import { compactionUnresolved, type CompactionView } from '@/renderer/services/compaction/contextCompactionTypes';

const empty: CompactionView = {
  snapshot: null,
  request: null,
  receipt: null,
  loading: false,
  mutation: null,
  uncertain: null,
  error: null,
  waitingForPrompt: false,
};
const noSubscribe = () => () => {};
const emptySnapshot = () => empty;
export function useAcpContextCompaction(
  enabled: boolean,
  ownerId: string,
  frameId: string,
  conversation?: TChatConversation
) {
  const extra = conversation?.extra as Record<string, unknown> | undefined;
  const frameIncarnationId = typeof extra?.frame_incarnation_id === 'string' ? extra.frame_incarnation_id : '';
  const rootFrameId = typeof extra?.root_frame_id === 'string' ? extra.root_frame_id : '';
  const account = getRendererAccountScopeToken();
  const eligible = Boolean(enabled && ownerId && frameIncarnationId && rootFrameId);
  const [allocationAttempt, setAllocationAttempt] = useState(0);
  const retryCapacity = useCallback(() => setAllocationAttempt((attempt) => attempt + 1), []);
  const controller = useMemo(
    () => (eligible ? getContextCompactionController({ ownerId, frameId, frameIncarnationId, rootFrameId }) : null),
    [eligible, ownerId, frameId, frameIncarnationId, rootFrameId, account, allocationAttempt]
  );
  const view = useSyncExternalStore(controller?.subscribe ?? noSubscribe, controller?.getSnapshot ?? emptySnapshot);
  const capacityLimited = eligible && controller === null;
  const needsAttention =
    capacityLimited ||
    compactionUnresolved(view) ||
    view.error !== null ||
    view.waitingForPrompt ||
    view.receipt?.archive_status === 'pending';
  return { controller, view, capacityLimited, retryCapacity, needsAttention };
}
