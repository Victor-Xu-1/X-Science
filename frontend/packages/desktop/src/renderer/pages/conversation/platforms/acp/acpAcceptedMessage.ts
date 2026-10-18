import type { TMessage } from '@/common/chat/chatLib';

/** Build only the canonical user row; runtime/receipt ownership stays in the shared store. */
export function buildAcceptedAcpUserMessage(input: {
  conversationId: string;
  messageId: string;
  text: string;
  artifactRefs: TMessage['artifact_refs'];
  turnIntent?: 'save-as-skill';
}): TMessage {
  return {
    id: input.messageId,
    msg_id: input.messageId,
    conversation_id: input.conversationId,
    type: 'text',
    position: 'right',
    status: 'finish',
    created_at: Date.now(),
    content: { content: input.text },
    artifact_refs: input.artifactRefs,
    ...(input.turnIntent ? { hidden: true, turn_intent: input.turnIntent } : {}),
  };
}
