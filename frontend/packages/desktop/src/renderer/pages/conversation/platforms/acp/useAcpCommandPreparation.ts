import { useCallback, useEffect, useMemo, type RefObject } from 'react';
import type { TFunction } from 'i18next';
import type { ContextCompactionController } from '@/renderer/services/compaction/ContextCompactionController';
import type { useConversationRuntimeView } from '../../runtime/useConversationRuntimeView';
import type { ConversationCommandQueueItem } from '../useConversationCommandQueue';
import type { useAcpCommandAuthority } from './useAcpCommandAuthority';
import type { CommandExecutionAuthority, DirectCommandRetry } from './acpCommandExecutionTypes';
import { ConversationPreparationCancelledError } from '@/common/chat/conversationSubmission';

type Options = {
  compaction: ContextCompactionController | null;
  authority: RefObject<{ conversationId: string; ownerId: string }>;
  mounted: RefObject<boolean>;
  runtime: ReturnType<typeof useConversationRuntimeView>;
  resolveAuthority: ReturnType<typeof useAcpCommandAuthority>;
  retry: RefObject<DirectCommandRetry | null>;
  t: TFunction;
};

/** Await existing preparation under the original runtime ticket, without owning requests or execution. */
export function useAcpCommandPreparation({
  compaction,
  authority,
  mounted,
  runtime,
  resolveAuthority,
  retry,
  t,
}: Options) {
  const scope = authority.current;
  // Retain only this mounted scope's exact ticket. The runtime store remains
  // its authority; navigation must never withdraw an admitted backend run or
  // another composer's newer preparation in the same conversation.
  const preparation = useMemo(() => ({ ticket: null as object | null }), [scope]);
  useEffect(
    () => () => {
      const ticket = preparation.ticket;
      preparation.ticket = null;
      if (ticket && runtime.isSendPreparing(ticket)) runtime.cancelSendPreparation(ticket);
    },
    [preparation, runtime.isSendPreparing, runtime.cancelSendPreparation]
  );
  return useCallback(
    async (
      command: Pick<ConversationCommandQueueItem, 'id' | 'specialistAuthority'> & {
        planMode?: boolean;
        turnIntent?: 'save-as-skill';
      },
      prepared: CommandExecutionAuthority | undefined,
      ticket: object
    ) => {
      const origin = authority.current;
      const checkCurrent = () => {
        if (!mounted.current || authority.current !== origin || !runtime.isSendPreparing(ticket)) {
          runtime.cancelSendPreparation(ticket);
          throw new ConversationPreparationCancelledError();
        }
      };
      try {
        checkCurrent();
        preparation.ticket = ticket;
        if (compaction) {
          const admitted = await runtime.awaitSendPreparation(ticket, compaction.prepareForPrompt());
          checkCurrent();
          if (!compaction.current()) {
            runtime.cancelSendPreparation(ticket);
            throw new ConversationPreparationCancelledError();
          }
          if (!admitted) throw new Error(t('conversation.manualCompaction.waitingForPrompt'));
        }
        const result =
          prepared ??
          (await runtime.awaitSendPreparation(
            ticket,
            resolveAuthority(command.planMode, !command.turnIntent, false, command.specialistAuthority ?? {})
          ));
        checkCurrent();
        const pending = retry.current;
        if (pending?.item.id === command.id && pending.authority === undefined)
          retry.current = { ...pending, authority: result };
        return result;
      } catch (error) {
        checkCurrent();
        throw error;
      }
    },
    [authority, compaction, mounted, preparation, resolveAuthority, retry, runtime, t]
  );
}
