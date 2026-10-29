import type { ConversationCommandQueueItem } from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import type { toXScienceMessageSessionOptions } from '@/renderer/services/xScienceSessionOptions';

export type CommandExecutionAuthority = {
  targetBranchId: string | null;
  targetBranchRevision: number;
  sessionOptions: ReturnType<typeof toXScienceMessageSessionOptions>;
};

export type DirectCommandRetry = {
  signature: string;
  item: ConversationCommandQueueItem;
  authority?: CommandExecutionAuthority;
};

/** An owned preparation was withdrawn before transport, never an admitted execution failure. */
export class AcpPreparationCancelledError extends Error {
  constructor() {
    super('conversation_preparation_cancelled');
    this.name = 'AcpPreparationCancelledError';
  }
}
