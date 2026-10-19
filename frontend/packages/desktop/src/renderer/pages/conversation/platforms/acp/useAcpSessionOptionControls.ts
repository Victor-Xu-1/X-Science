import { useCallback, useEffect, useState, type Dispatch, type RefObject, type SetStateAction } from 'react';
import { Message } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import type { TChatConversation } from '@/common/config/storage';
import { useXScienceMemoryAvailability } from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import { useSessionDelegationPolicy } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import { updateXScienceSessionConfig, type XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import { safeErrorDiagnostic } from './acpSendBoxDiagnostics';

type ConversationOwner = { conversationId: string; ownerId: string };

/** Reuses the existing composer mutation owner; desktop/mobile share its one capability read. */
export function useAcpSessionOptionControls(
  conversationId: string,
  conversation: TChatConversation | undefined,
  authority: RefObject<ConversationOwner>,
  mounted: RefObject<boolean>,
  setOptions: Dispatch<SetStateAction<XScienceSessionOptions>>,
  setMobileOpen: (open: boolean) => void
) {
  const ownerId = authority.current.ownerId;
  const extra = conversation?.type === 'acp' && conversation.id === conversationId ? conversation.extra : undefined;
  const memoryControl = useXScienceMemoryAvailability(
    JSON.stringify([ownerId, conversationId, extra?.frame_incarnation_id])
  );
  const delegationControl = useSessionDelegationPolicy(ownerId, conversationId, extra);
  const { t } = useTranslation();
  const [goalClearInFlight, setGoalClearInFlight] = useState(false);
  useEffect(() => setGoalClearInFlight(false), [conversationId]);
  const updatePersistentSessionOption = useCallback(
    async (key: 'autoReview' | 'memory', value: boolean) => {
      const requestOwner = authority.current;
      if (key === 'memory' && !memoryControl.canUpdate()) return;
      try {
        await updateXScienceSessionConfig(conversationId, { [key]: value });
      } catch (error) {
        if (!mounted.current || authority.current !== requestOwner || !memoryControl.current()) return;
        throw error;
      }
      if (!mounted.current || authority.current !== requestOwner || !memoryControl.current()) return;
      setOptions((current) => ({ ...current, [key]: value }));
    },
    [authority, conversationId, memoryControl, mounted, setOptions]
  );
  const openMobileControls = () => {
    memoryControl.refresh();
    setMobileOpen(true);
  };
  const handleClearGoal = useCallback(async () => {
    if (goalClearInFlight) return;
    const requestAuthority = authority.current;
    setGoalClearInFlight(true);
    try {
      await updateXScienceSessionConfig(conversationId, { goalText: null });
      if (!mounted.current || authority.current !== requestAuthority) return;
      setOptions((current) => ({ ...current, goalText: null }));
      Message.success(t('conversation.xscienceRuntime.sendBox.goalCleared'));
    } catch (error) {
      if (!mounted.current || authority.current !== requestAuthority) return;
      console.error('[AcpSendBox] Failed to clear conversation goal', safeErrorDiagnostic(error));
      Message.error(t('conversation.xscienceRuntime.sendBox.goalClearFailed'));
    } finally {
      if (mounted.current && authority.current === requestAuthority) setGoalClearInFlight(false);
    }
  }, [authority, conversationId, goalClearInFlight, mounted, setOptions, t]);
  return {
    memoryControl,
    delegationControl,
    updatePersistentSessionOption,
    openMobileControls,
    handleClearGoal,
    goalClearInFlight,
  };
}
