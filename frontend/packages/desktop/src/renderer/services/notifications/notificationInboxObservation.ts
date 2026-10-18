import { ipcBridge } from '@/common';

/** Existing canonical events are invalidations only, never an inbox record. */
export function observeNotificationInbox(invalidate: () => void): () => void {
  const releaseCompletion = ipcBridge.conversation.turnCompleted.on(() => invalidate());
  const releaseConfirmation = ipcBridge.conversation.responseStream.on((event) => {
    if (event.type === 'acp_permission') invalidate();
  });
  const releaseRuntime = ipcBridge.runtime.statusChanged.on(() => invalidate());
  const releaseReconnect = ipcBridge.realtime.reconnected.on(() => invalidate());
  const visible = () => {
    if (!document.hidden) invalidate();
  };
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', visible);
  return () => {
    releaseCompletion();
    releaseConfirmation();
    releaseRuntime();
    releaseReconnect();
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', visible);
  };
}
