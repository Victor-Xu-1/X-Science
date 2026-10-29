import { ipcBridge } from '@/common';
import { Message } from '@arco-design/web-react';
import type { TFunction } from 'i18next';
import type { RefObject } from 'react';
import type { ContextCompactionController } from '@/renderer/services/compaction/ContextCompactionController';
import type { useConversationRuntimeView } from '../../runtime/useConversationRuntimeView';
import { safeErrorDiagnostic } from './acpSendBoxDiagnostics';

type Authority = { conversationId: string; ownerId: string };
type StopOptions = {
  backend: string;
  conversationId: string;
  compaction: ContextCompactionController | null;
  authority: RefObject<Authority>;
  mounted: RefObject<boolean>;
  runtime: ReturnType<typeof useConversationRuntimeView>;
  t: TFunction;
  resetQueueExecution: () => void;
  setAiProcessing: (value: boolean) => void;
  resetState: () => void;
};
/** Exact maintenance cancellation and ordinary tree cancellation remain distinct. */
export async function stopAcpConversation(options: StopOptions): Promise<void> {
  const { backend, conversationId, compaction, authority, mounted, runtime, t } = options;
  const origin = authority.current;
  // Latch before maintenance Stop can yield. A dispatched/uncertain send is never withdrawn here.
  const preparationCancelled = runtime.cancelSendPreparation();
  if (preparationCancelled) {
    options.resetQueueExecution();
    options.setAiProcessing(false);
    options.resetState();
  }
  if (compaction && (await compaction.stop())) return;
  if (preparationCancelled) return;
  if (!mounted.current || authority.current !== origin) return;
  // Compatibility cancellation is frame-authority based. Preserve that fallback
  // for ordinary X-Science turns, never for context maintenance.
  const turnId = runtime.activeTurnId ?? (backend.trim().toLowerCase() === 'xscience' ? conversationId : null);
  if (!turnId) {
    runtime.resetLocalGate('stop_missing_turn');
    Message.error(t('conversation.xscienceRuntime.sendBox.stopMissingTurn'));
    return;
  }
  runtime.markStopRequested(turnId);
  try {
    const result = await ipcBridge.conversation.stop.invoke({ conversation_id: conversationId, turn_id: turnId });
    if (!mounted.current || authority.current !== origin) return;
    runtime.markStopAcknowledged(turnId, result.runtime);
    options.resetQueueExecution();
    const stillProcessing = result.runtime.is_processing || result.runtime.state === 'cancelling';
    options.setAiProcessing(stillProcessing);
    if (!stillProcessing) options.resetState();
  } catch (error) {
    if (!mounted.current || authority.current !== origin) return;
    console.warn('[AcpSendBox] stop request failed', safeErrorDiagnostic(error));
    runtime.resetLocalGate('stop_failed');
    Message.error(t('conversation.xscienceRuntime.sendBox.stopFailed'));
  }
}
