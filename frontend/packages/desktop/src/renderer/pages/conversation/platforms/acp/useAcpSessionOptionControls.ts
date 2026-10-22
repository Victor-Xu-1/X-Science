import { useCallback, useEffect, useState, type Dispatch, type RefObject, type SetStateAction } from 'react';
import { Message } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import type { TChatConversation } from '@/common/config/storage';
import {
  retireStagedMemory as withoutStagedMemory,
  retireStagedAutoReview,
  type StagedSessionOptions,
} from '@/renderer/hooks/chat/sendBoxDraftPersistence';
import { getRendererAccountOwnerId } from '@/renderer/services/rendererAccountScope';
import { useXScienceMemoryAvailability } from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import { useSessionDelegationPolicy } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import { useSessionSpecialist } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import {
  updateXScienceSessionConfig,
  type XScienceSessionOptions,
  type XScienceSessionMemoryControl,
  type XScienceSessionPreferenceControl,
} from '@/renderer/services/xScienceSessionOptions';
import { safeErrorDiagnostic } from './acpSendBoxDiagnostics';
import { useXScienceSessionOptionsState } from './useXScienceSessionOptionsState';

type ConversationOwner = { conversationId: string; ownerId: string };

type ComposerOptionsDraft<TRetry> = {
  stagedSessionOptionsRef: RefObject<StagedSessionOptions | null>;
  setStagedSessionOptions: (value: StagedSessionOptions) => void;
  directCommandRetryRef: RefObject<TRetry | null>;
};

/** Composer glue only: the existing session hook still owns all preference state and requests. */
export function useAcpComposerSessionOptions<TRetry>(
  conversation_id: string,
  agent_name: string | undefined,
  initialConversation: TChatConversation | undefined,
  ownerId: string,
  frameIncarnationId: string | undefined,
  { stagedSessionOptionsRef, setStagedSessionOptions, directCommandRetryRef }: ComposerOptionsDraft<TRetry>
) {
  const frameProjection =
    initialConversation?.type === 'acp' && initialConversation.id === conversation_id
      ? initialConversation.extra
      : undefined;
  const projectedMemoryRoot =
    ownerId === getRendererAccountOwnerId() && frameIncarnationId ? frameProjection?.root_frame_id : undefined;
  const retireStagedMemory = useCallback(() => {
    // A user mutation is new intent even if readback retains the same value.
    // Background observations must not retire the captured retry authority.
    directCommandRetryRef.current = null;
    const staged = stagedSessionOptionsRef.current;
    if (!staged || staged.memory === undefined) return;
    const remaining = withoutStagedMemory(staged);
    stagedSessionOptionsRef.current = remaining;
    setStagedSessionOptions(remaining);
  }, [setStagedSessionOptions]);
  const retireReviewIntent = useCallback(() => {
    directCommandRetryRef.current = null;
    const staged = stagedSessionOptionsRef.current;
    if (!staged || staged.autoReview === undefined) return;
    const remaining = retireStagedAutoReview(staged);
    stagedSessionOptionsRef.current = remaining;
    setStagedSessionOptions(remaining);
  }, [setStagedSessionOptions]);
  const state = useXScienceSessionOptionsState(conversation_id, agent_name, {
    ownerId,
    frameIncarnationId,
    expectedRootFrameId: projectedMemoryRoot,
    projectId: projectedMemoryRoot ? frameProjection?.project_id : undefined,
    onMutationStarted: retireStagedMemory,
    onReviewMutationStarted: retireReviewIntent,
  });
  const { setOptions: setXScienceSessionOptions, loadState: sessionOptionsLoad } = state;
  const applyStagedSessionOptions = useCallback(() => {
    const staged = stagedSessionOptionsRef.current;
    if (!staged) return;
    setXScienceSessionOptions((current) => ({
      ...current,
      delegation: staged.delegation,
      ...(typeof staged.autoReview === 'boolean' ? { autoReview: staged.autoReview } : {}),
    }));
  }, [setXScienceSessionOptions]);
  // The runtime session-options load resolves asynchronously for a fresh
  // conversation; replay the staged selection once it settles so the loaded
  // defaults cannot silently replace the requesting page's choices.
  useEffect(() => {
    if (sessionOptionsLoad.conversationId !== conversation_id) return;
    if (sessionOptionsLoad.status === 'ready' || sessionOptionsLoad.status === 'retrying') {
      applyStagedSessionOptions();
    }
  }, [applyStagedSessionOptions, sessionOptionsLoad]);
  return { ...state, applyStagedSessionOptions };
}

/** Reuses the existing composer mutation owner; desktop/mobile share its one capability read. */
export function useAcpSessionOptionControls(
  conversationId: string,
  conversation: TChatConversation | undefined,
  authority: RefObject<ConversationOwner>,
  mounted: RefObject<boolean>,
  setOptions: Dispatch<SetStateAction<XScienceSessionOptions>>,
  setMobileOpen: (open: boolean) => void,
  sessionMemory?: XScienceSessionMemoryControl,
  stagedMemory?: boolean,
  sessionAutoReview?: XScienceSessionPreferenceControl,
  mobilePlusTrigger?: RefObject<HTMLButtonElement | null>
) {
  const ownerId = authority.current.ownerId;
  const extra = conversation?.type === 'acp' && conversation.id === conversationId ? conversation.extra : undefined;
  const availabilityControl = useXScienceMemoryAvailability(
    JSON.stringify([ownerId, conversationId, extra?.frame_incarnation_id])
  );
  const refreshGlobalMemory = availabilityControl.refresh;
  const refreshSessionMemory = sessionMemory?.refresh;
  const refreshMemory = useCallback(() => {
    refreshGlobalMemory();
    refreshSessionMemory?.();
  }, [refreshGlobalMemory, refreshSessionMemory]);
  const memoryControl = {
    ...availabilityControl,
    preference: sessionMemory,
    stagedMemory,
    refresh: refreshMemory,
    canUpdate: () =>
      availabilityControl.canUpdate() &&
      (!sessionMemory || (sessionMemory.current() && sessionMemory.status === 'ready')),
  };
  const delegationControl = useSessionDelegationPolicy(ownerId, conversationId, extra);
  const specialistControl = useSessionSpecialist(ownerId, conversationId, extra);
  const { t } = useTranslation();
  const [goalClearInFlight, setGoalClearInFlight] = useState(false);
  useEffect(() => setGoalClearInFlight(false), [conversationId]);
  const updatePersistentSessionOption = useCallback(
    async (key: 'autoReview' | 'memory', value: boolean) => {
      const requestOwner = authority.current;
      if (key === 'autoReview' && sessionAutoReview) return sessionAutoReview.update(value);
      if (key === 'memory' && !memoryControl.canUpdate()) return;
      if (key === 'memory' && sessionMemory) return sessionMemory.update(value);
      try {
        await updateXScienceSessionConfig(conversationId, { [key]: value });
      } catch (error) {
        if (!mounted.current || authority.current !== requestOwner || !memoryControl.current()) return;
        throw error;
      }
      if (!mounted.current || authority.current !== requestOwner || !memoryControl.current()) return;
      setOptions((current) => ({ ...current, [key]: value }));
    },
    [authority, conversationId, memoryControl, mounted, setOptions, sessionMemory, sessionAutoReview]
  );
  const openMobileControls = (trigger?: HTMLButtonElement) => {
    if (mobilePlusTrigger)
      mobilePlusTrigger.current = trigger instanceof HTMLButtonElement && trigger.isConnected ? trigger : null;
    memoryControl.refresh();
    sessionAutoReview?.refresh();
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
    specialistControl,
    updatePersistentSessionOption,
    openMobileControls,
    handleClearGoal,
    goalClearInFlight,
  };
}
