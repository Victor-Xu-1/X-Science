import { ipcBridge } from '@/common';
import type { IConversationMcpStatus, TChatConversation } from '@/common/config/storage';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import { parseError, resolveLocaleKey, uuid } from '@/common/utils';
import XScienceModelSelector from '@/renderer/components/xScience/runtime/XScienceModelSelector';
import { useAcpSessionPermissionGrants } from './useAcpSessionPermissionGrants';
import XScienceSendOptionsMenu, {
  type XScienceSendIntent,
} from '@/renderer/components/xScience/runtime/XScienceSendOptionsMenu';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import ContextUsagePanel from '@/renderer/components/xScience/runtime/ContextUsagePanel';
import OptimizePromptAction from '@/renderer/components/xScience/runtime/OptimizePromptAction';
import XScienceRuntimeOperations from '@/renderer/components/xScience/runtime/XScienceRuntimeOperations';
import CommandQueuePanel from '@/renderer/components/chat/CommandQueuePanel';
import BtwOverlay from '@/renderer/components/chat/BtwOverlay';
import { useAttachEntry } from '@/renderer/components/chat/MobileActionSheet';
import SendBox from '@/renderer/components/chat/SendBox';
import { AcpComposerAttachments } from './AcpComposerAttachments';
import { PdfReadingComposerPrefix } from '@/renderer/components/chat/SendBox/reading/PdfReadingComposerPrefix';
import { usePdfReadingComposer } from '@/renderer/hooks/chat/usePdfReadingComposer';
import { usePdfReadingAttachments } from '@/renderer/hooks/chat/usePdfReadingAttachments';
import { buildAcceptedAcpUserMessage } from './acpAcceptedMessage';
import { buildComposerCapabilityPayload } from '@/renderer/components/chat/SendBox/composerCompositionModel';
import ThoughtDisplay from '@/renderer/components/chat/ThoughtDisplay';
import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import GuidProjectFilesModal from '@/renderer/pages/guid/components/GuidProjectFilesModal';
import { loadXScienceComposerCapabilities } from '@/renderer/services/xScienceGateway';
import { useAcpConfigOptions } from '@/renderer/hooks/xScience/runtime/useAcpConfigOptions';
import { useAcpModelInfo } from '@/renderer/hooks/xScience/runtime/useAcpModelInfo';
import { useAutoTitle } from '@/renderer/hooks/chat/useAutoTitle';
import type { FileOrFolderItem } from '@/renderer/hooks/chat/useSendBoxDraft';
import { useSendBoxFiles } from '@/renderer/hooks/chat/useSendBoxFiles';
import { useConversationContextSafe } from '@/renderer/hooks/context/ConversationContext';
import { useLayoutContext } from '@/renderer/hooks/context/LayoutContext';
import { useOpenFileSelector } from '@/renderer/hooks/file/useOpenFileSelector';
import { useLatestRef } from '@/renderer/hooks/ui/useLatestRef';
import {
  useAddOrUpdateMessage,
  useRemoveMessageByMsgId,
  useUpdateMessageList,
  useMessageList,
} from '@/renderer/pages/conversation/Messages/hooks';
import { X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX } from '@/renderer/pages/conversation/Messages/optimisticUserMessage';
import {
  shouldEnqueueConversationCommand,
  createQueuedCommandItem,
  useConversationCommandQueue,
  type ConversationCommandQueueItem,
} from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import { usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { useConversationRuntimeView } from '@/renderer/pages/conversation/runtime/useConversationRuntimeView';
import type { TerminalFailureRollback } from '@/renderer/pages/conversation/runtime/terminalFailureMessagesModel';
import { CHAT_SURFACE_WIDTH_CLASS } from '@/renderer/pages/conversation/utils/chatSurfaceWidth';
import { allSupportedExts } from '@/renderer/services/FileService';
import { loadXScienceAssistants } from '@/renderer/services/xScienceCatalog';
import { requestXScienceFrameAudit, type XScienceVerificationCheck } from '@/renderer/services/xScienceAnnotations';
import {
  getSelectedXScienceBranch,
  getXScienceBranchSelectionRevision,
  selectXScienceBranch,
} from '@/renderer/services/xScienceConversationBranches';
import {
  loadXScienceComputeProviders,
  loadXScienceSessionComputeProviders,
  setXScienceSessionComputeProvider,
} from '@/renderer/services/xScienceCompute';
import { notifyXScienceRuntimeInvalidation } from '@/renderer/services/xScienceRuntimeOperations';
import { updateXScienceSessionConfig } from '@/renderer/services/xScienceSessionOptions';
import { resolveAssistantName } from '@/renderer/utils/model/assistantDisplay';
import { emitter, useAddEventListener } from '@/renderer/utils/emitter';
import { mergeFileSelectionItems } from '@/renderer/utils/file/fileSelection';
import { buildDisplayMessage } from '@/renderer/utils/file/messageFiles';
import { Message } from '@arco-design/web-react';
import { useNavigate } from 'react-router';
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import useSWR from 'swr';
import { buildSendFailureError } from './buildSendFailureError';
import { redactErrorText } from './errorDiagnostics';
import { buildXScienceReviewRepairPrompt } from '@/renderer/components/xScience/runtime/xScienceReviewRepair';
import { useAcpInitialMessage } from './useAcpInitialMessage';
import { applyStagedDraft, makeDraftRestoreIssueNotifier, type StagedDraftPayload } from './stagedDraftHandoff';
import type { UseAcpMessageReturn } from './useAcpMessage';
import { useAcpSendBoxDraftController } from './useAcpSendBoxDraftController';
import { useAcpMobileActionSheetController } from './useAcpMobileActionSheetController';
import { configErrorMessageKey, safeErrorDiagnostic } from './acpSendBoxDiagnostics';
import { useXScienceSessionOptionsState } from './useXScienceSessionOptionsState';
import { useAcpComposerContextSelection } from './useAcpComposerContextSelection';
import { useAcpTaskCenterMetrics } from './useAcpTaskCenterMetrics';
import { useAcpPlanPresentation } from './useAcpPlanPresentation';
import { useAcpAsideController, type AsideQueueBridge } from './useAcpAsideController';
import { useAcpConversationSkillController } from './useAcpConversationSkillController';
import { useAcpContextCompaction } from './useAcpContextCompaction';
import { stopAcpConversation } from './acpStopAction';
import { useAcpRuntimeActions } from './useAcpRuntimeActions';
import type { CommandExecutionAuthority, DirectCommandRetry } from './acpCommandExecutionTypes';
import { useAcpCommandAuthority } from './useAcpCommandAuthority';
import './conversationQueueDock.css';

const AcpSendBox: React.FC<{
  conversation_id: string;
  backend: string;
  session_mode?: string;
  agent_name?: string;
  workspacePath?: string;
  messageState: UseAcpMessageReturn;
  onRuntimeMutation?: () => void;
  initialConversation?: TChatConversation;
  ownerId?: string;
}> = ({
  conversation_id,
  backend,
  session_mode,
  agent_name,
  workspacePath,
  messageState,
  onRuntimeMutation,
  initialConversation,
  ownerId = '',
}) => {
  const { aiProcessing, setAiProcessing, resetState, streamReady, hasThinkingMessage, slashCommands } = messageState;
  const { t, i18n } = useTranslation();
  const localeKey = resolveLocaleKey(i18n.language);
  const navigate = useNavigate();
  const { checkAndUpdateTitle } = useAutoTitle(ownerId, initialConversation);
  const {
    atPath,
    uploadFile,
    setAtPath,
    setUploadFile,
    content,
    setContent,
    replaceContentIfUnchanged,
    contextItems,
    pdfReadingAttachmentsDisabled,
    keepPdfAttachments,
    queuedPdfSelection,
    queuedPdfInvalid,
    setQueuedPdfSelection,
    setContextItems,
    stagedPlanMode,
    setStagedPlanMode,
    stagedSessionOptions,
    setStagedSessionOptions,
    pdfReadingMutation,
    setPdfReadingMutation,
    draftSaveFailed,
    retryDraftSave,
  } = useAcpSendBoxDraftController(conversation_id, ownerId);
  // Staged plan mode and session options are durable in the draft; keep
  // synchronous views for the send path because the draft store resolves
  // asynchronously.
  const stagedPlanModeRef = useRef(stagedPlanMode);
  useEffect(() => {
    stagedPlanModeRef.current = stagedPlanMode;
  }, [stagedPlanMode]);
  const stagedSessionOptionsRef = useRef(stagedSessionOptions);
  useEffect(() => {
    stagedSessionOptionsRef.current = stagedSessionOptions;
  }, [stagedSessionOptions]);
  const layout = useLayoutContext();
  const isMobile = Boolean(layout?.isMobile);
  const [contextWindow, setContextWindow] = useState({ sequence: 0, returnFocus: null as HTMLButtonElement | null });
  const conversationContext = useConversationContextSafe();
  const { data: composerCapabilities, mutate: mutateComposerCapabilities } = useSWR(
    conversation_id ? ['x-science-composer-capabilities', conversation_id] : null,
    () => loadXScienceComposerCapabilities(conversation_id),
    { keepPreviousData: false }
  );
  const loadedSkills = composerCapabilities?.skills ?? conversationContext?.loadedSkills ?? [];
  const projectId = conversationContext?.projectId;
  const loadedMcpStatuses =
    composerCapabilities?.mcpStatuses ??
    conversationContext?.loadedMcpStatuses ??
    (conversationContext?.loadedMcpServers ?? []).map<IConversationMcpStatus>((name) => ({
      id: name,
      name,
      status: 'loaded',
    }));
  const isXScienceConversation =
    backend.trim().toLowerCase() === 'xscience' || workspacePath?.startsWith('x-science://') === true;
  const reading = usePdfReadingComposer({
    enabled: isXScienceConversation,
    ownerId,
    conversationId: conversation_id,
    frameIncarnationId:
      initialConversation?.type === 'acp' ? initialConversation.extra.frame_incarnation_id : undefined,
    pending: pdfReadingMutation,
    persistPending: setPdfReadingMutation,
  });
  const readingAttachments = usePdfReadingAttachments(
    reading.controller,
    contextItems,
    pdfReadingAttachmentsDisabled,
    uploadFile.length > 0 || atPath.length > 0
  );
  const compaction = useAcpContextCompaction(isXScienceConversation, ownerId, conversation_id, initialConversation);
  const { grantsController, setIsMobileSheetOpen, renderMobileSheet } = useAcpSessionPermissionGrants(
    conversation_id,
    ownerId,
    initialConversation,
    isXScienceConversation
  );
  const [currentMode, setCurrentMode] = useState<string | undefined>(session_mode);
  const {
    options: xscienceSessionOptions,
    setOptions: setXScienceSessionOptions,
    loadState: sessionOptionsLoad,
  } = useXScienceSessionOptionsState(conversation_id, agent_name);
  const applyStagedSessionOptions = useCallback(() => {
    const staged = stagedSessionOptionsRef.current;
    if (!staged) return;
    setXScienceSessionOptions((current) => ({
      ...current,
      delegation: staged.delegation,
      autoReview: staged.autoReview,
      memory: staged.memory,
      targetAgent: staged.targetAgent.trim() || current.targetAgent,
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
  const [mobileSpecialists, setMobileSpecialists] = useState<Array<{ id: string; label: string; agentId: string }>>([]);
  const [mobileComputeProviders, setMobileComputeProviders] = useState<Array<{ name: string; label: string }>>([]);
  const [mobileEnabledCompute, setMobileEnabledCompute] = useState<string[]>([]);
  const [reviewInFlight, setReviewInFlight] = useState(false);
  const [reviewActive, setReviewActive] = useState(false);
  const [goalClearInFlight, setGoalClearInFlight] = useState(false);
  const {
    canSelectProjectFiles,
    handleArtifactReferenceSelected,
    handleLocalFilesSelected,
    handleMcpSelected,
    handleProjectArtifactsSelected,
    handleSkillSelected,
    openProjectFiles,
    projectFilesOpen,
    selectedMcpServerIds,
    selectedSkillNames,
    setProjectFilesOpen,
  } = useAcpComposerContextSelection(projectId, contextItems, setContextItems, `${ownerId}:${conversation_id}`);
  const sendBoxAnchorRef = useRef<HTMLDivElement>(null);
  const mountedRef = useRef(true);
  const directCommandRetryRef = useRef<DirectCommandRetry | null>(null);
  const conversationAuthorityRef = useRef({ conversationId: conversation_id, ownerId });
  const sendIntentRef = useRef<XScienceSendIntent | null>(null);
  const terminalFailureRollbackRef = useRef<TerminalFailureRollback>([]);
  if (
    conversationAuthorityRef.current.conversationId !== conversation_id ||
    conversationAuthorityRef.current.ownerId !== ownerId
  ) {
    conversationAuthorityRef.current = { conversationId: conversation_id, ownerId };
    directCommandRetryRef.current = null;
    terminalFailureRollbackRef.current = [];
  }
  const sideQuestionSupported = backend.trim().toLowerCase() === 'xscience';
  const mobileModeChangeInFlightRef = useRef(false);
  const frameIncarnationId =
    initialConversation?.type === 'acp' ? initialConversation.extra.frame_incarnation_id : undefined;
  const planPresentation = useAcpPlanPresentation(ownerId, conversation_id, frameIncarnationId);
  const runtimeConfig = useAcpConfigOptions({
    conversation_id,
    frameIncarnationId,
    enabled: true,
  });
  const runtimeMode = runtimeConfig.mode;
  const runtimeThoughtLevel = runtimeConfig.thoughtLevel;
  const handleThoughtLevelSetOption = useCallback(
    async (optionId: string, value: string) => {
      const requestAuthority = conversationAuthorityRef.current;
      try {
        await runtimeConfig.setConfigOption(optionId, value);
      } catch (error) {
        if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return false;
        throw error;
      }
      return mountedRef.current && conversationAuthorityRef.current === requestAuthority;
    },
    [runtimeConfig]
  );
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  useEffect(() => {
    setReviewInFlight(false);
    setReviewActive(false);
    setGoalClearInFlight(false);
    mobileModeChangeInFlightRef.current = false;
  }, [conversation_id]);

  // Drive the mobile sheet's model entry off the same source XScienceModelSelector uses.
  const {
    model_info,
    canSwitch: canSwitchModel,
    isModelListLoading,
    modelListError,
    retryModelList,
    selectModel,
  } = useAcpModelInfo({
    conversation_id,
    frameIncarnationId,
    backend,
    enabled: true,
    onSelectModelSuccess: () => Message.success(t('agent.model.switchSuccess')),
    onSelectModelFailed: (_modelId, error) => Message.error(t(configErrorMessageKey(error))),
  });
  useEffect(() => {
    if (!runtimeMode?.currentValue) return;
    setCurrentMode(runtimeMode.currentValue);
  }, [runtimeMode?.currentValue]);
  useEffect(() => {
    if (!isMobile) return;
    let active = true;
    void Promise.all([
      loadXScienceAssistants(),
      loadXScienceComputeProviders(),
      loadXScienceSessionComputeProviders(conversation_id),
    ])
      .then(([assistants, providers, enabledProviders]) => {
        if (!active) return;
        setMobileSpecialists(
          assistants.map((assistant) => ({
            id: assistant.id,
            label: resolveAssistantName(assistant, localeKey, assistant.name),
            agentId: assistant.agent_id || assistant.name,
          }))
        );
        setMobileComputeProviders(
          providers
            .filter((provider) => provider.checked)
            .map((provider) => ({
              name: provider.name,
              label: provider.displayName,
            }))
        );
        setMobileEnabledCompute(enabledProviders);
      })
      .catch((error) =>
        console.warn('[AcpSendBox] Failed to load mobile session controls', safeErrorDiagnostic(error))
      );
    return () => {
      active = false;
    };
  }, [conversation_id, isMobile, localeKey]);

  const updatePersistentSessionOption = useCallback(
    async (key: 'autoReview' | 'memory', value: boolean) => {
      const requestAuthority = conversationAuthorityRef.current;
      try {
        await updateXScienceSessionConfig(conversation_id, { [key]: value });
      } catch (error) {
        if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
        throw error;
      }
      if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
      setXScienceSessionOptions((current) => ({ ...current, [key]: value }));
    },
    [conversation_id]
  );

  const handleClearGoal = useCallback(async () => {
    if (goalClearInFlight) return;
    const requestAuthority = conversationAuthorityRef.current;
    setGoalClearInFlight(true);
    try {
      await updateXScienceSessionConfig(conversation_id, { goalText: null });
      if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
      setXScienceSessionOptions((current) => ({ ...current, goalText: null }));
      Message.success(t('conversation.xscienceRuntime.sendBox.goalCleared'));
    } catch (error) {
      if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
      console.error('[AcpSendBox] Failed to clear conversation goal', safeErrorDiagnostic(error));
      Message.error(t('conversation.xscienceRuntime.sendBox.goalClearFailed'));
    } finally {
      if (mountedRef.current && conversationAuthorityRef.current === requestAuthority) setGoalClearInFlight(false);
    }
  }, [conversation_id, goalClearInFlight, t]);

  const handleSheetModeChange = useCallback(
    async (mode: string) => {
      if (!runtimeMode || mode === runtimeMode.currentValue || mobileModeChangeInFlightRef.current) return;
      const requestAuthority = conversationAuthorityRef.current;
      mobileModeChangeInFlightRef.current = true;
      try {
        await runtimeConfig.setConfigOption(runtimeMode.id, mode);
        if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
        setCurrentMode(mode);
        setIsMobileSheetOpen(false);
        Message.success(t('agentMode.switchSuccess'));
      } catch (error) {
        if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
        console.error('[AcpSendBox] Failed to switch mode via sheet:', safeErrorDiagnostic(error));
        Message.error(t(configErrorMessageKey(error)));
      } finally {
        if (mountedRef.current && conversationAuthorityRef.current === requestAuthority) {
          mobileModeChangeInFlightRef.current = false;
        }
      }
    },
    [runtimeConfig, runtimeMode, t]
  );

  const handleSessionComputeChange = useCallback(
    async (providerName: string, checked: boolean) => {
      const requestAuthority = conversationAuthorityRef.current;
      try {
        await setXScienceSessionComputeProvider(conversation_id, providerName, checked);
        if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
        setMobileEnabledCompute((current) =>
          checked ? Array.from(new Set([...current, providerName])) : current.filter((name) => name !== providerName)
        );
      } catch (error) {
        if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
        console.error('[AcpSendBox] Failed to update session compute', safeErrorDiagnostic(error));
        Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
      }
    },
    [conversation_id, t]
  );

  const { setSendBoxHandler } = usePreviewContext();

  // Use useLatestRef to keep latest setters to avoid re-registering handler
  const setContentRef = useLatestRef(setContent);
  const contentRef = useLatestRef(content);
  const atPathRef = useLatestRef(atPath);

  const addOrUpdateMessage = useAddOrUpdateMessage(); // Move this here so it's available in useEffect
  const addOrUpdateMessageRef = useLatestRef(addOrUpdateMessage);
  const removeMessageByMsgId = useRemoveMessageByMsgId();
  const removeMessageByMsgIdRef = useLatestRef(removeMessageByMsgId);
  const updateMessageList = useUpdateMessageList();
  const messages = useMessageList();
  const runtimeView = useConversationRuntimeView(conversation_id, initialConversation);

  // Shared file handling logic
  const { handleFilesAdded, clearFiles } = useSendBoxFiles({
    atPath,
    uploadFile,
    setAtPath,
    setUploadFile,
  });
  const sessionOptionsReady =
    sessionOptionsLoad.conversationId === conversation_id && sessionOptionsLoad.status === 'ready';
  const commandQueueRuntimeGate = {
    hydrated: runtimeView.hydrated,
    canSendMessage: runtimeView.canSendMessage,
    isProcessing: runtimeView.isProcessing,
  };
  const isCancelling = runtimeView.state === 'cancelling';
  const runtimeUnavailable = !runtimeView.hydrated || Boolean(runtimeView.hydrationError);
  const isBusy =
    !runtimeUnavailable &&
    (isCancelling || commandQueueRuntimeGate.isProcessing || !commandQueueRuntimeGate.canSendMessage);
  const reviewBusy = reviewInFlight || reviewActive;
  const asideQueueRef = useRef<AsideQueueBridge | null>(null);
  const aside = useAcpAsideController({
    conversationId: conversation_id,
    ownerId,
    enabled: sideQuestionSupported,
    modelId: model_info?.current_model_id ?? null,
    queue: asideQueueRef,
    clearDraftIfUnchanged: (text) => replaceContentIfUnchanged(text, ''),
    navigate,
  });

  const handleRequestReview = useCallback(async () => {
    if (isBusy || reviewInFlight || reviewActive) return;
    const requestAuthority = conversationAuthorityRef.current;
    setReviewInFlight(true);
    try {
      await requestXScienceFrameAudit(conversation_id);
      if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
      notifyXScienceRuntimeInvalidation(conversation_id);
      Message.success(t('conversation.xscienceRuntime.sendBox.reviewStarted'));
      emitter.emit('chat.history.refresh');
      onRuntimeMutation?.();
    } catch (error) {
      if (!mountedRef.current || conversationAuthorityRef.current !== requestAuthority) return;
      console.error('[AcpSendBox] Failed to start manual review', safeErrorDiagnostic(error));
      Message.error(t('conversation.xscienceRuntime.sendBox.reviewStartFailed'));
    } finally {
      if (mountedRef.current && conversationAuthorityRef.current === requestAuthority) setReviewInFlight(false);
    }
  }, [conversation_id, isBusy, onRuntimeMutation, reviewActive, reviewInFlight, t]);

  // Register handler for adding text from preview panel to sendbox
  useEffect(() => {
    const handler = (text: string) => {
      // If there's existing content, add newline and new text; otherwise just set the text
      const new_content = content ? `${content}\n${text}` : text;
      setContentRef.current(new_content);
    };
    setSendBoxHandler(handler);
  }, [setSendBoxHandler, content]);

  // Listen for sendbox.fill event to append text to sendbox
  useAddEventListener(
    'sendbox.fill',
    (text: string) => {
      const prev = contentRef.current;
      setContentRef.current(prev ? `${prev}${text}` : text);
    },
    []
  );

  // Consume routed sends once; explicitly staged entry points still restore a draft.
  const stageInitialDraft = useCallback(
    (payload: StagedDraftPayload) => {
      applyStagedDraft(
        payload,
        {
          setContent,
          setUploadFile,
          setContextItems,
          setStagedSessionOptions,
          setStagedPlanMode,
        },
        {
          onStagedSessionOptions: (options) => {
            stagedSessionOptionsRef.current = options;
          },
          applyStagedSessionOptions,
        }
      );
    },
    [setContent, setUploadFile, setContextItems, setStagedSessionOptions, setStagedPlanMode, applyStagedSessionOptions]
  );
  const notifyDraftRestoreIssue = useMemo(() => makeDraftRestoreIssueNotifier(t), [t]);
  const initialSend = useAcpInitialMessage({
    conversation_id: conversation_id,
    ownerId,
    canDiscardReadingPreparation: () => !reading.controller.getSnapshot().pending,
    prepareReadingSources: reading.prepareInitialSources,
    backend,
    workspacePath,
    streamReady,
    setAiProcessing,
    resetState,
    markSendStarted: runtimeView.markSendStarted,
    markSendAccepted: runtimeView.markSendAccepted,
    markSendFailed: runtimeView.markSendFailed,
    checkAndUpdateTitle,
    addOrUpdateMessage: addOrUpdateMessageRef.current,
    onDraftPrefill: stageInitialDraft,
    onDraftRestoreIssue: notifyDraftRestoreIssue,
  });

  const resolveCommandExecutionAuthority = useAcpCommandAuthority(
    conversation_id,
    xscienceSessionOptions,
    stagedSessionOptionsRef,
    stagedPlanModeRef
  );

  const executeCommand = useCallback(
    async (
      {
        id,
        input,
        files,
        contextItems: commandContextItems,
        planMode,
        turnIntent,
        readingContext,
        readingContextInvalid,
      }: Pick<
        ConversationCommandQueueItem,
        'id' | 'input' | 'files' | 'contextItems' | 'readingContext' | 'readingContextInvalid'
      > & {
        planMode?: boolean;
        turnIntent?: 'save-as-skill';
      },
      preparedAuthority?: CommandExecutionAuthority,
      preparedTicket?: object
    ) => {
      const displayMessage = buildDisplayMessage(input, files, workspacePath || '');
      const capabilityPayload = buildComposerCapabilityPayload(commandContextItems);
      const conversationAuthority = conversationAuthorityRef.current;
      let submissionTicket = preparedTicket;

      try {
        if (readingContextInvalid) throw new Error(t('conversation.reading.invalidQueued'));
        if (compaction.controller) {
          const admitted = await compaction.controller.prepareForPrompt();
          if (!compaction.controller.current()) return;
          if (!admitted) throw new Error(t('conversation.manualCompaction.waitingForPrompt'));
        }
        if (!mountedRef.current || conversationAuthorityRef.current !== conversationAuthority) return;
        if (!turnIntent) void checkAndUpdateTitle(conversation_id, input);
        // A control command reserves the same runtime gate before resolving
        // branch authority. Ordinary commands reserve it here.
        if (!turnIntent) {
          submissionTicket = runtimeView.markSendStarted();
          setAiProcessing(true);
        }
        // Surface the submitted message immediately. Durable history replaces
        // this optimistic row on the next canonical refresh; long-running
        // turns must never look empty while the model is still thinking.
        if (!turnIntent)
          addOrUpdateMessageRef.current(
            {
              id: `${X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX}${id}`,
              msg_id: `${X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX}${id}`,
              conversation_id,
              type: 'text',
              position: 'right',
              status: 'pending',
              created_at: Date.now(),
              content: { content: displayMessage },
              artifact_refs: capabilityPayload.artifactRefs,
            },
            true
          );
        const commandAuthority = preparedAuthority ?? (await resolveCommandExecutionAuthority(planMode, !turnIntent));
        const pendingRetry = directCommandRetryRef.current;
        if (pendingRetry?.item.id === id && pendingRetry.authority === undefined) {
          directCommandRetryRef.current = {
            ...pendingRetry,
            authority: commandAuthority,
          };
        }
        const result = await ipcBridge.acpConversation.sendMessage.invoke({
          input: displayMessage,
          conversation_id,
          loading_id: id,
          ...(turnIntent ? { turn_intent: turnIntent } : {}),
          ...(!turnIntent && readingContext ? { reading_context: readingContext } : {}),
          files,
          artifact_refs: capabilityPayload.artifactRefs,
          inject_skills: capabilityPayload.injectSkills,
          inject_mcp_server_ids: capabilityPayload.injectMcpServerIds,
          session_options: commandAuthority.sessionOptions,
        });
        if (!mountedRef.current || conversationAuthorityRef.current !== conversationAuthority) return;
        const accepted = runtimeView.markSendAccepted(result.turn_id, result.runtime, result.msg_id, {
          ticket: submissionTicket,
          receipt: result.input_receipt,
        });
        if (accepted === null) {
          removeMessageByMsgIdRef.current(`${X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX}${id}`);
          emitter.emit('chat.history.refresh');
          return;
        }
        if (accepted) setAiProcessing(accepted.isProcessing && accepted.state !== 'waiting_confirmation');
        // The staged intents applied to this first accepted send and must not
        // leak into later manual sends: the frame now records them.
        if (!turnIntent && stagedPlanModeRef.current) {
          stagedPlanModeRef.current = false;
          setStagedPlanMode(false);
        }
        if (!turnIntent && stagedSessionOptionsRef.current) {
          stagedSessionOptionsRef.current = null;
          setStagedSessionOptions(null);
        }
        // The send response is the first authoritative identity for the user
        // message. Promote the optimistic row to that identity immediately so
        // the durable userCreated event cannot leave two identical rows on
        // screen while a long task is running.
        removeMessageByMsgIdRef.current(`${X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX}${id}`);
        if (result.msg_id) {
          addOrUpdateMessageRef.current(
            buildAcceptedAcpUserMessage({
              conversationId: conversation_id,
              messageId: result.msg_id,
              text: displayMessage,
              artifactRefs: capabilityPayload.artifactRefs,
              turnIntent,
            }),
            false
          );
        }
        if (
          !turnIntent &&
          commandAuthority.targetBranchId &&
          getXScienceBranchSelectionRevision(conversation_id) === commandAuthority.targetBranchRevision &&
          getSelectedXScienceBranch(conversation_id) === commandAuthority.targetBranchId
        ) {
          selectXScienceBranch(conversation_id, null);
        }
        emitter.emit('chat.history.refresh');
      } catch (error: unknown) {
        if (!mountedRef.current || conversationAuthorityRef.current !== conversationAuthority) throw error;
        removeMessageByMsgIdRef.current(`${X_SCIENCE_OPTIMISTIC_USER_MESSAGE_PREFIX}${id}`);
        const pendingRetry = directCommandRetryRef.current;
        if (
          pendingRetry?.item.id === id &&
          pendingRetry.authority?.targetBranchId &&
          isBackendHttpError(error) &&
          error.status === 409
        ) {
          // The mutation identity remains stable for an idempotent retry, but
          // an explicit branch conflict invalidates the cached CAS authority.
          // Resolve the latest active branch/generation before retrying.
          directCommandRetryRef.current = {
            ...pendingRetry,
            authority: undefined,
          };
        }
        const errorMsg = redactErrorText(parseError(error) || t('common.unknownError'));
        if (runtimeView.markSendFailed(errorMsg, submissionTicket) === null) throw error;

        // Archived conversation (e.g. legacy Gemini). Backend signals this
        // via HTTP 410 + code='CONVERSATION_ARCHIVED' — identified by code,
        // not by substring matching.
        if (isBackendHttpError(error) && error.code === 'CONVERSATION_ARCHIVED') {
          Message.error({
            content: error.backendMessage || errorMsg,
            duration: 6000,
          });
          setAiProcessing(false);
          throw error;
        }

        const isAuthError =
          errorMsg.includes('[ACP-AUTH-') ||
          errorMsg.includes('authentication failed') ||
          errorMsg.includes('认证失败');
        const visibleError = isAuthError ? t('acp.auth.failed', { backend, error: errorMsg }) : errorMsg;
        addOrUpdateMessageRef.current(
          {
            id: uuid(),
            msg_id: uuid(),
            type: 'tips',
            position: 'center',
            conversation_id,
            created_at: Date.now(),
            content: {
              content: visibleError,
              type: 'error',
              error: buildSendFailureError(error, errorMsg),
            },
          },
          true
        );

        resetState();
        setAiProcessing(false);
        throw error;
      }

      if (files.length > 0) {
        emitter.emit('acp.workspace.refresh');
      }
    },
    [
      backend,
      checkAndUpdateTitle,
      compaction.controller,
      conversation_id,
      resetState,
      resolveCommandExecutionAuthority,
      runtimeView,
      setAiProcessing,
      t,
      workspacePath,
    ]
  );

  const { skillSave, skillSurfaces, skillMenuProps, mobileSkillProps } = useAcpConversationSkillController({
    conversationAuthorityRef,
    mountedRef,
    runtimeView,
    setAiProcessing,
    executeCommand,
    resolveCommandExecutionAuthority,
    messages,
    disabled: isBusy || readingAttachments.preparing,
    saveDisabled: runtimeUnavailable || !sessionOptionsReady,
    isXScience: isXScienceConversation,
    isMobile,
    assistantId: conversationContext?.assistantId,
    // This is server-projected runtime identity, not the localized display prop.
    runtimeAgentName: (initialConversation?.extra as { agent_name?: string } | undefined)?.agent_name,
    locale: localeKey,
    loadedSkills,
    mutateComposerCapabilities,
    fetchSlashCommands: messageState.fetchSlashCommands,
    onSelectSkill: handleSkillSelected,
  });
  const handleContentChange = useCallback(
    (val: string) => {
      if (skillSave.isBlocking()) return;
      // Preserve a direct-send receipt through SendBox's synchronous pre-send
      // clear; a real non-empty edit still starts a new mutation.
      if (val !== '' && directCommandRetryRef.current?.item.input !== val) directCommandRetryRef.current = null;
      setContent(val);
    },
    [setContent, skillSave.isBlocking]
  );

  const {
    items: queuedCommands,
    isInterrupted: isCommandQueueInterrupted,
    isQueueingEnabled,
    hasPendingCommands,
    enqueue,
    remove,
    reorder,
    retry: retryQueuedCommand,
    sendNow,
    setQueueingEnabled,
    pause: pauseCommandQueue,
    resume: resumeCommandQueue,
    lockInteraction,
    unlockInteraction,
    resetActiveExecution,
    getQueuedItem,
  } = useConversationCommandQueue({
    conversation_id: conversation_id,
    enabled: true,
    isBusy,
    runtimeGate: commandQueueRuntimeGate,
    onExecute: executeCommand,
    isItemBlocked: aside.isQueueItemBlocked,
  });
  asideQueueRef.current = { items: queuedCommands, remove, getQueuedItem };

  const onSendHandler = async (message: string) => {
    if (skillSave.isBlocking()) return;
    if (compaction.controller?.getSnapshot().waitingForPrompt) return;
    const origin = conversationAuthorityRef.current;
    const atPathFiles = atPath.map((item) => (typeof item === 'string' ? item : item.path));
    const allFiles = Array.from(new Set([...uploadFile, ...atPathFiles].filter(Boolean)));
    const normalizedMessage = message.trim() || t('conversation.sendbox.contextOnlyPrompt');
    const commandContextItems = [...contextItems];
    const sendIntent = sendIntentRef.current;
    sendIntentRef.current = null;
    const planMode = sendIntent === 'plan_first' ? true : undefined;
    const signature = JSON.stringify({
      input: normalizedMessage,
      files: allFiles,
      contextItems: commandContextItems,
      branchId: getSelectedXScienceBranch(conversation_id),
      branchRevision: getXScienceBranchSelectionRevision(conversation_id),
      sessionOptions: xscienceSessionOptions,
      planMode,
    });
    const retry = directCommandRetryRef.current?.signature === signature ? directCommandRetryRef.current : null;
    let readingSnapshot: ReturnType<typeof reading.captureSend> | undefined;
    try {
      // Freeze the click before maintenance draining or PDF preparation yields.
      // Exact retry and edited queue items already own their original selection.
      if (!retry && queuedPdfInvalid) throw new Error(t('conversation.reading.invalidQueued'));
      if (!retry && !queuedPdfSelection) readingSnapshot = reading.captureSend();
    } catch (error) {
      if (contentRef.current === '') setContent(normalizedMessage);
      throw error;
    }
    const maintenance = compaction.controller ? await compaction.controller.preparePromptIntent() : undefined;
    if (compaction.controller && !compaction.controller.current()) return;
    if (!mountedRef.current || conversationAuthorityRef.current !== origin) return;
    if (maintenance && !maintenance.safe) {
      if (contentRef.current === '') setContent(normalizedMessage);
      throw new Error(t('conversation.manualCompaction.waitingForPrompt'));
    }
    if (retry) {
      try {
        await executeCommand(retry.item, retry.authority);
        if (directCommandRetryRef.current?.item.id === retry.item.id) directCommandRetryRef.current = null;
        clearFiles();
        setContextItems([]);
        setQueuedPdfSelection();
        emitter.emit('acp.selected.file.clear');
        return;
      } catch (error) {
        if (contentRef.current === '') setContent(retry.item.input);
        throw error;
      }
    }
    directCommandRetryRef.current = null;
    let readingContext;
    try {
      readingContext = queuedPdfSelection ?? (await readingAttachments.prepare(readingSnapshot!));
    } catch (error) {
      if (contentRef.current === '') setContent(normalizedMessage);
      throw error;
    }

    if (
      shouldEnqueueConversationCommand({
        enabled: isQueueingEnabled,
        isBusy: isBusy && !maintenance?.preempted,
        hasPendingCommands,
      })
    ) {
      enqueue({
        input: normalizedMessage,
        files: allFiles,
        contextItems: commandContextItems,
        planMode,
        readingContext,
      });
      clearFiles();
      setContextItems([]);
      setQueuedPdfSelection();
      emitter.emit('acp.selected.file.clear');
      return;
    }

    const item = createQueuedCommandItem({
      input: normalizedMessage,
      files: allFiles,
      contextItems: commandContextItems,
      planMode,
      readingContext,
    });
    directCommandRetryRef.current = { signature, item };
    try {
      await executeCommand(item);
      if (directCommandRetryRef.current?.item.id === item.id) directCommandRetryRef.current = null;
      clearFiles();
      setContextItems([]);
      setQueuedPdfSelection();
      emitter.emit('acp.selected.file.clear');
    } catch (error) {
      if (contentRef.current === '') setContent(item.input);
      throw error;
    }
  };

  const handleRepairAndRegenerate = async (checks: XScienceVerificationCheck[]) => {
    await onSendHandler(buildXScienceReviewRepairPrompt(checks, i18n.language));
  };

  const handleSendOption = useCallback(
    (intent: XScienceSendIntent) => {
      if (skillSave.isBlocking()) return;
      const draft = contentRef.current;
      if (!draft.trim()) return;

      if (intent === 'plan_first') {
        sendIntentRef.current = 'plan_first';
        void onSendHandler(draft);
        return;
      }

      if (
        uploadFile.length > 0 ||
        atPath.length > 0 ||
        contextItems.length > 0 ||
        queuedPdfSelection ||
        queuedPdfInvalid ||
        reading.view.context?.bindings.length
      ) {
        Message.warning(t('conversation.sendbox.structuredContextNormalSend'));
        return;
      }

      if (intent === 'side_chat') {
        void aside.sideDraft(draft);
        return;
      }

      void aside.branchDraft(draft);
    },
    [
      atPath.length,
      contextItems.length,
      aside.sideDraft,
      aside.branchDraft,
      onSendHandler,
      queuedPdfSelection,
      queuedPdfInvalid,
      reading.view.context?.bindings.length,
      skillSave.isBlocking,
      t,
      uploadFile.length,
    ]
  );

  const handleEditQueuedCommand = useCallback(
    (item: ConversationCommandQueueItem) => {
      if (skillSave.isBlocking()) return;
      remove(item.id);
      sendIntentRef.current = item.planMode ? 'plan_first' : null;
      setContent(item.input);
      setUploadFile(Array.from(new Set(item.files)));
      setContextItems(item.contextItems);
      setQueuedPdfSelection(item.readingContext, item.readingContextInvalid);
      setAtPath([]);
      emitter.emit('acp.selected.file.clear');
    },
    [remove, setAtPath, setContent, setContextItems, setUploadFile, setQueuedPdfSelection, skillSave.isBlocking]
  );

  const handleOpenQueuedCommandInSideChat = useCallback(
    (item: ConversationCommandQueueItem) => {
      if (item.files.length > 0 || item.contextItems.length > 0 || item.readingContext || item.readingContextInvalid) {
        Message.warning(t('conversation.commandQueue.sideChatAttachmentsUnsupported'));
        return;
      }
      void aside.openQueued(item);
    },
    [aside.openQueued, t]
  );

  const appendSelectedFiles = useCallback(
    (files: string[]) => {
      setUploadFile((prev) => [...prev, ...files]);
    },
    [setUploadFile]
  );
  const { openFileSelector, onSlashBuiltinCommand } = useOpenFileSelector({
    onFilesSelected: appendSelectedFiles,
  });

  const { entries: attachEntries, hiddenFileInput: attachHiddenInput } = useAttachEntry({
    openFileSelector,
    onLocalFilesAdded: handleFilesAdded,
    onBrowserFilesAdded: canSelectProjectFiles ? handleLocalFilesSelected : undefined,
  });

  const sheetEntries = useAcpMobileActionSheetController({
    isMobile,
    runtimeMode,
    currentMode,
    runtimeThoughtLevel,
    runtimeConfig,
    grantsController,
    canSwitchModel,
    model_info,
    isModelListLoading,
    modelListError,
    retryModelList,
    selectModel,
    xscienceSessionOptions,
    sessionOptionsReady,
    setXScienceSessionOptions,
    updatePersistentSessionOption,
    mobileSpecialists,
    mobileComputeProviders,
    mobileEnabledCompute,
    handleSessionComputeChange,
    handleSheetModeChange,
    handleThoughtLevelSetOption,
    attachEntries:
      isXScienceConversation && planPresentation.reference
        ? [
            ...attachEntries,
            {
              key: 'view-plan',
              label: t('conversation.attachMenu.viewPlan'),
              meta: planPresentation.reference.progress
                ? `${planPresentation.reference.progress.completed}/${planPresentation.reference.progress.total}`
                : undefined,
              onClick: planPresentation.openPlan,
            },
          ]
        : attachEntries,
    ...mobileSkillProps,
    loadedMcpStatuses,
    selectedSkillNames,
    selectedMcpServerIds,
    onSelectMcpServer: handleMcpSelected,
    t,
    i18n,
  });

  useAddEventListener('acp.selected.file', setAtPath);
  useAddEventListener('acp.selected.file.append', (selectedItems: Array<string | FileOrFolderItem>) => {
    const merged = mergeFileSelectionItems(atPathRef.current, selectedItems);
    if (merged !== atPathRef.current) {
      setAtPath(merged as Array<string | FileOrFolderItem>);
    }
  });

  const handleStop = () =>
    stopAcpConversation({
      backend,
      conversationId: conversation_id,
      compaction: compaction.controller,
      authority: conversationAuthorityRef,
      mounted: mountedRef,
      runtime: runtimeView,
      t,
      resetQueueExecution: () => {
        resetActiveExecution('stop');
        if (queuedCommands.length > 0) pauseCommandQueue('interrupted');
      },
      setAiProcessing,
      resetState,
    });
  const { handleRuntimeUpdated, handleResumeStarted, handleResumeFailed } = useAcpRuntimeActions(
    conversation_id,
    runtimeView,
    updateMessageList,
    terminalFailureRollbackRef,
    setAiProcessing,
    onRuntimeMutation
  );
  const sendBoxWidthClass = CHAT_SURFACE_WIDTH_CLASS;
  const taskCenterMetrics = useAcpTaskCenterMetrics(messageState.tokenUsage?.total_tokens);

  return (
    <div
      ref={sendBoxAnchorRef}
      className={`${sendBoxWidthClass} conversation-composer w-full min-w-0 max-w-full box-border flex flex-col mt-auto mb-16px`}
    >
      <BtwOverlay controller={aside.sideQuestion} parentTaskRunning={isBusy} />
      {aside.branchStatus}
      <XScienceRuntimeOperations
        key={planPresentation.key}
        conversationId={conversation_id}
        runtimeState={runtimeView.state}
        terminalProjectionPending={runtimeView.view.terminalProjectionPending}
        onResumeStarted={handleResumeStarted}
        onResumeFailed={handleResumeFailed}
        onResumed={handleRuntimeUpdated}
        onRuntimeUpdated={handleRuntimeUpdated}
        onPlanReferenceChange={planPresentation.onPlanReferenceChange}
        runtimeAuthorityUnavailable={Boolean(runtimeView.hydrationError)}
        onRetryRuntimeAuthority={runtimeView.retryHydration}
        onStop={handleStop}
        onReviewActivityChange={setReviewActive}
        onRepairAndRegenerate={handleRepairAndRegenerate}
        taskCenterMetrics={taskCenterMetrics}
        onChooseModel={isMobile ? () => setIsMobileSheetOpen(true) : undefined}
        modelInfo={
          model_info
            ? {
                currentModelId: model_info.current_model_id,
                availableModels: model_info.available_models.map((model) => ({
                  id: model.id,
                  label: model.label || model.id,
                })),
              }
            : null
        }
      />
      {!sideQuestionSupported ? (
        <ThoughtDisplay running={aiProcessing && !hasThinkingMessage && runtimeView.state !== 'waiting_confirmation'} />
      ) : null}

      <CommandQueuePanel
        items={queuedCommands}
        isInterrupted={isCommandQueueInterrupted}
        isQueueingEnabled={isQueueingEnabled}
        isSendNowDisabled={runtimeUnavailable || isCancelling || isCommandQueueInterrupted || skillSave.inFlight}
        onInteractionLock={lockInteraction}
        onInteractionUnlock={unlockInteraction}
        onEdit={handleEditQueuedCommand}
        onOpenInSideChat={sideQuestionSupported ? handleOpenQueuedCommandInSideChat : undefined}
        onReorder={reorder}
        onRemove={remove}
        onRetry={(commandId) => void retryQueuedCommand(commandId)}
        onSendNow={(commandId) => void sendNow(commandId)}
        onQueueingChange={setQueueingEnabled}
        onResumeInterruptedQueue={resumeCommandQueue}
      />

      {skillSurfaces}

      <SendBox
        onMobilePlusClick={isMobile ? () => setIsMobileSheetOpen(true) : undefined}
        value={content}
        onChange={handleContentChange}
        selectedWorkspaceItems={atPath}
        onSelectedWorkspaceItemsChange={(items) => {
          emitter.emit('acp.selected.file', items);
          setAtPath(items);
        }}
        loading={isBusy}
        disabled={
          runtimeUnavailable || skillSave.inFlight || readingAttachments.preparing || compaction.view.waitingForPrompt
        }
        allowSendWhileLoading={!skillSave.inFlight}
        placeholder={t('acp.sendbox.placeholder', {
          backend: agent_name || 'X-Science',
        })}
        className='z-10 w-full min-w-0 max-w-full'
        onFilesAdded={handleFilesAdded}
        onBrowserFilesAdded={canSelectProjectFiles ? handleLocalFilesSelected : undefined}
        hasPendingAttachments={uploadFile.length > 0 || atPath.length > 0}
        hasPendingContext={contextItems.length > 0}
        onSelectSkillCommand={handleSkillSelected}
        onSelectArtifactReference={handleArtifactReferenceSelected}
        enableBtw={sideQuestionSupported}
        btwController={aside.sideQuestion}
        supportedExts={allSupportedExts}
        defaultMultiLine={!isMobile}
        lockMultiLine={!isMobile}
        tools={
          isMobile ? undefined : (
            <div className='flex min-w-0 items-center gap-4px'>
              <FileAttachButton
                key={`${ownerId}:${conversation_id}`}
                openFileSelector={openFileSelector}
                onSelectProjectFiles={canSelectProjectFiles ? openProjectFiles : undefined}
                onLocalFilesAdded={handleFilesAdded}
                onLocalFilesSelected={canSelectProjectFiles ? handleLocalFilesSelected : undefined}
                loadedMcpStatuses={loadedMcpStatuses}
                {...skillMenuProps}
                selectedSkillNames={selectedSkillNames}
                selectedMcpServerIds={selectedMcpServerIds}
                onSelectMcpServer={handleMcpSelected}
                onViewPlan={
                  isXScienceConversation && planPresentation.reference ? planPresentation.openPlan : undefined
                }
                planProgress={planPresentation.reference?.progress}
                onViewContextWindow={
                  isXScienceConversation
                    ? (returnFocus) => {
                        setContextWindow((current) => ({ sequence: current.sequence + 1, returnFocus }));
                      }
                    : undefined
                }
                onRequestReview={handleRequestReview}
                reviewLabelMode='manual'
                reviewInFlight={reviewBusy}
                reviewDisabled={isBusy}
              />
              <XScienceSessionOptionsMenu
                rootFrameId={conversation_id}
                value={xscienceSessionOptions}
                onChange={setXScienceSessionOptions}
                onPersistentOptionChange={updatePersistentSessionOption}
                onClearGoal={handleClearGoal}
                goalClearInFlight={goalClearInFlight}
                disabled={!sessionOptionsReady || skillSave.inFlight}
                permissionControl={{
                  contextKey: JSON.stringify([ownerId, conversation_id, frameIncarnationId]),
                  grantsController,
                  option: runtimeMode,
                  setStatus: runtimeConfig.setStatus,
                  setConfigOption: runtimeConfig.setConfigOption,
                  disabled: runtimeUnavailable || skillSave.inFlight,
                }}
              />
            </div>
          )
        }
        rightTools={
          <div className='flex items-center gap-8px min-w-0'>
            {isXScienceConversation ? (
              <ContextUsagePanel
                key={`${ownerId}:${conversation_id}:${frameIncarnationId ?? ''}`}
                conversationId={conversation_id}
                active={isBusy}
                compaction={compaction}
                openRequest={contextWindow.sequence}
                returnFocus={contextWindow.returnFocus}
                triggerVisible={isBusy || content.trim() === '' || compaction.needsAttention}
              />
            ) : null}
            {isXScienceConversation && content.trim() !== '' && (
              <OptimizePromptAction
                draft={content}
                conversationId={conversation_id}
                ownerId={ownerId}
                disabled={skillSave.inFlight}
                replaceIfCurrent={replaceContentIfUnchanged}
              />
            )}
            <XScienceModelSelector
              conversation_id={conversation_id}
              frameIncarnationId={frameIncarnationId}
              backend={backend}
              initialModelId={model_info?.current_model_id}
            />
          </div>
        }
        prefix={
          <PdfReadingComposerPrefix
            draftPersistence={{ failed: draftSaveFailed, retry: retryDraftSave }}
            enabled={isXScienceConversation}
            reading={reading}
            initialSend={initialSend}
            suggestion={{ visible: readingAttachments.suggested, keepAttachments: keepPdfAttachments }}
            frozenQueue={{
              present: Boolean(queuedPdfSelection || queuedPdfInvalid),
              useCurrent: () => {
                directCommandRetryRef.current = null;
                setQueuedPdfSelection();
              },
            }}
          >
            <AcpComposerAttachments
              contextItems={contextItems}
              setContextItems={setContextItems}
              onOpenArtifact={reading.openArtifact}
              uploadFile={uploadFile}
              setUploadFile={setUploadFile}
              atPath={atPath}
              setAtPath={setAtPath}
            />
          </PdfReadingComposerPrefix>
        }
        onSend={onSendHandler}
        sendButtonSuffix={
          content.trim() || uploadFile.length > 0 || atPath.length > 0 || contextItems.length > 0 ? (
            <XScienceSendOptionsMenu disabled={isBusy || runtimeUnavailable} hasDraft onSelect={handleSendOption} />
          ) : undefined
        }
        slash_commands={slashCommands}
        onSlashBuiltinCommand={onSlashBuiltinCommand}
        compactActions={false}
      ></SendBox>
      <GuidProjectFilesModal
        visible={projectFilesOpen}
        projectId={projectId}
        onCancel={() => setProjectFilesOpen(false)}
        onSelect={handleProjectArtifactsSelected}
      />
      {renderMobileSheet(isMobile, sheetEntries, attachHiddenInput, t('common.more'))}
    </div>
  );
};

export default AcpSendBox;
