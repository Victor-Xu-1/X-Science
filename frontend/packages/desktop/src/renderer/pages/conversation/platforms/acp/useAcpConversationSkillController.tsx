import React, { useCallback, useEffect, useRef, type RefObject } from 'react';
import { useTranslation } from 'react-i18next';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import type { TMessage } from '@/common/chat/chatLib';
import { parseError } from '@/common/utils';
import type { ConversationCommandQueueItem } from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import type { useConversationRuntimeView } from '@/renderer/pages/conversation/runtime/useConversationRuntimeView';
import type { CommandExecutionAuthority } from './acpCommandExecutionTypes';
import { ConversationSkillActivationDialog } from './ConversationSkillActivationDialog';
import { ConversationSkillTaskStatus } from './ConversationSkillTaskStatus';
import { redactErrorText } from './errorDiagnostics';
import { useAcpConversationSkills } from './useAcpConversationSkills';
import type { UseAcpMessageReturn } from './useAcpMessage';
import { isActiveConversationSkillTask, useConversationSkillTask } from './useConversationSkillTask';

type SkillCommand = Pick<ConversationCommandQueueItem, 'id' | 'input' | 'files' | 'contextItems'> & {
  planMode?: boolean;
  turnIntent?: 'save-as-skill';
};

type Options = Omit<Parameters<typeof useAcpConversationSkills>[0], 'authority' | 'conversationId' | 'ownerId'> & {
  conversationAuthorityRef: RefObject<{ conversationId: string; ownerId: string }>;
  runtimeView: ReturnType<typeof useConversationRuntimeView>;
  setAiProcessing: UseAcpMessageReturn['setAiProcessing'];
  resolveCommandExecutionAuthority: (
    planMode?: boolean,
    includeStaged?: boolean,
    requireBranch?: boolean
  ) => Promise<CommandExecutionAuthority>;
  executeCommand: (
    command: SkillCommand,
    preparedAuthority?: CommandExecutionAuthority,
    submissionTicket?: object
  ) => Promise<void>;
  messages: readonly TMessage[];
  saveDisabled: boolean;
};

type SkillTaskCommand = {
  scope: object;
  id: string;
  input: string;
  authority?: CommandExecutionAuthority;
  submissionTicket?: object;
  dispatched: boolean;
};

// Compose the existing Skill admission/catalog hooks with the ordinary command
// sender. Execution and cancellation remain owned by the shared runtime gate.
export function useAcpConversationSkillController(options: Options) {
  const {
    conversationAuthorityRef,
    mountedRef,
    runtimeView,
    setAiProcessing,
    resolveCommandExecutionAuthority,
    executeCommand,
    messages,
  } = options;
  const { t } = useTranslation();
  const skillTaskScope = conversationAuthorityRef.current;
  const { refreshCatalogs, skillActivation, mobileLibraryNames } = useAcpConversationSkills({
    conversationId: skillTaskScope.conversationId,
    ownerId: skillTaskScope.ownerId,
    authority: skillTaskScope,
    mountedRef,
    disabled: options.disabled,
    isXScience: options.isXScience,
    isMobile: options.isMobile,
    assistantId: options.assistantId,
    runtimeAgentName: options.runtimeAgentName,
    locale: options.locale,
    loadedSkills: options.loadedSkills,
    mutateComposerCapabilities: options.mutateComposerCapabilities,
    fetchSlashCommands: options.fetchSlashCommands,
    onSelectSkill: options.onSelectSkill,
  });
  const skillTaskCommandRef = useRef<SkillTaskCommand | null>(null);
  useEffect(
    () => () => {
      const command = skillTaskCommandRef.current;
      if (command?.scope === skillTaskScope && !command.dispatched && command.submissionTicket) {
        runtimeView.markSendFailed('control_submission_cancelled', command.submissionTicket);
      }
    },
    [runtimeView.markSendFailed, skillTaskScope]
  );
  const startSkillTask = useCallback(
    async (id: string) => {
      const scope = conversationAuthorityRef.current;
      let command = skillTaskCommandRef.current;
      if (!command || command.id !== id || command.scope !== scope) {
        command = { scope, id, input: t('conversation.attachMenu.saveAsSkillTaskPrompt'), dispatched: false };
        skillTaskCommandRef.current = command;
      }
      // Reserve before the first await. The shared runtime gate owns sending;
      // the Skill hook's synchronous admission latch guards same-tick UI calls.
      const submissionTicket = runtimeView.markSendStarted();
      command.submissionTicket = submissionTicket;
      command.dispatched = false;
      setAiProcessing(true);
      // A lost HTTP reply is retried with the same input and branch authority,
      // even after a language switch. Never rebind one mutation to a newer draft.
      try {
        command.authority ??= await resolveCommandExecutionAuthority(false, false, true);
      } catch (error) {
        runtimeView.markSendFailed(redactErrorText(parseError(error) || t('common.unknownError')), submissionTicket);
        if (mountedRef.current && conversationAuthorityRef.current === scope) {
          setAiProcessing(false);
        }
        throw error;
      }
      if (!mountedRef.current || conversationAuthorityRef.current !== scope) {
        runtimeView.markSendFailed('control_submission_cancelled', submissionTicket);
        return;
      }
      command.dispatched = true;
      try {
        await executeCommand(
          { id, input: command.input, files: [], contextItems: [], turnIntent: 'save-as-skill', planMode: false },
          command.authority,
          submissionTicket
        );
      } catch (error) {
        if (
          mountedRef.current &&
          conversationAuthorityRef.current === scope &&
          isBackendHttpError(error) &&
          error.status === 409 &&
          error.code === 'CONVERSATION_BRANCH_STATE_STALE'
        ) {
          // A definite rejected CAS has no admitted command to replay. Refresh
          // authority on the next explicit retry, retaining input and mutation.
          // Lost/uncertain replies keep the original complete payload instead.
          command.authority = undefined;
        }
        throw error;
      }
    },
    [
      conversationAuthorityRef,
      executeCommand,
      mountedRef,
      resolveCommandExecutionAuthority,
      runtimeView,
      setAiProcessing,
      t,
    ]
  );
  const skillSave = useConversationSkillTask({
    authority: skillTaskScope,
    ownerId: skillTaskScope.ownerId,
    mountedRef,
    disabled: options.disabled || options.saveDisabled || !options.isXScience,
    isProcessing: runtimeView.isProcessing,
    activeControlTask:
      runtimeView.view.hasTask && isActiveConversationSkillTask(messages, runtimeView.view.activeInputMessageId),
    startTask: startSkillTask,
    refreshCatalogs,
  });
  const skillSelectionProps = {
    loadedSkills: options.loadedSkills,
    onSelectSkill: options.onSelectSkill,
    onEnableSkill: skillActivation.canActivate ? skillActivation.request : undefined,
  };
  return {
    skillSave,
    skillMenuProps: {
      ...skillSelectionProps,
      onSaveSkill: skillSave.save,
      saveAsSkillDisabled: skillSave.saveDisabled,
      saveAsSkillInFlight: skillSave.inFlight,
    },
    mobileSkillProps: { ...skillSelectionProps, librarySkillNames: mobileLibraryNames },
    skillSurfaces: (
      <>
        <ConversationSkillTaskStatus
          state={skillSave.state}
          disabled={skillSave.saveDisabled}
          onRetry={skillSave.save}
          onRetryRefresh={skillSave.retryRefresh}
        />
        <ConversationSkillActivationDialog
          state={skillActivation.state}
          onConfirm={skillActivation.confirm}
          onCancel={skillActivation.cancel}
        />
      </>
    ),
  };
}
