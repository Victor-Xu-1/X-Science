import { realtimeReconnectedEmitter, wsEmitter } from '@/common/adapter/httpBridge';
import type { IRuntimeStatusEvent } from '@/common/adapter/ipcRuntimeClient';
import type { CompactionIdentity } from './contextCompactionTypes';

const lifecyclePhases = new Set<IRuntimeStatusEvent['phase']>([
  'waiting_for_lock',
  'waiting_input',
  'downloading',
  'extracting',
  'validating',
  'ready',
  'failed',
]);

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
  const releaseRuntime = wsEmitter<IRuntimeStatusEvent>('durable', 'runtime.statusChanged').on((event) => {
    if (
      event.resource === 'acp_tool' &&
      event.scope?.kind === 'conversation' &&
      event.scope.id === identity.frameId &&
      (event.frame_id === undefined || event.frame_id === identity.frameId) &&
      (event.root_frame_id === undefined || event.root_frame_id === identity.rootFrameId) &&
      (event.boundary_kind === undefined || event.boundary_kind === 'attempt') &&
      event.tool_call_id === undefined &&
      event.tool_name === undefined &&
      lifecyclePhases.has(event.phase)
    )
      invalidate();
  });
  const releaseReconnect = realtimeReconnectedEmitter().on(invalidate);
  return () => {
    releaseEvent();
    releaseRuntime();
    releaseReconnect();
  };
}
