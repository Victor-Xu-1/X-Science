import { httpRequest } from '@/common/adapter/httpBridge';
import { loadSynonBiomedConversationBranches } from './synonBiomedConversationBranches';

export type ReplyBranchInput = {
  conversationId: string;
  throughAttempt: number;
  sourceBranchId?: string | null;
  intentId: string;
};

/** Copy a durable completed prefix; never submit another prompt or start a runner. */
export async function branchSynonBiomedReply(input: ReplyBranchInput): Promise<string> {
  if (!input.conversationId.trim() || !Number.isSafeInteger(input.throughAttempt) || input.throughAttempt < 1)
    throw new Error('Invalid reply boundary');
  const branches = await loadSynonBiomedConversationBranches(input.conversationId);
  const branchId = input.sourceBranchId || branches.selectedBranchId || branches.activeBranchId;
  if (!branchId || !branches.branches.some((branch) => branch.id === branchId))
    throw new Error('Source branch changed');
  const result = await httpRequest<{ id?: unknown }>(
    'POST',
    '/api/conversations/clone',
    {
      conversation: { id: input.conversationId },
      intent_id: input.intentId,
      through_attempt: input.throughAttempt,
      source_branch_id: branchId,
    },
    { timeoutMs: 120000 }
  );
  if (typeof result?.id !== 'string' || !/^[0-9a-f-]{36}$/i.test(result.id) || result.id === input.conversationId)
    throw new Error('Invalid new conversation identity');
  return result.id;
}
