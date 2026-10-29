import { useCallback, type RefObject } from 'react';
import type { TFunction } from 'i18next';
import type { ContextCompactionController } from '@/renderer/services/compaction/ContextCompactionController';
import type { useConversationRuntimeView } from '../../runtime/useConversationRuntimeView';
import type { ConversationCommandQueueItem } from '../useConversationCommandQueue';
import type { useAcpCommandAuthority } from './useAcpCommandAuthority';
import {
  AcpPreparationCancelledError,
  type CommandExecutionAuthority,
  type DirectCommandRetry,
} from './acpCommandExecutionTypes';

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
          throw new AcpPreparationCancelledError();
        }
      };
      try {
        checkCurrent();
        if (compaction) {
          const admitted = await compaction.prepareForPrompt();
          checkCurrent();
          if (!compaction.current()) {
            runtime.cancelSendPreparation(ticket);
            throw new AcpPreparationCancelledError();
          }
          if (!admitted) throw new Error(t('conversation.manualCompaction.waitingForPrompt'));
        }
        const result =
          prepared ??
          (await resolveAuthority(command.planMode, !command.turnIntent, false, command.specialistAuthority ?? {}));
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
    [authority, compaction, mounted, resolveAuthority, retry, runtime, t]
  );
}
