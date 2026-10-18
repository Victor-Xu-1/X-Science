import { useCallback } from 'react';
import type { TConversationRuntimeSummary } from '@/common/config/storage';
import type { useUpdateMessageList } from '../../Messages/hooks';
import type { useConversationRuntimeView } from '../../runtime/useConversationRuntimeView';
import {
  markTerminalFailuresSuperseded,
  restoreTerminalFailures,
  type TerminalFailureRollback,
} from '../../runtime/terminalFailureMessagesModel';
import { emitter } from '@/renderer/utils/emitter';

export function useAcpRuntimeActions(
  conversationId: string,
  runtimeView: ReturnType<typeof useConversationRuntimeView>,
  updateMessageList: ReturnType<typeof useUpdateMessageList>,
  terminalFailureRollbackRef: { current: TerminalFailureRollback },
  setAiProcessing: (processing: boolean) => void,
  onRuntimeMutation?: () => void
) {
  const handleRuntimeUpdated = useCallback(
    (turnId: string, runtime: TConversationRuntimeSummary, source: 'initial' | 'refresh' = 'refresh') => {
      terminalFailureRollbackRef.current = [];
      runtimeView.markSendAccepted(turnId, runtime);
      emitter.emit('xscience.runtime.reconciled', conversationId, runtime);
      setAiProcessing(runtime.is_processing && runtime.state !== 'waiting_confirmation');
      if (source !== 'initial') onRuntimeMutation?.();
    },
    [conversationId, onRuntimeMutation, runtimeView, setAiProcessing, terminalFailureRollbackRef]
  );
  const handleResumeStarted = useCallback(() => {
    updateMessageList((messages) => {
      const marked = markTerminalFailuresSuperseded(messages);
      if (marked.rollback.length > 0) terminalFailureRollbackRef.current = marked.rollback;
      return marked.messages;
    });
    runtimeView.markSendStarted();
  }, [runtimeView, updateMessageList, terminalFailureRollbackRef]);
  const handleResumeFailed = useCallback(
    (reason: string) => {
      const rollback = terminalFailureRollbackRef.current;
      terminalFailureRollbackRef.current = [];
      if (rollback.length > 0) updateMessageList((messages) => restoreTerminalFailures(messages, rollback));
      runtimeView.markSendFailed(reason);
    },
    [runtimeView, updateMessageList, terminalFailureRollbackRef]
  );
  return { handleRuntimeUpdated, handleResumeStarted, handleResumeFailed };
}
