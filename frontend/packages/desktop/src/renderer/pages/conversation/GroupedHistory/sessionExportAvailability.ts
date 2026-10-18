import type { TChatConversation, TConversationRuntimeSummary } from '@/common/config/storage';
import type { ConversationRuntimeView } from '../runtime/conversationRuntimeViewStore';
import {
  isConversationRuntimeUnfinished,
  resolveConversationTaskIndicatorState,
} from './conversationTaskIndicatorModel';

// Export is a stable snapshot action. Read-only notebook browsing is deliberately
// independent of this predicate and remains available during running/waiting.
export function isSessionExportDisabled(
  conversation: TChatConversation,
  isGenerating = false,
  currentView?: ConversationRuntimeView
): boolean {
  if (currentView?.localSubmitting || currentView?.localStopping) return true;
  const runtime: TConversationRuntimeSummary | undefined = currentView?.hasBackendRuntime
    ? {
        state: currentView.state,
        is_processing: currentView.isProcessing,
        pending_confirmations: currentView.pendingConfirmations,
        has_task: currentView.hasTask,
        task_status: currentView.taskStatus ?? undefined,
        can_send_message: currentView.canSendMessage,
        turn_id: currentView.activeTurnId,
      }
    : conversation.runtime;
  if (runtime) {
    if (runtime.is_processing || runtime.pending_confirmations > 0) return true;
    return runtime.state !== 'paused' && isConversationRuntimeUnfinished(runtime);
  }
  return resolveConversationTaskIndicatorState(conversation, isGenerating) === 'running';
}
