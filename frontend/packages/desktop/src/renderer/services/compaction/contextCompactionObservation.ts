import { realtimeReconnectedEmitter, wsEmitter } from '@/common/adapter/httpBridge';
import type { CompactionIdentity } from './contextCompactionTypes';

/** Existing account-bound fanout is a dirty hint only, never execution authority. */
export function observeContextCompaction(identity: CompactionIdentity, invalidate: () => void): () => void {
  const releaseEvent = wsEmitter<Record<string, unknown>>('durable', 'compaction_status').on((event) => {
    if (
      event.schema === 'x-science.context-compaction-event.v1' &&
      event.conversation_id === identity.frameId &&
      event.frame_incarnation_id === identity.frameIncarnationId &&
      event.root_frame_id === identity.rootFrameId
    )
      invalidate();
  });
  const releaseReconnect = realtimeReconnectedEmitter().on(invalidate);
  return () => {
    releaseEvent();
    releaseReconnect();
  };
}
