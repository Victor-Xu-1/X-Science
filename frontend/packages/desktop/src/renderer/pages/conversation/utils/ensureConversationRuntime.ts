import { ipcBridge } from '@/common';
import type { EnsureConversationRuntimeResponse } from '@/common/types/platform/acpTypes';
import { registerRendererAccountReset, rendererAccountScopedKey } from '@/renderer/services/rendererAccountScope';

const ensureRuntimeByConversation = new Map<string, Promise<EnsureConversationRuntimeResponse>>();

export function ensureConversationRuntime(
  conversation_id: string,
  frameIncarnationId = '',
  snapshotGeneration = 0
): Promise<EnsureConversationRuntimeResponse> {
  const key = rendererAccountScopedKey(JSON.stringify([conversation_id, frameIncarnationId, snapshotGeneration]));
  const existing = ensureRuntimeByConversation.get(key);
  if (existing) {
    return existing;
  }

  const promise = ipcBridge.conversation.ensureRuntime.invoke({ conversation_id }).finally(() => {
    if (ensureRuntimeByConversation.get(key) === promise) ensureRuntimeByConversation.delete(key);
  });
  ensureRuntimeByConversation.set(key, promise);
  return promise;
}

export function resetEnsureConversationRuntimeStateForTests(): void {
  ensureRuntimeByConversation.clear();
}

registerRendererAccountReset('ensure-conversation-runtime', resetEnsureConversationRuntimeStateForTests);
