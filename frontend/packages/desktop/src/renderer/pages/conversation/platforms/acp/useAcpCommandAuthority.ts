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
  type XScienceSessionMemoryControl,
} from '@/renderer/services/xScienceSessionOptions';
import type { CommandExecutionAuthority } from './acpCommandExecutionTypes';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import type { SpecialistSendAuthority } from '@/common/adapter/specialistBinding';

/** Shared existing branch/session-option capture for ordinary and typed sends. */
export function useAcpCommandAuthority(
  conversation_id: string,
  xscienceSessionOptions: XScienceSessionOptions,
  stagedSessionOptionsRef: RefObject<StagedSessionOptions | null>,
  stagedPlanModeRef: RefObject<boolean>,
  delegationControl?: XScienceDelegationControl,
  specialistControl?: XScienceSpecialistControl,
  sessionMemory?: XScienceSessionMemoryControl,
  sessionAutoReview?: XScienceSessionMemoryControl
) {
  return useCallback(
    async (
      planMode?: boolean,
      includeStaged = true,
      requireBranch = false,
      capturedSpecialist?: SpecialistSendAuthority
    ): Promise<CommandExecutionAuthority> => {
      if (includeStaged && sessionMemory && !sessionMemory.current()) throw new Error('session_memory_owner_changed');
      if (includeStaged && sessionAutoReview && !sessionAutoReview.current())
        throw new Error('session_review_owner_changed');
      const selectedBranchId = getSelectedXScienceBranch(conversation_id);
      // Capture before branch loading yields. Exact retries reuse the captured authority.
      const stagedMemory = includeStaged ? stagedSessionOptionsRef.current?.memory : undefined;
      const memory = includeStaged && sessionMemory ? (stagedMemory ?? sessionMemory.capture()) : undefined;
      const review =
        includeStaged && sessionAutoReview
          ? (stagedSessionOptionsRef.current?.autoReview ?? sessionAutoReview.capture())
          : undefined;
      const specialist = includeStaged
        ? (capturedSpecialist ?? specialistControl?.capture(selectedBranchId))
        : undefined;
      const targetBranchRevision = getXScienceBranchSelectionRevision(conversation_id);
      const branchState =
        selectedBranchId || requireBranch ? await loadXScienceConversationBranches(conversation_id) : null;
      const targetBranchId = selectedBranchId ?? (requireBranch ? (branchState?.activeBranchId ?? null) : null);
      if (includeStaged && sessionMemory && !sessionMemory.current()) throw new Error('session_memory_owner_changed');
      if (includeStaged && sessionAutoReview && !sessionAutoReview.current())
        throw new Error('session_review_owner_changed');
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
              ...(typeof stagedSessionOptionsRef.current.autoReview === 'boolean'
                ? { autoReview: stagedSessionOptionsRef.current.autoReview }
                : {}),
              ...(stagedSessionOptionsRef.current.memory === undefined
                ? {}
                : { memory: stagedSessionOptionsRef.current.memory }),
            }
          : xscienceSessionOptions;
      return {
        targetBranchId,
        targetBranchRevision,
        sessionOptions: toXScienceMessageSessionOptions(effectiveSessionOptions, {
          ...(memory === undefined ? {} : { memory }),
          ...(review === undefined ? {} : { autoReview: review }),
          ...(delegationControl
            ? { delegation: delegationControl.current() ? (delegationControl.enabled ?? null) : null }
            : {}),
          planMode: planMode ?? (includeStaged && stagedPlanModeRef.current ? true : undefined),
          targetBranchId,
          expectedBranchId: branchState?.activeBranchId,
          expectedGeneration: branchState?.generation,
          ...(specialistControl || capturedSpecialist ? { targetAgent: specialist?.targetAgent ?? null } : {}),
          ...(specialist?.reference ? { specialistBinding: specialist.reference } : {}),
        }),
      };
    },
    [
      conversation_id,
      xscienceSessionOptions,
      stagedSessionOptionsRef,
      stagedPlanModeRef,
      delegationControl,
      specialistControl,
      sessionMemory,
      sessionAutoReview,
    ]
  );
}
