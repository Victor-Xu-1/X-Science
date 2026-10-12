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
