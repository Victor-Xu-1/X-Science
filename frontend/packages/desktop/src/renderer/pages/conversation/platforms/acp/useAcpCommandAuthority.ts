import { useCallback, type RefObject } from 'react';
import type { StagedSessionOptions } from '@/renderer/hooks/chat/sendBoxDraftPersistence';
import {
  getSelectedXScienceBranch,
  getXScienceBranchSelectionRevision,
  loadXScienceConversationBranches,
} from '@/renderer/services/xScienceConversationBranches';
import {
  toXScienceMessageSessionOptions,
  type XScienceSessionOptions,
} from '@/renderer/services/xScienceSessionOptions';
import type { CommandExecutionAuthority } from './acpCommandExecutionTypes';

/** Shared existing branch/session-option capture for ordinary and typed sends. */
export function useAcpCommandAuthority(
  conversation_id: string,
  xscienceSessionOptions: XScienceSessionOptions,
  stagedSessionOptionsRef: RefObject<StagedSessionOptions | null>,
  stagedPlanModeRef: RefObject<boolean>
) {
  return useCallback(
    async (planMode?: boolean, includeStaged = true, requireBranch = false): Promise<CommandExecutionAuthority> => {
      const selectedBranchId = getSelectedXScienceBranch(conversation_id);
      const targetBranchRevision = getXScienceBranchSelectionRevision(conversation_id);
      const branchState =
        selectedBranchId || requireBranch ? await loadXScienceConversationBranches(conversation_id) : null;
      const targetBranchId = selectedBranchId ?? (requireBranch ? (branchState?.activeBranchId ?? null) : null);
      if (
        (selectedBranchId || requireBranch) &&
        (getXScienceBranchSelectionRevision(conversation_id) !== targetBranchRevision ||
          getSelectedXScienceBranch(conversation_id) !== selectedBranchId ||
          !targetBranchId ||
          !branchState?.branches.some((branch) => branch.id === targetBranchId))
      ) {
        throw new Error('selected_branch_changed');
      }
      // A staged draft carries its requesting page's session options through
      // the durable draft; they win over the async runtime load for the first
      // send, while an explicit per-send choice (plan mode) always wins.
      const effectiveSessionOptions =
        includeStaged && stagedSessionOptionsRef.current
          ? {
              ...xscienceSessionOptions,
              delegation: stagedSessionOptionsRef.current.delegation,
              autoReview: stagedSessionOptionsRef.current.autoReview,
              memory: stagedSessionOptionsRef.current.memory,
              targetAgent: stagedSessionOptionsRef.current.targetAgent.trim() || xscienceSessionOptions.targetAgent,
            }
          : xscienceSessionOptions;
      return {
        targetBranchId,
        targetBranchRevision,
        sessionOptions: toXScienceMessageSessionOptions(effectiveSessionOptions, {
          planMode: planMode ?? (includeStaged && stagedPlanModeRef.current ? true : undefined),
          targetBranchId,
          expectedBranchId: branchState?.activeBranchId,
          expectedGeneration: branchState?.generation,
        }),
      };
    },
    [conversation_id, xscienceSessionOptions, stagedSessionOptionsRef, stagedPlanModeRef]
  );
}
