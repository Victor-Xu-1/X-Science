/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, fireEvent, screen, waitFor, type RenderOptions } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import * as httpBridge from '@/common/adapter/httpBridge';
import type { TMessage } from '@/common/chat/chatLib';
import type { TChatConversation, TConversationRuntimeSummary } from '@/common/config/storage';
import type { XSciencePlanReference } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import AcpSendBox from '@/renderer/pages/conversation/platforms/acp/AcpSendBox';
import * as composerContextSelection from '@/renderer/pages/conversation/platforms/acp/useAcpComposerContextSelection';
import type { UseAcpMessageReturn } from '@/renderer/pages/conversation/platforms/acp/useAcpMessage';
import type { ComposerContextItem } from '@/renderer/components/chat/SendBox/composerCompositionModel';
import { renderWithI18n } from '../../i18nTestUtils';
import { pdfBinding, pdfContext } from './pdfReadingFixtures';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { getRendererAccountOwnerId, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { clearXScienceFrameReads } from '@/renderer/services/xScienceFrameReads';
import { getContextCompactionController } from '@/renderer/services/compaction/contextCompactionRegistry';
import {
  localSendFailed,
  localSendStarted,
  resetConversationRuntimeViewStoreForTest,
} from '@/renderer/pages/conversation/runtime/conversationRuntimeViewStore';
import * as runtimeStore from '@/renderer/pages/conversation/runtime/conversationRuntimeViewStore';
import type { StagedSessionOptions } from '@/renderer/hooks/chat/sendBoxDraftPersistence';
import { retainCompactionCapacity } from './contextCompactionCapacityTestFixture';
import { registerSpecialistAsideCases, type SpecialistAsideFixture } from './acpSpecialistAsideCases';
import type { SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import type { SessionComputeControl } from '@/renderer/hooks/xScience/runtime/useSessionComputeControls';
import { computeRootProjection, registerComputeRootCases } from './acpComputeRootCases';
import {
  manualCompactionIdentity,
  manualCompactionTarget,
  type manualCompactionRequest,
  manualCompactionReceipt,
  manualCompactionSnapshot,
  manualCompactionUsage,
} from './manualContextCompactionFixtures';
import {
  sessionGrantIdentity,
  sessionGrantRevokeBody,
  sessionGrantRevokeResponse,
  sessionGrantsSnapshot,
} from '../../xscience/sessionPermissionGrantsFixtures';

let settleDefaultSessionOptions: (() => Promise<void>) | null = null;
let settleDefaultFramePlan: (() => Promise<void>) | null = null;
let queuedCommandSequence = 0;
const realContextDropdown = { current: false };
const realPreparationRuntime = { current: false };
const preparationSnapshotMock = vi.fn();
const specialistAsideFixture: SpecialistAsideFixture = {
  active: false,
  item: null,
  draft: {},
  capture: vi.fn<(branch?: string | null) => SpecialistSendAuthority>(() => ({})),
  warning: vi.fn(),
  remove: vi.fn(),
};
const compactionEnqueueMock = vi.fn();
const clearFilesMock = vi.fn();
const sendBoxProps = {
  current: null as { allowSendWhileLoading?: boolean; bottomHint?: React.ReactNode } | null,
};

const render = async (ui: React.ReactElement, options?: RenderOptions) => {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(ui, 'zh-CN', {
      ...options,
      wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter>,
    });
    await settleDefaultSessionOptions?.();
    await settleDefaultFramePlan?.();
    await Promise.resolve();
  });
  return view;
};

const createDeferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
};

const sendMessageInvokeMock = vi.fn();
const stopInvokeMock = vi.fn();
const addOrUpdateMessageMock = vi.fn();
const updateMessageListMock = vi.fn();
const removeMessageByMsgIdMock = vi.fn();
const resetStateMock = vi.fn();
const setAiProcessingMock = vi.fn();
const messageErrorMock = vi.fn();
const messageSuccessMock = vi.fn();
const loadFramePlanMock = vi.fn();
const resumeFrameMock = vi.fn();
const requestFrameAuditMock = vi.fn();
const notifyRuntimeInvalidationMock = vi.fn();
const loadSessionOptionsMock = vi.fn();
const loadSessionMemoryMock = vi.fn();
const loadSessionReviewMock = vi.fn();
const realMemoryAuthority = { current: false };
const realReviewAuthority = { current: false };
const updateSessionConfigMock = vi.fn();
const loadAssistantsMock = vi.fn();
const saveConversationAsSkillMock = vi.fn();
const loadComputeProvidersMock = vi.fn();
const loadSessionComputeProvidersMock = vi.fn();
const setSessionComputeProviderMock = vi.fn();
const loadComposerCapabilitiesMock = vi.fn();
const loadProjectArtifactsMock = vi.fn();
const loadSkillLibraryMock = vi.fn();
const getSelectedBranchMock = vi.fn();
const getBranchRevisionMock = vi.fn();
const loadBranchStateMock = vi.fn();
const selectBranchMock = vi.fn();
const fileAttachProps = { current: null as Record<string, unknown> | null };
const draftContextItemsMock = { current: [] as ComposerContextItem[] };
const sessionOptionsProps = { current: null as Record<string, unknown> | null };
const messageListMock = { current: [] as TMessage[] };
const runtimeOperationsProps = {
  current: null as {
    onChooseModel?: () => void;
    runtimeState?: string;
    runtimeAuthorityUnavailable?: boolean;
    onRetryRuntimeAuthority?: () => void;
    onStop?: () => void | Promise<void>;
    onReviewActivityChange?: (active: boolean) => void;
    onResumed?: (turnId: string, runtime: TConversationRuntimeSummary) => void;
    onRuntimeUpdated?: (turnId: string, runtime: TConversationRuntimeSummary) => void;
    onPlanReferenceChange?: (owner: string, reference: XSciencePlanReference | null) => void;
    taskCenterMetrics?: {
      total: {
        tokenUsage: number | null;
        modelCallCount: number | null;
        toolCallCount: number | null;
      };
      latest: {
        tokenUsage: number | null;
        modelCallCount: number | null;
        toolCallCount: number | null;
      };
    };
  } | null,
};
const mobileActionSheetOpen = { current: false };
const realMobileActionSheet = { current: false };
const realAgentControls = { current: false };
const runtimeViewMock = {
  activeTurnId: 'turn-1' as string | null,
  markStopRequested: vi.fn(),
  markStopAcknowledged: vi.fn(),
  resetLocalGate: vi.fn(),
  markSendStarted: vi.fn(),
  isSendPreparing: vi.fn(),
  markSendDispatched: vi.fn(),
  cancelSendPreparation: vi.fn(),
  markSendAccepted: vi.fn(),
  markSendFailed: vi.fn(),
  isProcessing: true,
  canSendMessage: false,
  hydrated: true,
  hydrationError: null as string | null,
  retryHydration: vi.fn(),
  state: 'running',
  view: {
    activeInputMessageId: null as string | null,
    localStopping: false,
    hasBackendRuntime: true,
    hasTask: true,
    taskStatus: 'running' as 'pending' | 'running' | 'finished' | 'error' | 'cancelled' | null,
  },
};
const emitterEmitMock = vi.fn();
const setSendBoxHandlerMock = vi.fn();
const useAcpConfigOptionsMock = vi.fn();
const useAcpModelInfoMock = vi.fn();
const askSideQuestionMock = vi.fn();
const isMobileMock = { current: false };
const mobilePlusProject = { current: null as { projectId: string } | null };
const mobileActionSheetEntries = {
  current: [] as Array<{
    key: string;
    label?: React.ReactNode;
    description?: React.ReactNode;
    onClick?: () => void;
    submenu?: {
      onSelect?: (value: string) => void;
      options?: Array<{
        key: string;
        description?: string;
        active?: boolean;
        checked?: boolean | 'mixed';
        disabled?: boolean;
        kind?: string;
      }>;
    };
  }>,
};

vi.mock('@/common', () => ({
  ipcBridge: {
    acpConversation: {
      sendMessage: {
        invoke: (...args: unknown[]) => sendMessageInvokeMock(...args),
      },
    },
    conversation: {
      listChanged: { on: () => () => {} },
      turnCompleted: { on: () => () => {} },
      stop: {
        invoke: (...args: unknown[]) => stopInvokeMock(...args),
      },
    },
    realtime: { reconnected: { on: () => () => {} } },
    runtime: { statusChanged: { on: () => () => {} } },
  },
}));

vi.mock('@/renderer/components/chat/SendBox', () => ({
  default: ({
    value,
    onSend,
    onChange,
    rightTools,
    prefix,
    placeholder,
    bottomHint,
    onStop,
    onMobilePlusClick,
    tools,
    disabled,
    allowSendWhileLoading,
    btwController,
    sendButtonSuffix,
  }: {
    value?: string;
    onSend: (message: string) => Promise<void>;
    onStop?: () => Promise<void>;
    onChange?: (value: string) => void;
    rightTools?: React.ReactNode;
    prefix?: React.ReactNode;
    placeholder?: string;
    bottomHint?: React.ReactNode;
    onMobilePlusClick?: (trigger?: HTMLButtonElement) => void;
    tools?: React.ReactNode;
    disabled?: boolean;
    allowSendWhileLoading?: boolean;
    btwController?: unknown;
    sendButtonSuffix?: React.ReactNode;
  }) => (
    <div
      data-allow-send-while-loading={allowSendWhileLoading ? 'true' : 'false'}
      data-aside-owner={btwController ? 'external' : 'local'}
    >
      {tools}
      {/* Match the real SendBox compact outlet: hidden tools must not mount owners. */}
      {isMobileMock.current && onMobilePlusClick ? null : rightTools}
      {prefix}
      {specialistAsideFixture.active ? sendButtonSuffix : null}
      <output data-testid='sendbox-draft'>{value}</output>
      <output data-testid='sendbox-placeholder'>{placeholder}</output>
      <output data-testid='sendbox-bottom-hint'>{bottomHint ?? ''}</output>
      <button type='button' onClick={() => onChange?.('hello')}>
        change
      </button>
      <button type='button' onClick={(event) => onMobilePlusClick?.(event.currentTarget)}>
        mobile more
      </button>
      <button
        type='button'
        disabled={disabled}
        onClick={() => {
          onChange?.('');
          void onSend('Hello').catch(() => {});
        }}
      >
        send
      </button>
      {onStop ? (
        <button
          type='button'
          onClick={() => {
            void onStop();
          }}
        >
          stop
        </button>
      ) : null}
    </div>
  ),
}));

vi.mock('@/renderer/components/chat/BtwOverlay', () => ({
  default: () => null,
}));
vi.mock('@/renderer/components/chat/BtwOverlay/useBtwCommand', () => ({
  useBtwCommand: () => ({
    answer: '',
    isLoading: false,
    isOpen: false,
    question: '',
    ask: (...args: unknown[]) => askSideQuestionMock(...args),
    dismiss: vi.fn(),
    operation: null,
    starting: false,
    action: null,
    receipt: null,
    acceptedConsumer: null,
    getReceipt: () => null,
    getAcceptedConsumer: () => null,
    acknowledgeConsumer: vi.fn(),
    acknowledgeBranch: vi.fn(),
  }),
}));
vi.mock('@/renderer/components/xScience/runtime/XScienceModelSelector', () => ({
  default: ({ conversation_id }: { conversation_id: string }) => (
    <button type='button' data-testid='composer-model-selector'>
      模型 {conversation_id}
    </button>
  ),
}));
vi.mock('@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu')>();
  return {
    default: (props: React.ComponentProps<typeof actual.default>) => {
      sessionOptionsProps.current = props;
      return realAgentControls.current ? <actual.default {...props} /> : null;
    },
  };
});
vi.mock('@/renderer/components/xScience/runtime/XScienceBranchMenu', () => ({
  default: ({ rootFrameId }: { rootFrameId: string }) => (
    <button data-testid='branch-menu' data-root-frame-id={rootFrameId}>
      branches
    </button>
  ),
}));
vi.mock('@/renderer/components/xScience/runtime/XScienceRuntimeOperations', async (original) => {
  const actual = await original<typeof import('@/renderer/components/xScience/runtime/XScienceRuntimeOperations')>();
  return {
  default: (props: {
    onChooseModel?: () => void;
    runtimeState?: string;
    runtimeAuthorityUnavailable?: boolean;
    onRetryRuntimeAuthority?: () => void;
    onStop?: () => void | Promise<void>;
    onReviewActivityChange?: (active: boolean) => void;
    onResumed?: (turnId: string, runtime: TConversationRuntimeSummary) => void;
    onRuntimeUpdated?: (turnId: string, runtime: TConversationRuntimeSummary) => void;
    onPlanReferenceChange?: (owner: string, reference: XSciencePlanReference | null) => void;
    taskCenterMetrics?: {
      total: {
        tokenUsage: number | null;
        modelCallCount: number | null;
        toolCallCount: number | null;
      };
      latest: {
        tokenUsage: number | null;
        modelCallCount: number | null;
        toolCallCount: number | null;
      };
    };
  }) => {
    runtimeOperationsProps.current = props;
    if (realPreparationRuntime.current) return <actual.default {...(props as React.ComponentProps<typeof actual.default>)} />;
    return null;
  },
};
});
vi.mock('@/renderer/services/xScienceRuntimeOperations', async (original) => ({
  ...(await original<typeof import('@/renderer/services/xScienceRuntimeOperations')>()),
  loadXScienceRuntimeSnapshot: (...args: unknown[]) => preparationSnapshotMock(...args),
  loadXScienceFramePlanArtifact: (...args: unknown[]) => loadFramePlanMock(...args),
  notifyXScienceRuntimeInvalidation: (...args: unknown[]) => notifyRuntimeInvalidationMock(...args),
  resumeXScienceFrame: (...args: unknown[]) => resumeFrameMock(...args),
}));
vi.mock('@/renderer/services/skills/xScienceSkillLibrary', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/services/skills/xScienceSkillLibrary')>();
  return {
    ...actual,
    saveXScienceConversationAsSkill: (...args: unknown[]) => saveConversationAsSkillMock(...args),
  };
});
vi.mock('@/renderer/services/xScienceCapabilities', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/services/xScienceCapabilities')>();
  return {
    ...actual,
    loadXScienceSkills: (...args: unknown[]) => loadSkillLibraryMock(...args),
    loadXScienceMcpServers: vi.fn().mockResolvedValue([]),
  };
});
vi.mock('@/renderer/services/xScienceAnnotations', () => ({
  requestXScienceFrameAudit: (...args: unknown[]) => requestFrameAuditMock(...args),
}));
vi.mock('@/renderer/components/chat/CommandQueuePanel', () => ({
  default: ({ onEdit }: { onEdit?: (item: NonNullable<SpecialistAsideFixture['item']>) => void }) =>
    specialistAsideFixture.active && specialistAsideFixture.item ? (
      <button type='button' onClick={() => onEdit?.(specialistAsideFixture.item!)}>
        edit frozen queue
      </button>
    ) : null,
}));
vi.mock('@/renderer/hooks/xScience/runtime/useSessionSpecialist', async (original) => {
  const actual = await original<typeof import('@/renderer/hooks/xScience/runtime/useSessionSpecialist')>();
  return {
    ...actual,
    useSessionSpecialist: (...args: Parameters<typeof actual.useSessionSpecialist>) =>
      specialistAsideFixture.active
        ? {
            applied: null,
            desired: null,
            choices: [],
            pending: false,
            available: true,
            unavailable: false,
            error: null,
            retryable: false,
            catalogLoading: false,
            catalogError: false,
            hasLiveAttempt: false,
            prepared: false,
            change: vi.fn(),
            refresh: vi.fn(),
            retry: vi.fn(),
            current: () => true,
            capture: specialistAsideFixture.capture,
          }
        : actual.useSessionSpecialist(...args),
  };
});
vi.mock('@/renderer/components/chat/MobileActionSheet', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/components/chat/MobileActionSheet')>();
  return {
    default: ({
      entries,
      open,
      onClose,
    }: {
      open?: boolean;
      onClose?: () => void;
      entries?: Array<{
        key: string;
        submenu?: {
          onSelect?: (value: string) => void;
        };
      }>;
    }) => {
      mobileActionSheetEntries.current = entries ?? [];
      mobileActionSheetOpen.current = open ?? false;
      if (realMobileActionSheet.current) {
        return (
          <actual.default
            entries={(entries ?? []) as React.ComponentProps<typeof actual.default>['entries']}
            open={open ?? false}
            onClose={onClose ?? (() => undefined)}
          />
        );
      }
      return null;
    },
    useAttachEntry: () => ({ entries: [], hiddenFileInput: null }),
  };
});
vi.mock('@/renderer/components/chat/ThoughtDisplay', () => ({
  default: () => null,
}));
vi.mock('@/renderer/components/media/FileAttachButton', () => ({
  default: (props: Record<string, unknown>) => {
    fileAttachProps.current = props;
    return null;
  },
}));
vi.mock('@/renderer/components/media/FilePreview', () => ({
  default: () => null,
}));
vi.mock('@/renderer/components/media/HorizontalFileList', () => ({
  default: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceComposerCapabilities: (...args: unknown[]) => loadComposerCapabilitiesMock(...args),
  loadXScienceProjectArtifacts: (...args: unknown[]) => loadProjectArtifactsMock(...args),
  loadXScienceProjects: async () => [],
}));
vi.mock('@/renderer/hooks/xScience/runtime/useAcpModelInfo', () => ({
  useAcpModelInfo: (...args: unknown[]) => useAcpModelInfoMock(...args),
}));
vi.mock('@/renderer/hooks/xScience/runtime/useAcpConfigOptions', () => ({
  classifyConfigSetError: () => 'unknown',
  useAcpConfigOptions: (...args: unknown[]) => useAcpConfigOptionsMock(...args),
}));
// The mocked draft hook keeps content in React state so typing flows through
// the composer like it does in production; tests seed it via
// sendBoxDraftSeed before rendering.
const sendBoxDraftSeed = { current: '' };
const memoryRetryDraft = {
  initial: undefined as StagedSessionOptions | undefined,
  replace: null as ((value: StagedSessionOptions) => void) | null,
};
vi.mock('@/renderer/hooks/chat/useSendBoxDraft', () => ({
  getSendBoxDraftHook: () => () => {
    const [draft, setDraft] = React.useState({
      content: sendBoxDraftSeed.current,
      contextItems: draftContextItemsMock.current,
      stagedSessionOptions: memoryRetryDraft.initial,
    });
    const draftRef = React.useRef(draft);
    draftRef.current = draft;
    memoryRetryDraft.replace = (value) => {
      const next = { ...draftRef.current, stagedSessionOptions: value };
      draftRef.current = next;
      setDraft(next);
    };
    if (specialistAsideFixture.active) specialistAsideFixture.draft = draft;
    return {
      saveFailed: false,
      retrySave: vi.fn(),
      data: {
        atPath: [],
        uploadFile: [],
        ...draft,
      },
      mutate: (updater: (current: typeof draft) => typeof draft | undefined) => {
        const next = updater(draftRef.current);
        if (next !== undefined) {
          draftRef.current = next;
          setDraft(next);
        }
      },
      mutatePersistedIf: (updater: (current: typeof draft) => typeof draft | undefined) => {
        const current = draftRef.current;
        const next = updater(current) ?? current;
        if (next !== current) {
          draftRef.current = next;
          setDraft(next);
        }
        return { available: true, draft: next };
      },
      replaceContentIfUnchanged: (expected: string, replacement: string) => {
        if (draft.content !== expected) return false;
        setDraft((current) => (current.content === expected ? { ...current, content: replacement } : current));
        return true;
      },
    };
  },
}));
vi.mock('@/renderer/hooks/chat/useSendBoxFiles', () => ({
  useSendBoxFiles: () => ({
    handleFilesAdded: vi.fn(),
    clearFiles: clearFilesMock,
  }),
  createSetUploadFile: () => vi.fn(),
}));
vi.mock('@/renderer/hooks/chat/useAutoTitle', () => ({
  useAutoTitle: () => ({
    checkAndUpdateTitle: vi.fn(),
  }),
}));
vi.mock('@/renderer/hooks/context/ConversationContext', () => ({
  useConversationContextSafe: () => mobilePlusProject.current,
}));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({
  useLayoutContext: () => ({ isMobile: isMobileMock.current }),
}));
vi.mock('@/renderer/hooks/file/useOpenFileSelector', () => ({
  useOpenFileSelector: () => ({
    openFileSelector: vi.fn(),
    onSlashBuiltinCommand: vi.fn(),
  }),
}));
vi.mock('@/renderer/pages/conversation/Messages/hooks', () => ({
  useAddOrUpdateMessage: () => addOrUpdateMessageMock,
  useMessageList: () => messageListMock.current,
  useUpdateMessageList: () => updateMessageListMock,
  useRemoveMessageByMsgId: () => removeMessageByMsgIdMock,
}));
vi.mock('@/renderer/pages/conversation/platforms/useConversationCommandQueue', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/renderer/pages/conversation/platforms/useConversationCommandQueue')>();
  return {
    createQueuedCommandItem: (input: Record<string, unknown>) => ({
      id: `command-${++queuedCommandSequence}`,
      ...input,
    }),
    shouldEnqueueConversationCommand: (input: Parameters<typeof actual.shouldEnqueueConversationCommand>[0]) =>
      realContextDropdown.current ? actual.shouldEnqueueConversationCommand(input) : false,
    useConversationCommandQueue: () => ({
      items: [],
      isPaused: false,
      isInteractionLocked: false,
      hasPendingCommands: false,
      enqueue: compactionEnqueueMock,
      remove: (id: string) => {
        specialistAsideFixture.remove(id);
        specialistAsideFixture.item = null;
        return true;
      },
      clear: vi.fn(),
      reorder: vi.fn(),
      pause: vi.fn(),
      resume: vi.fn(),
      lockInteraction: vi.fn(),
      unlockInteraction: vi.fn(),
      resetActiveExecution: vi.fn(),
      getQueuedItem: () => null,
    }),
  };
});
vi.mock('@/renderer/services/xScienceConversationBranches', () => ({
  getSelectedXScienceBranch: (...args: unknown[]) => getSelectedBranchMock(...args),
  getXScienceBranchSelectionRevision: (...args: unknown[]) => getBranchRevisionMock(...args),
  loadXScienceConversationBranches: (...args: unknown[]) => loadBranchStateMock(...args),
  selectXScienceBranch: (...args: unknown[]) => selectBranchMock(...args),
}));
vi.mock('@/renderer/pages/conversation/runtime/useConversationRuntimeView', async (original) => {
  const actual = await original<typeof import('@/renderer/pages/conversation/runtime/useConversationRuntimeView')>();
  return {
    useConversationRuntimeView: (...args: Parameters<typeof actual.useConversationRuntimeView>) =>
      realPreparationRuntime.current ? actual.useConversationRuntimeView(...args) : runtimeViewMock,
  };
});
vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  useRealtime: () => ({ runtime: {}, snapshot: { identity: 'authenticated', status: 'connected' } }),
}));
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/pages/conversation/Preview/context/PreviewContext')>();
  return {
    ...actual,
    usePreviewContext: () => actual.useOptionalPreviewContext() ?? { setSendBoxHandler: setSendBoxHandlerMock },
  };
});
vi.mock('@/renderer/services/FileService', () => ({
  allSupportedExts: [],
}));
vi.mock('@/renderer/services/xScienceSessionOptions', async (original) => {
  const actual = await original<typeof import('@/renderer/services/xScienceSessionOptions')>();
  return {
    ...actual,
    X_SCIENCE_SESSION_DEFAULTS: actual.X_SCIENCE_SESSION_DEFAULTS,
    createXScienceSessionDefaults: (targetAgent = 'OPERON') => ({
      delegation: false,
      autoReview: false,
      memory: false,
      targetAgent,
      goalText: null,
      asRoutine: false,
    }),
    loadXScienceSessionOptions: (...args: Parameters<typeof actual.loadXScienceSessionOptions>) =>
      realReviewAuthority.current ? actual.loadXScienceSessionOptions(...args) : loadSessionOptionsMock(...args),
    loadXScienceSessionMemoryPreference: (...args: unknown[]) => loadSessionMemoryMock(...args),
    loadXScienceSessionAutoReviewPreference: (
      ...args: Parameters<typeof actual.loadXScienceSessionAutoReviewPreference>
    ) =>
      realReviewAuthority.current
        ? actual.loadXScienceSessionAutoReviewPreference(...args)
        : loadSessionReviewMock(...args),
    toXScienceMessageSessionOptions: (
      ...[value, overrides]: Parameters<typeof actual.toXScienceMessageSessionOptions>
    ) =>
      specialistAsideFixture.active || realMemoryAuthority.current || realReviewAuthority.current
        ? actual.toXScienceMessageSessionOptions(value, overrides)
        : {
            ...value,
            ...(overrides?.planMode === undefined ? {} : { plan_mode: overrides.planMode }),
            ...(overrides?.targetBranchId ? { target_branch_id: overrides.targetBranchId } : {}),
            ...(overrides?.expectedBranchId ? { expected_branch_id: overrides.expectedBranchId } : {}),
            ...(overrides?.expectedGeneration ? { expected_generation: overrides.expectedGeneration } : {}),
          },
    updateXScienceSessionConfig: (...args: Parameters<typeof actual.updateXScienceSessionConfig>) =>
      realReviewAuthority.current ? actual.updateXScienceSessionConfig(...args) : updateSessionConfigMock(...args),
  };
});
vi.mock('@/renderer/services/xScienceCatalog', async (original) => ({
  ...(await original<typeof import('@/renderer/services/xScienceCatalog')>()),
  loadXScienceAssistants: (...args: unknown[]) => loadAssistantsMock(...args),
}));
vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: (...args: unknown[]) => loadComputeProvidersMock(...args),
  loadXScienceSessionComputeProviders: (...args: unknown[]) => loadSessionComputeProvidersMock(...args),
  setXScienceSessionComputeProvider: (...args: unknown[]) => setSessionComputeProviderMock(...args),
}));
vi.mock('@/renderer/services/runtime/computeSessionState', () => ({
  loadXScienceSessionComputeState: async (...args: unknown[]) => {
    const enabled = (await loadSessionComputeProvidersMock(...args)) as string[];
    return {
      enabled,
      configured: true,
      selectionSource: 'explicit',
      softwareRuntime:
        enabled.length === 0
          ? { kind: 'local_explicit' }
          : enabled.length > 1
            ? { kind: 'invalid_selection' }
            : { kind: 'remote_unsupported', providerName: enabled[0] },
    };
  },
}));
vi.mock('@/renderer/utils/emitter', () => ({
  emitter: {
    emit: (...args: unknown[]) => emitterEmitMock(...args),
    on: vi.fn(),
    off: vi.fn(),
  },
  useAddEventListener: vi.fn(),
}));
vi.mock('@/renderer/utils/file/fileSelection', () => ({
  mergeFileSelectionItems: vi.fn(),
}));
vi.mock('@/renderer/utils/file/messageFiles', () => ({
  buildDisplayMessage: (input: string) => input,
}));
vi.mock('@/renderer/pages/conversation/platforms/acp/useAcpInitialMessage', () => ({
  useAcpInitialMessage: vi.fn(() => ({ failed: false, retry: vi.fn() })),
}));
vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    Input: ({
      value,
      onChange,
      placeholder,
    }: {
      value?: string;
      onChange?: (value: string) => void;
      placeholder?: string;
    }) => <input aria-label={placeholder} value={value ?? ''} onChange={(event) => onChange?.(event.target.value)} />,
    Trigger: ({
      children,
      popup,
      popupVisible,
      onVisibleChange,
    }: {
      children?: React.ReactNode;
      popup?: React.ReactNode | (() => React.ReactNode);
      popupVisible?: boolean;
      onVisibleChange?: (visible: boolean) => void;
    }) => (
      <div onMouseEnter={() => onVisibleChange?.(true)} onMouseLeave={() => onVisibleChange?.(false)}>
        {children}
        {popupVisible ? (typeof popup === 'function' ? popup() : popup) : null}
      </div>
    ),
    Spin: ({ children }: { children?: React.ReactNode }) => <span>{children}</span>,
    Popover: ({
      children,
      content,
      popupVisible,
    }: {
      children?: React.ReactNode;
      content?: React.ReactNode;
      popupVisible?: boolean;
    }) => (
      <>
        {children}
        {popupVisible ? content : null}
      </>
    ),
    Dropdown: (props: React.ComponentProps<typeof actual.Dropdown>) =>
      realContextDropdown.current || specialistAsideFixture.active ? (
        <actual.Dropdown {...props} />
      ) : (
        <>
          {props.children}
          {props.popupVisible ? props.droplist : null}
        </>
      ),
    Tooltip: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
    Modal: ({
      visible,
      title,
      children,
      onOk,
      onCancel,
      okText,
      cancelText,
      okButtonProps,
    }: {
      visible?: boolean;
      title?: React.ReactNode;
      children?: React.ReactNode;
      onOk?: () => void;
      onCancel?: () => void;
      okText?: React.ReactNode;
      cancelText?: React.ReactNode;
      okButtonProps?: { disabled?: boolean };
    }) =>
      visible ? (
        <div role='dialog' aria-label={typeof title === 'string' ? title : undefined}>
          {children}
          {onCancel && (
            <button type='button' onClick={onCancel}>
              {cancelText ?? 'Cancel'}
            </button>
          )}
          {onOk && (
            <button type='button' disabled={okButtonProps?.disabled} onClick={onOk}>
              {okText ?? 'OK'}
            </button>
          )}
        </div>
      ) : null,
    Button: ({
      children,
      disabled,
      onClick,
      'aria-label': ariaLabel,
    }: {
      children?: React.ReactNode;
      disabled?: boolean;
      onClick?: () => void;
      'aria-label'?: string;
    }) => (
      <button type='button' aria-label={ariaLabel} disabled={disabled} onClick={onClick}>
        {children}
      </button>
    ),
    Message: {
      warning: (...args: unknown[]) => specialistAsideFixture.warning(...args),
      success: (...args: unknown[]) => messageSuccessMock(...args),
      error: (...args: unknown[]) => messageErrorMock(...args),
      useMessage: () => [{ success: vi.fn(), error: vi.fn() }, null],
    },
    Tag: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  };
});

const makeMessageState = (): UseAcpMessageReturn => ({
  thought: { subject: '', description: '' },
  setThought: vi.fn(),
  running: true,
  hasHydratedRunningState: true,
  acpStatus: null,
  aiProcessing: false,
  setAiProcessing: setAiProcessingMock,
  resetState: resetStateMock,
  tokenUsage: null,
  hasThinkingMessage: false,
  slashCommands: [],
  fetchSlashCommands: vi.fn(),
});

const acceptSkillTaskSend = async () => {
  runtimeViewMock.isProcessing = true;
  runtimeViewMock.canSendMessage = false;
  runtimeViewMock.state = 'running';
  runtimeViewMock.view.taskStatus = 'running';
  return {
    msg_id: 'skill-control-message',
    turn_id: 'skill-control-turn',
    runtime: { state: 'running', is_processing: true },
  };
};

const manualCompactionPath = '/api/conversations/compaction-child/context-compactions';
async function renderManualCompaction(
  capability = manualCompactionSnapshot(),
  usage = manualCompactionUsage(),
  respond?: (method: string, path: string, body: unknown) => unknown | Promise<unknown>,
  busy = false,
  reading?: { context: ReturnType<typeof pdfContext>; observer: React.ReactNode }
) {
  realContextDropdown.current = true;
  setRendererAccountOwner(manualCompactionIdentity.ownerId);
  runtimeViewMock.isProcessing = busy;
  runtimeViewMock.canSendMessage = !busy;
  runtimeViewMock.state = busy ? 'running' : 'idle';
  runtimeViewMock.view.taskStatus = busy ? 'running' : 'finished';
  const messageState = makeMessageState();
  messageState.running = false;
  sendMessageInvokeMock.mockResolvedValue({
    msg_id: 'ordinary-next-input',
    turn_id: 'ordinary-next-turn',
    runtime: { state: 'running', is_processing: true },
  });
  const usageFetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (!/\/context-usage(?:\?inspect=1)?$/.test(String(input))) throw new Error('Unexpected C11 usage request');
    return new Response(JSON.stringify(usage), { status: 200 });
  });
  const transport = vi.spyOn(httpBridge, 'httpRequest').mockImplementation(async (method, path, body) => {
    if (
      reading &&
      method === 'GET' &&
      path === `/api/conversations/${manualCompactionIdentity.frameId}/reading-context`
    )
      return reading.context as never;
    if (respond) return (await respond(method, path, body)) as never;
    if (method === 'GET' && path.split('?')[0] === manualCompactionPath) return capability as never;
    if (method === 'POST' && path === manualCompactionPath)
      return manualCompactionReceipt(body as typeof manualCompactionRequest) as never;
    throw new Error(`Unexpected C11 control request: ${method} ${path}`);
  });
  const restore = () => {
    transport.mockRestore();
    usageFetch.mockRestore();
    setRendererAccountOwner(null);
    realContextDropdown.current = false;
    realPreparationRuntime.current = false;
  };
  try {
    const composer = (
      <AcpSendBox
        conversation_id={manualCompactionIdentity.frameId}
        ownerId={manualCompactionIdentity.ownerId}
        backend='xscience'
        messageState={messageState}
        initialConversation={
          {
            id: manualCompactionIdentity.frameId,
            type: 'acp',
            name: 'Manual context compaction',
            extra: {
              backend: 'xscience',
              root_frame_id: manualCompactionIdentity.rootFrameId,
              frame_incarnation_id: manualCompactionIdentity.frameIncarnationId,
            },
          } as TChatConversation
        }
      />
    );
    const view = await render(
      reading ? (
        <PreviewProvider>
          {reading.observer}
          {composer}
        </PreviewProvider>
      ) : (
        composer
      )
    );
    await act(async () => {
      await view.i18n.changeLanguage('en-US');
    });
    return {
      view,
      transport,
      dispose: () => {
        view.unmount();
        restore();
      },
    };
  } catch (error) {
    restore();
    throw error;
  }
}

describe('AcpSendBox', () => {
  it.each(['ordinary', 'save-as-skill'] as const)(
    'real preparation controls expose Stop after a completed attempt for %s and withdraw its exact send',
    async (kind) => {
      realPreparationRuntime.current = true;
      const conversationId = 'conv-real-preparing';
      const completedRuntime: TConversationRuntimeSummary = {
        state: 'completed', can_send_message: true, has_task: false, task_status: 'finished',
        is_processing: false, pending_confirmations: 0, turn_id: conversationId,
      };
      preparationSnapshotMock.mockResolvedValue({
        frameId: conversationId, rootFrameId: conversationId, status: 'completed',
        runtimeAttempt: 47, runtimeInputRevision: 47, pendingInputRequests: [],
        canCancel: false, canResume: false, taskPlan: null, planApproval: null,
        runtimeActive: false, runtimeTaskActive: false, runtimeStage: null,
      });
      const branch = createDeferred<{ activeBranchId: string; generation: number; branches: Array<{ id: string }> }>();
      getSelectedBranchMock.mockReturnValue('branch-preparing');
      loadBranchStateMock.mockReturnValueOnce(branch.promise);
      sendBoxDraftSeed.current = 'Keep the original draft.';
      sendMessageInvokeMock.mockReset();
      await render(<AcpSendBox conversation_id={conversationId} backend='xscience'
        initialConversation={{ id: conversationId, type: 'acp', runtime: completedRuntime, extra: {} } as TChatConversation}
        messageState={makeMessageState()} />);
      await waitFor(() => expect(screen.getByText('任务已完成')).toBeInTheDocument());
      let saving: Promise<void> | undefined;
      try {
        await act(async () => {
          if (kind === 'ordinary') fireEvent.click(screen.getByRole('button', { name: 'send' }));
          else {
            const save = fileAttachProps.current?.onSaveSkill;
            if (typeof save !== 'function') throw new Error('Missing Save-as-Skill entry');
            saving = save() as Promise<void>;
          }
        });
        await waitFor(() => expect(loadBranchStateMock).toHaveBeenCalledOnce());
        expect(runtimeStore.getConversationRuntimeViewSnapshot(conversationId).localSubmitting).toBe(true);
        expect(sendMessageInvokeMock).not.toHaveBeenCalled();
        const stop = screen.getByRole('button', { name: '暂停任务' });
        fireEvent.click(stop);
        await waitFor(() =>
          expect(runtimeStore.getConversationRuntimeViewSnapshot(conversationId).localSubmitting).toBe(false)
        );
        await act(async () => {
          branch.resolve({ activeBranchId: 'branch-main', generation: 1, branches: [{ id: 'branch-preparing' }] });
          await branch.promise;
          await saving;
        });
        expect(sendMessageInvokeMock).not.toHaveBeenCalled();
        expect(stopInvokeMock).not.toHaveBeenCalled();
        expect(clearFilesMock).not.toHaveBeenCalled();
        expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep the original draft.');
        if (kind === 'save-as-skill') expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(false);
      } finally {
        branch.resolve({ activeBranchId: 'branch-main', generation: 1, branches: [{ id: 'branch-preparing' }] });
        await saving;
      }
    }
  );

  it.each(['ordinary', 'save-as-skill'] as const)(
    'pre-dispatch Stop prevents %s branch preparation from sending after Stop returns',
    async (kind) => {
      runtimeViewMock.isProcessing = false;
      runtimeViewMock.canSendMessage = true;
      runtimeViewMock.activeTurnId = null;
      runtimeViewMock.state = 'idle';
      runtimeViewMock.view.taskStatus = 'finished';
      sendBoxDraftSeed.current = 'Keep the original draft.';
      getSelectedBranchMock.mockReturnValue('branch-preparing');
      const branch = createDeferred<{ activeBranchId: string; generation: number; branches: Array<{ id: string }> }>();
      loadBranchStateMock.mockReturnValueOnce(branch.promise);
      sendMessageInvokeMock.mockReset().mockImplementation(acceptSkillTaskSend);
      await render(
        <AcpSendBox conversation_id='conv-stop-preparing' backend='xscience' messageState={makeMessageState()} />
      );
      let saving: Promise<void> | undefined;
      await act(async () => {
        if (kind === 'ordinary') fireEvent.click(screen.getByRole('button', { name: 'send' }));
        else {
          const save = fileAttachProps.current?.onSaveSkill;
          if (typeof save !== 'function') throw new Error('Missing Save-as-Skill entry');
          saving = save() as Promise<void>;
        }
      });
      await waitFor(() => expect(loadBranchStateMock).toHaveBeenCalledOnce());
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(runtimeOperationsProps.current?.onStop).toBeTypeOf('function');
      await act(async () => {
        await runtimeOperationsProps.current?.onStop?.();
        await runtimeOperationsProps.current?.onStop?.();
      });
      if (kind === 'ordinary') fireEvent.click(screen.getByRole('button', { name: 'change' }));
      await act(async () => {
        branch.resolve({ activeBranchId: 'branch-main', generation: 1, branches: [{ id: 'branch-preparing' }] });
        await branch.promise;
        await saving;
      });
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(clearFilesMock).not.toHaveBeenCalled();
      expect(screen.getByTestId('sendbox-draft')).toHaveTextContent(
        kind === 'ordinary' ? 'hello' : 'Keep the original draft.'
      );
      if (kind === 'save-as-skill') {
        expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(false);
        fireEvent.click(screen.getByRole('button', { name: 'change' }));
        expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('hello');
      }
    }
  );

  it.each(['ordinary', 'save-as-skill'] as const)(
    'pre-dispatch Stop prevents %s final maintenance preparation from sending after Stop returns',
    async (kind) => {
      sendMessageInvokeMock.mockReset();
      sendBoxDraftSeed.current = 'Keep the maintenance draft.';
      let snapshot = manualCompactionSnapshot();
      const cancelled = {
        ...manualCompactionReceipt(),
        state: 'cancelled',
        cancellation_status: 'cancelled',
        execution_observation: 'settled',
        can_submit_next_input: true,
        terminal_event_id: 25,
      };
      const cancellation = createDeferred<typeof cancelled>();
      const harness = await renderManualCompaction(snapshot, manualCompactionUsage(), (method, path, body) => {
        if (method === 'GET' && path === manualCompactionPath) return snapshot;
        if (method === 'DELETE') {
          expect(path).toBe(`${manualCompactionPath}/by-request/${cancelled.request.request_id}`);
          expect(body).toEqual(cancelled.request);
          return cancellation.promise;
        }
        if (method === 'GET' && path.includes('/by-request/')) return snapshot.operation;
        throw new Error(`Unexpected maintenance Stop request: ${method} ${path}`);
      });
      const controller = getContextCompactionController(manualCompactionIdentity);
      if (!controller) throw new Error('Missing original maintenance controller');
      const preparation = createDeferred<boolean>();
      const prepare = vi.spyOn(controller, 'prepareForPrompt').mockReturnValueOnce(preparation.promise);
      let saving: Promise<void> | undefined;
      try {
        await act(async () => {
          if (kind === 'ordinary') fireEvent.click(screen.getByRole('button', { name: 'send' }));
          else {
            const save = fileAttachProps.current?.onSaveSkill;
            if (typeof save !== 'function') throw new Error('Missing Save-as-Skill entry');
            saving = save() as Promise<void>;
          }
        });
        await waitFor(() => expect(prepare).toHaveBeenCalledOnce());
        expect(sendMessageInvokeMock).not.toHaveBeenCalled();
        expect(runtimeOperationsProps.current?.onStop).toBeTypeOf('function');
        await act(async () => {
          snapshot = manualCompactionSnapshot(manualCompactionReceipt());
          await controller.refresh();
        });
        let stopping!: Promise<void>;
        await act(async () => {
          stopping = Promise.resolve(runtimeOperationsProps.current?.onStop?.());
        });
        await waitFor(() =>
          expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(1)
        );
        if (kind === 'ordinary') fireEvent.click(screen.getByRole('button', { name: 'change' }));
        await act(async () => {
          preparation.resolve(true);
          await preparation.promise;
          await saving;
        });
        expect(sendMessageInvokeMock).not.toHaveBeenCalled();
        expect(clearFilesMock).not.toHaveBeenCalled();
        await act(async () => {
          cancellation.resolve(cancelled);
          await stopping;
        });
        expect(controller.getSnapshot().receipt).toMatchObject({
          state: 'cancelled',
          execution_observation: 'settled',
        });
        expect(stopInvokeMock).not.toHaveBeenCalled();
        expect(screen.getByTestId('sendbox-draft')).toHaveTextContent(
          kind === 'ordinary' ? 'hello' : 'Keep the maintenance draft.'
        );
        if (kind === 'save-as-skill') {
          expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(false);
          fireEvent.click(screen.getByRole('button', { name: 'change' }));
          expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('hello');
        }
      } finally {
        preparation.resolve(true);
        cancellation.resolve(cancelled);
        prepare.mockRestore();
        harness.dispose();
      }
    }
  );

  it('pre-dispatch Stop leaves an ordinary send without cancellation on its single accepted path', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.activeTurnId = null;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendMessageInvokeMock.mockReset().mockImplementation(acceptSkillTaskSend);
    await render(
      <AcpSendBox conversation_id='conv-normal-preparing' backend='xscience' messageState={makeMessageState()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'send' }));
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledOnce());
    await waitFor(() => expect(clearFilesMock).toHaveBeenCalledOnce());
    expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledOnce();
    expect(stopInvokeMock).not.toHaveBeenCalled();
  });

  it('pre-dispatch Stop keeps an invoked send and its late acknowledgement under backend cancellation', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.activeTurnId = null;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    const response = createDeferred<{
      msg_id: string;
      turn_id: string;
      runtime: { state: string; is_processing: boolean };
    }>();
    sendMessageInvokeMock.mockReset().mockReturnValueOnce(response.promise);
    await render(
      <AcpSendBox conversation_id='conv-unknown-ack' backend='xscience' messageState={makeMessageState()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'send' }));
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledOnce());
    const sent = sendMessageInvokeMock.mock.calls[0]?.[0];
    await act(async () => {
      await runtimeOperationsProps.current?.onStop?.();
    });
    expect(runtimeViewMock.cancelSendPreparation).toHaveLastReturnedWith(false);
    expect(stopInvokeMock).toHaveBeenCalledWith({ conversation_id: 'conv-unknown-ack', turn_id: 'conv-unknown-ack' });
    await act(async () => {
      response.resolve({
        msg_id: 'accepted-late',
        turn_id: 'late-turn',
        runtime: { state: 'running', is_processing: true },
      });
      await response.promise;
    });
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
    expect(sendMessageInvokeMock.mock.calls[0]?.[0]).toBe(sent);
    expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledWith('late-turn', expect.any(Object), 'accepted-late', {
      ticket: runtimeViewMock.markSendStarted.mock.results[0]?.value,
      receipt: undefined,
    });
    expect(stopInvokeMock).toHaveBeenCalledOnce();
  });

  registerComputeRootCases({
    render,
    messageState: makeMessageState,
    mobile: isMobileMock,
    menu: sessionOptionsProps,
    entries: mobileActionSheetEntries,
    providers: loadComputeProvidersMock,
    selection: loadSessionComputeProvidersMock,
    write: setSessionComputeProviderMock,
  });
  registerSpecialistAsideCases({
    fixture: specialistAsideFixture,
    render,
    messageState: makeMessageState,
    asideAsk: askSideQuestionMock,
    send: sendMessageInvokeMock,
    prepare: () => {
      sendBoxDraftSeed.current = 'synthetic frozen draft';
      runtimeViewMock.isProcessing = false;
      runtimeViewMock.canSendMessage = true;
      runtimeViewMock.state = 'idle';
      runtimeViewMock.view.taskStatus = 'finished';
    },
  });
  it('C12 freezes the clicked reading page while its own new PDF link is preparing', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    const context = pdfContext();
    context.scope.frame_id = 'conv-c12-preparing';
    context.scope.root_frame_id = context.scope.frame_id;
    context.scope.root_frame_incarnation_id = context.scope.frame_incarnation_id;
    const source = { artifact_id: 'artifact-2', version_id: 'version-2' };
    draftContextItemsMock.current = [
      {
        kind: 'artifact',
        artifactId: source.artifact_id,
        versionId: source.version_id,
        label: 'paper-2.pdf',
        contentType: 'application/pdf',
        sizeBytes: 512,
      },
    ];
    const inspection = createDeferred<Response>();
    const readingPath = `/api/conversations/${context.scope.frame_id}/reading-context`;
    const linkRequests: unknown[] = [];
    const response = (body: unknown) =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const address = typeof input === 'string' ? input : 'url' in input ? input.url : input.href;
      const pathname = new URL(address, 'http://localhost').pathname;
      const method = init?.method ?? (typeof input === 'object' && 'method' in input ? input.method : 'GET');
      if (method === 'GET' && pathname === readingPath) return response(context);
      if (method === 'GET' && pathname.endsWith('/context-usage')) return response({ status: 'unavailable' });
      if (method === 'POST' && pathname === `${readingPath}/candidates`) return inspection.promise;
      if (method === 'POST' && pathname === `${readingPath}/link`) {
        linkRequests.push(JSON.parse(String(init?.body)));
        return response({ ...context, revision: 2, changed: true, bindings: [...context.bindings, pdfBinding('2')] });
      }
      throw new Error(`Unexpected reading preparation request: ${method} ${pathname}`);
    });
    function PreviewObservation() {
      const { pdfReadingOwner } = usePreviewContext();
      return (
        <>
          <output data-testid='reading-owner'>{pdfReadingOwner?.context?.revision}</output>
          {[4, 9].map((pageNumber) => (
            <button
              key={pageNumber}
              onClick={() =>
                pdfReadingOwner?.reportPosition(
                  { artifact_id: 'artifact-1', version_id: 'version-1' },
                  { pageNumber, pageCount: 9 }
                )
              }
            >
              observe page {pageNumber}
            </button>
          ))}
        </>
      );
    }
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: context.scope.frame_id,
      msg_id: 'canonical-prepared-input',
      runtime: { state: 'idle', is_processing: false },
    });
    let view: Awaited<ReturnType<typeof render>> | undefined;
    try {
      view = await render(
        <PreviewProvider>
          <PreviewObservation />
          <AcpSendBox
            conversation_id={context.scope.frame_id}
            ownerId='owner'
            backend='xscience'
            messageState={makeMessageState()}
          />
        </PreviewProvider>
      );
      await waitFor(() => expect(screen.getByTestId('reading-owner')).toHaveTextContent('1'));
      fireEvent.click(screen.getByRole('button', { name: 'observe page 4' }));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() =>
        expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/candidates'))).toBe(true)
      );
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button', { name: 'observe page 9' }));
      await act(async () => {
        inspection.resolve(
          response({
            schema: 'x-science.pdf-reading-candidates.v1',
            scope: context.scope,
            revision: 1,
            candidates: [{ source, eligible: true, page_count: 9, size_bytes: 512, checksum: 'a'.repeat(64) }],
          })
        );
        await inspection.promise;
      });
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(linkRequests).toEqual([expect.objectContaining({ expected_revision: 1, sources: [source] })]);
      expect(sendMessageInvokeMock.mock.calls[0][0]).toMatchObject({
        reading_context: {
          expected_revision: 2,
          binding_ids: ['binding-1', 'binding-2'],
          active_binding_id: 'binding-1',
          page_number: 4,
        },
      });
    } finally {
      view?.unmount();
      fetchMock.mockRestore();
    }
  });

  it('C12 sends reading selection on ordinary input but never attaches it to Save-as-Skill control', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    const context = pdfContext();
    context.scope.frame_id = 'conv-c12-intents';
    context.scope.root_frame_id = context.scope.frame_id;
    context.scope.root_frame_incarnation_id = context.scope.frame_incarnation_id;
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const address = typeof input === 'string' ? input : 'url' in input ? input.url : input.href;
      const pathname = new URL(address, 'http://localhost').pathname;
      const method = init?.method ?? (typeof input === 'object' && 'method' in input ? input.method : 'GET');
      const readingPath = `/api/conversations/${context.scope.frame_id}/reading-context`;
      const usagePath = `/api/conversations/${context.scope.frame_id}/context-usage`;
      if (method !== 'GET' || (pathname !== readingPath && pathname !== usagePath)) {
        throw new Error(`Unexpected C12 fixture request: ${method} ${pathname}`);
      }
      const body = pathname === readingPath ? context : { status: 'unavailable' };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: 'conv-c12-intents',
      msg_id: 'canonical-input',
      runtime: { state: 'idle', is_processing: false },
    });
    try {
      await render(
        <AcpSendBox
          conversation_id='conv-c12-intents'
          ownerId='owner'
          backend='xscience'
          messageState={makeMessageState()}
        />
      );
      await screen.findByTestId('pdf-context-bar');
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(sendMessageInvokeMock.mock.calls[0][0]).toMatchObject({
        reading_context: {
          schema: 'x-science.pdf-reading-selection.v1',
          expected_revision: 1,
          binding_ids: ['binding-1'],
        },
      });
      const action = fileAttachProps.current?.onSaveSkill;
      if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');
      await act(async () => {
        await action();
      });
      expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2);
      expect(sendMessageInvokeMock.mock.calls[1][0]).toMatchObject({ turn_intent: 'save-as-skill' });
      expect(sendMessageInvokeMock.mock.calls[1][0]).not.toHaveProperty('reading_context');
      expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    } finally {
      fetchMock.mockRestore();
    }
  });

  it('C12 restores the canonical linked PDF bar without sending a message', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    const scope = {
      frame_id: 'conv-reading',
      frame_incarnation_id: 'frame-reading',
      root_frame_id: 'conv-reading',
      root_frame_incarnation_id: 'frame-reading',
      project_id: 'project-reading',
    };
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async () =>
        new Response(
          JSON.stringify({
            schema: 'x-science.pdf-reading-context.v1',
            scope,
            revision: 1,
            changed: false,
            bindings: [
              {
                binding_id: 'binding-reading',
                source_kind: 'artifact-version',
                artifact_id: 'artifact-reading',
                version_id: 'version-reading',
                name: 'reading.pdf',
                mime_type: 'application/pdf',
                size_bytes: 100,
                checksum: 'a'.repeat(64),
                page_count: 2,
                linked_at: '2026-10-18T00:00:00Z',
              },
            ],
          }),
          { status: 200, headers: { 'content-type': 'application/json' } }
        )
    );
    try {
      await render(
        <AcpSendBox
          conversation_id='conv-reading'
          ownerId='owner-reading'
          backend='xscience'
          messageState={makeMessageState()}
        />
      );
      expect(await screen.findByTestId('pdf-context-bar')).toHaveTextContent('reading.pdf');
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    } finally {
      fetchMock.mockRestore();
    }
  });

  beforeEach(() => {
    vi.clearAllMocks();
    resetConversationRuntimeViewStoreForTest();
    specialistAsideFixture.active = false;
    specialistAsideFixture.item = null;
    specialistAsideFixture.draft = {};
    specialistAsideFixture.capture.mockReset().mockReturnValue({});
    queuedCommandSequence = 0;
    realContextDropdown.current = false;
    sendBoxDraftSeed.current = '';
    memoryRetryDraft.initial = undefined;
    memoryRetryDraft.replace = null;
    sendBoxProps.current = null;
    isMobileMock.current = false;
    mobilePlusProject.current = null;
    mobileActionSheetEntries.current = [];
    mobileActionSheetOpen.current = false;
    realMobileActionSheet.current = false;
    realAgentControls.current = false;
    fileAttachProps.current = null;
    draftContextItemsMock.current = [];
    sessionOptionsProps.current = null;
    messageListMock.current = [];
    runtimeOperationsProps.current = null;
    const defaultFramePlan = createDeferred<null>();
    loadFramePlanMock.mockReturnValue(defaultFramePlan.promise);
    settleDefaultFramePlan = async () => {
      defaultFramePlan.resolve(null);
      await defaultFramePlan.promise;
    };
    requestFrameAuditMock.mockResolvedValue({ accepted: true });
    const defaultSessionOptions = createDeferred<Record<string, unknown>>();
    const defaultSessionOptionsValue = {
      delegation: false,
      autoReview: false,
      memory: false,
      targetAgent: 'OPERON',
      goalText: null,
      asRoutine: false,
    };
    loadSessionOptionsMock.mockReturnValue(defaultSessionOptions.promise);
    realMemoryAuthority.current = false;
    realReviewAuthority.current = false;
    loadSessionMemoryMock.mockImplementation(async (id: string) => ({ rootFrameId: id, memory: false }));
    loadSessionReviewMock.mockImplementation(async (id: string) => ({ rootFrameId: id, autoReview: false }));
    settleDefaultSessionOptions = async () => {
      defaultSessionOptions.resolve(defaultSessionOptionsValue);
      await defaultSessionOptions.promise;
    };
    updateSessionConfigMock.mockResolvedValue(undefined);
    loadAssistantsMock.mockImplementation(() => new Promise(() => {}));
    saveConversationAsSkillMock.mockResolvedValue({ name: 'session-skill', scope: 'personal' });
    loadComputeProvidersMock.mockImplementation(() => new Promise(() => {}));
    loadSessionComputeProvidersMock.mockImplementation(() => new Promise(() => {}));
    setSessionComputeProviderMock.mockResolvedValue(undefined);
    loadComposerCapabilitiesMock.mockResolvedValue({ skills: [], mcpStatuses: [] });
    loadProjectArtifactsMock.mockResolvedValue([]);
    loadSkillLibraryMock.mockResolvedValue([]);
    getSelectedBranchMock.mockReturnValue(null);
    getBranchRevisionMock.mockReturnValue(0);
    loadBranchStateMock.mockResolvedValue({
      activeBranchId: 'branch-main',
      generation: 1,
      branches: [{ id: 'branch-main' }],
    });
    askSideQuestionMock.mockResolvedValue(undefined);
    runtimeViewMock.activeTurnId = 'turn-1';
    runtimeViewMock.markSendStarted.mockImplementation((phase?: 'preparing') => {
      const ticket = {};
      localSendStarted('composer-test-reservation', ticket, phase);
      return ticket;
    });
    runtimeViewMock.isSendPreparing.mockImplementation((ticket: object) =>
      runtimeStore.isLocalSendPreparing('composer-test-reservation', ticket)
    );
    runtimeViewMock.markSendDispatched.mockImplementation((ticket: object) =>
      runtimeStore.localSendDispatched('composer-test-reservation', ticket)
    );
    runtimeViewMock.cancelSendPreparation.mockImplementation(
      (ticket?: object) => runtimeStore.cancelLocalSendPreparation('composer-test-reservation', ticket).cancelled
    );
    runtimeViewMock.markSendFailed.mockImplementation((reason: string, ticket?: object) => {
      localSendFailed('composer-test-reservation', reason, ticket);
    });
    runtimeViewMock.isProcessing = true;
    runtimeViewMock.canSendMessage = false;
    runtimeViewMock.hydrated = true;
    runtimeViewMock.hydrationError = null;
    runtimeViewMock.state = 'running';
    runtimeViewMock.view.hasTask = true;
    runtimeViewMock.view.activeInputMessageId = null;
    runtimeViewMock.view.hasBackendRuntime = true;
    runtimeViewMock.view.taskStatus = 'running';
    resumeFrameMock.mockResolvedValue({
      snapshot: { rootFrameId: 'conv-1' },
      runtime: {
        state: 'starting',
        can_send_message: false,
        has_task: true,
        task_status: 'running',
        is_processing: true,
        pending_confirmations: 0,
        turn_id: 'turn-resumed',
      },
    });
    stopInvokeMock.mockResolvedValue({
      runtime: {
        state: 'idle',
        can_send_message: true,
        has_task: true,
        task_status: 'cancelled',
        is_processing: false,
        pending_confirmations: 0,
        turn_id: null,
      },
    });
    useAcpConfigOptionsMock.mockReturnValue({
      setStatus: { state: 'idle' },
      mode: null,
      model: null,
      thoughtLevel: null,
      reload: vi.fn(),
      setConfigOption: vi.fn(),
    });
    useAcpModelInfoMock.mockReturnValue({
      model_info: null,
      canSwitch: false,
      isModelListLoading: false,
      modelListError: null,
      retryModelList: vi.fn().mockResolvedValue(undefined),
      selectModel: vi.fn(),
    });
  });

  it('C11 starts explicit measured compaction from the real context card and closing only hides it', async () => {
    const harness = await renderManualCompaction();
    try {
      const trigger = screen.getByTestId('x-science-context-usage-trigger');
      fireEvent.click(trigger);
      expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('30.0%');
      expect(trigger.querySelector('svg')).toHaveAttribute('width', '16');
      const compact = await screen.findByRole('button', { name: 'Compact context' });
      expect(compact).toBeEnabled();
      fireEvent.click(compact);
      await waitFor(() => expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(1));
      const body = harness.transport.mock.calls.find(([method]) => method === 'POST')![2];
      expect(body).toEqual({
        schema: 'x-science.context-compaction-request.v1',
        request_id: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i),
        expected: manualCompactionTarget,
      });
      expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('30.0%');
      fireEvent.click(screen.getByTestId('context-usage-close'));
      await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
      expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(0);
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    } finally {
      harness.dispose();
    }
  });

  it('C11 uses typed queued availability rather than treating high usage as execution permission', async () => {
    const harness = await renderManualCompaction(
      manualCompactionSnapshot(null, false, 'queued'),
      manualCompactionUsage(450_000)
    );
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('45.0%');
      expect(await screen.findByRole('button', { name: 'Compact context' })).toBeDisabled();
      expect(
        harness.transport.mock.calls.some(([method, path]) => method === 'GET' && path === manualCompactionPath)
      ).toBe(true);
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
    } finally {
      harness.dispose();
    }
  });

  it('C11 recovers the original maintenance request and explicit Stop never reports a draining receipt as stopped', async () => {
    const receipt = manualCompactionReceipt();
    const capability = manualCompactionSnapshot(receipt);
    const draining = { ...receipt, cancellation_status: 'requested', execution_observation: 'draining' };
    const harness = await renderManualCompaction(
      capability,
      manualCompactionUsage(50_000, true),
      (method, path, body) => {
        if (method === 'GET' && path.split('?')[0] === manualCompactionPath) return capability;
        if (method === 'DELETE') {
          expect(path).toBe(`${manualCompactionPath}/by-request/${receipt.request.request_id}`);
          expect(body).toEqual(receipt.request);
          return draining;
        }
        if (method === 'GET' && path.includes('/by-request/')) return draining;
        throw new Error('Unexpected C11 recovery mutation');
      }
    );
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('5.0%');
      expect(await screen.findByRole('button', { name: 'Compacting context' })).toBeDisabled();
      await act(async () => {
        await runtimeOperationsProps.current?.onStop?.();
      });
      await waitFor(() =>
        expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(1)
      );
      expect(screen.getByRole('button', { name: 'Compacting context' })).toBeDisabled();
      expect(runtimeViewMock.markStopAcknowledged).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    } finally {
      harness.dispose();
    }
  });

  it('C11 reconciles a lost acknowledgement and explicitly retries the same UUID and complete original body', async () => {
    let submissions = 0;
    const harness = await renderManualCompaction(
      manualCompactionSnapshot(),
      manualCompactionUsage(),
      (method, path, body) => {
        if (method === 'GET' && path.split('?')[0] === manualCompactionPath) return manualCompactionSnapshot();
        if (method === 'POST' && path === manualCompactionPath) {
          if (++submissions === 1) throw new Error('Lost compaction acknowledgement');
          return manualCompactionReceipt(body as typeof manualCompactionRequest);
        }
        if (method === 'GET' && path.includes('/by-request/'))
          throw new BackendHttpError({
            method,
            path,
            status: 404,
            body: { code: 'CONTEXT_COMPACTION_NOT_FOUND' },
          });
        throw new Error('Unexpected C11 retry request');
      }
    );
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      fireEvent.click(await screen.findByRole('button', { name: 'Compact context' }));
      const retry = await screen.findByRole('button', { name: 'Retry original compaction' });
      const writes = () => harness.transport.mock.calls.filter(([method]) => method === 'POST');
      expect(writes()).toHaveLength(1);
      expect(
        harness.transport.mock.calls.some(([method, path]) => method === 'GET' && path.includes('/by-request/'))
      ).toBe(true);
      fireEvent.click(retry);
      await waitFor(() => expect(writes()).toHaveLength(2));
      expect(writes()[1][2]).toEqual(writes()[0][2]);
      expect(stopInvokeMock).not.toHaveBeenCalled();
    } finally {
      harness.dispose();
    }
  });

  it('C11 waits for the exact maintenance drain receipt before admitting a new ordinary prompt', async () => {
    const receipt = manualCompactionReceipt();
    const capability = manualCompactionSnapshot(receipt);
    const drain = createDeferred<ReturnType<typeof manualCompactionReceipt>>();
    const harness = await renderManualCompaction(capability, manualCompactionUsage(), (method, path, body) => {
      if (method === 'GET' && path.split('?')[0] === manualCompactionPath) return capability;
      if (method === 'DELETE') {
        expect(path).toBe(`${manualCompactionPath}/by-request/${receipt.request.request_id}`);
        expect(body).toEqual(receipt.request);
        return drain.promise;
      }
      throw new Error('Unexpected C11 preemption request');
    });
    try {
      await waitFor(() =>
        expect(
          harness.transport.mock.calls.some(([method, path]) => method === 'GET' && path === manualCompactionPath)
        ).toBe(true)
      );
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() =>
        expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(1)
      );
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
      await act(async () => {
        drain.resolve({
          ...receipt,
          state: 'cancelled',
          cancellation_status: 'cancelled',
          terminal_event_id: 43,
          execution_observation: 'settled',
          can_submit_next_input: true,
        });
        await drain.promise;
      });
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
    } finally {
      harness.dispose();
    }
  });

  it('C11 C12 freezes the reading selection before waiting for the exact maintenance drain', async () => {
    const receipt = manualCompactionReceipt();
    const context = pdfContext();
    context.scope.frame_id = manualCompactionIdentity.frameId;
    context.scope.frame_incarnation_id = manualCompactionIdentity.frameIncarnationId;
    context.scope.root_frame_id = manualCompactionIdentity.rootFrameId;
    context.scope.root_frame_incarnation_id = manualCompactionTarget.root_frame_incarnation_id;
    const drain = createDeferred<ReturnType<typeof manualCompactionReceipt>>();
    function ObservePage() {
      const { pdfReadingOwner } = usePreviewContext();
      return (
        <>
          <output data-testid='combined-reading-owner'>{pdfReadingOwner?.context?.revision}</output>
          {[4, 9].map((pageNumber) => (
            <button
              key={pageNumber}
              onClick={() =>
                pdfReadingOwner?.reportPosition(
                  { artifact_id: 'artifact-1', version_id: 'version-1' },
                  { pageNumber, pageCount: 9 }
                )
              }
            >
              combined page {pageNumber}
            </button>
          ))}
        </>
      );
    }
    const harness = await renderManualCompaction(
      manualCompactionSnapshot(receipt),
      manualCompactionUsage(),
      (method, path, body) => {
        if (method === 'GET' && path === manualCompactionPath) return manualCompactionSnapshot(receipt);
        if (method === 'DELETE' && path === `${manualCompactionPath}/by-request/${receipt.request.request_id}`) {
          expect(body).toEqual(receipt.request);
          return drain.promise;
        }
        throw new Error(`Unexpected combined control request: ${method} ${path}`);
      },
      true,
      { context, observer: <ObservePage /> }
    );
    try {
      await waitFor(() => expect(screen.getByTestId('combined-reading-owner')).toHaveTextContent('1'));
      await waitFor(() =>
        expect(
          harness.transport.mock.calls.some(([method, path]) => method === 'GET' && path === manualCompactionPath)
        ).toBe(true)
      );
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      expect(await screen.findByRole('button', { name: 'Compacting context' })).toBeDisabled();
      fireEvent.click(screen.getByTestId('context-usage-close'));
      fireEvent.click(screen.getByRole('button', { name: 'combined page 4' }));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() =>
        expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(1)
      );
      fireEvent.click(screen.getByRole('button', { name: 'combined page 9' }));
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(compactionEnqueueMock).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
      await act(async () => {
        drain.resolve({
          ...receipt,
          state: 'cancelled',
          cancellation_status: 'cancelled',
          execution_observation: 'settled',
          can_submit_next_input: true,
        });
        await drain.promise;
      });
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(sendMessageInvokeMock.mock.calls[0][0]).toMatchObject({
        reading_context: {
          expected_revision: 1,
          binding_ids: ['binding-1'],
          active_binding_id: 'binding-1',
          page_number: 4,
        },
      });
      expect(compactionEnqueueMock).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
    } finally {
      harness.dispose();
    }
  });

  it('C11 preempts exact busy maintenance before the real queue decision and does not duplicate a waiting prompt', async () => {
    const receipt = manualCompactionReceipt();
    const drain = createDeferred<ReturnType<typeof manualCompactionReceipt>>();
    const harness = await renderManualCompaction(
      manualCompactionSnapshot(receipt),
      manualCompactionUsage(),
      (method, path) => {
        if (method === 'GET' && path === manualCompactionPath) return manualCompactionSnapshot(receipt);
        if (method === 'DELETE') return drain.promise;
        throw new Error('Unexpected busy maintenance request');
      },
      true
    );
    try {
      await waitFor(() => expect(harness.transport.mock.calls.some(([method]) => method === 'GET')).toBe(true));
      const send = screen.getByRole('button', { name: 'send' });
      fireEvent.click(send);
      fireEvent.click(send);
      await waitFor(() =>
        expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(1)
      );
      expect(compactionEnqueueMock).not.toHaveBeenCalled();
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      await act(async () => {
        drain.resolve({
          ...receipt,
          state: 'cancelled',
          cancellation_status: 'cancelled',
          execution_observation: 'settled',
          can_submit_next_input: true,
        });
        await drain.promise;
      });
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(compactionEnqueueMock).not.toHaveBeenCalled();
      expect(runtimeViewMock.markStopAcknowledged).not.toHaveBeenCalled();
    } finally {
      harness.dispose();
    }
  });

  it.each([
    [299_999, false],
    [600_000, true],
  ])('C11 hides the new action below threshold or for preflight usage (%s, %s)', async (used, preflight) => {
    const harness = await renderManualCompaction(manualCompactionSnapshot(), manualCompactionUsage(used, preflight));
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      await screen.findByTestId('context-usage-percent');
      expect(screen.queryByRole('button', { name: 'Compact context' })).not.toBeInTheDocument();
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
    } finally {
      harness.dispose();
    }
  });

  it('C11 admits a new prompt with unavailable physical observation only when safe admission is proven', async () => {
    const receipt = manualCompactionReceipt();
    const capability = manualCompactionSnapshot(receipt);
    const harness = await renderManualCompaction(capability, manualCompactionUsage(), (method, path) => {
      if (method === 'GET' && path === manualCompactionPath) return capability;
      if (method === 'DELETE')
        return {
          ...receipt,
          state: 'cancelled',
          cancellation_status: 'cancelled',
          execution_observation: 'unavailable',
          can_submit_next_input: true,
        };
      throw new Error('Unexpected C11 safe admission request');
    });
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      await screen.findByRole('button', { name: 'Compacting context' });
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(screen.getByText(/Physical execution observation unavailable/)).toBeInTheDocument();
      expect(screen.queryByText(/Execution settled/)).not.toBeInTheDocument();
      expect(runtimeViewMock.markStopAcknowledged).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
    } finally {
      harness.dispose();
    }
  });

  it('C11 sends terminal maintenance superseded by a busy ordinary input through the original queue', async () => {
    const terminal = {
      ...manualCompactionReceipt(),
      state: 'committed',
      boundary_event_id: 41,
      terminal_event_id: 42,
      execution_observation: 'unavailable',
      can_submit_next_input: false,
    };
    const original = manualCompactionSnapshot(terminal);
    const ordinary = {
      ...manualCompactionSnapshot(null, false, 'active'),
      target: { ...manualCompactionTarget, input_revision: 9, through_publication_seq: 30 },
    };
    let reads = 0;
    const harness = await renderManualCompaction(
      original,
      manualCompactionUsage(),
      (method, path) => {
        if (method === 'GET' && path === manualCompactionPath) return ++reads === 1 ? original : ordinary;
        if (method === 'GET' && path.includes('/by-request/')) return terminal;
        throw new Error('Unexpected superseded maintenance write');
      },
      true
    );
    try {
      await waitFor(() => expect(reads).toBe(1));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(compactionEnqueueMock).toHaveBeenCalledTimes(1));
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE' || method === 'POST')).toHaveLength(
        0
      );
      expect(runtimeViewMock.markStopAcknowledged).not.toHaveBeenCalled();
    } finally {
      harness.dispose();
    }
  });

  it.each(['pending', 'failed'])(
    'C11 optional bootstrap %s does not interrupt the ordinary composer send',
    async (mode) => {
      const bootstrap = createDeferred<ReturnType<typeof manualCompactionSnapshot>>();
      const harness = await renderManualCompaction(
        manualCompactionSnapshot(),
        manualCompactionUsage(),
        (method, path) => {
          if (method === 'GET' && path === manualCompactionPath) {
            if (mode === 'pending') return bootstrap.promise;
            throw new BackendHttpError({
              method,
              path,
              status: 500,
              body: { code: 'CONTEXT_COMPACTION_STORAGE_UNAVAILABLE' },
            });
          }
          throw new Error('Unexpected optional bootstrap mutation');
        }
      );
      try {
        fireEvent.click(screen.getByRole('button', { name: 'send' }));
        await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
        expect(compactionEnqueueMock).not.toHaveBeenCalled();
        expect(stopInvokeMock).not.toHaveBeenCalled();
        expect(
          harness.transport.mock.calls.filter(([method]) => method === 'DELETE' || method === 'POST')
        ).toHaveLength(0);
        fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
        expect(await screen.findByRole('button', { name: 'Compact context' })).toBeDisabled();
        if (mode === 'failed')
          expect(
            screen.getByText('Could not verify context maintenance. Retry the read; no new operation was started.')
          ).toBeInTheDocument();
      } finally {
        harness.dispose();
        bootstrap.resolve(manualCompactionSnapshot());
      }
    }
  );

  it('C11 keeps uncertain maintenance recovery reachable while preserving a nonempty draft', async () => {
    const harness = await renderManualCompaction(
      manualCompactionSnapshot(),
      manualCompactionUsage(),
      (method, path) => {
        if (method === 'GET' && path === manualCompactionPath) return manualCompactionSnapshot();
        if (method === 'POST') throw new Error('lost acknowledgement');
        if (method === 'GET' && path.includes('/by-request/'))
          throw new BackendHttpError({ method, path, status: 404, body: { code: 'CONTEXT_COMPACTION_NOT_FOUND' } });
        throw new Error('Unexpected draft recovery request');
      }
    );
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      fireEvent.click(await screen.findByRole('button', { name: 'Compact context' }));
      await screen.findByRole('button', { name: 'Retry original compaction' });
      expect(screen.getByRole('button', { name: 'Context maintenance unconfirmed' })).toBeDisabled();
      fireEvent.click(screen.getByTestId('context-usage-close'));
      fireEvent.click(screen.getByRole('button', { name: 'change' }));
      expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('hello');
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      expect(await screen.findByRole('button', { name: 'Retry original compaction' })).toBeEnabled();
      expect(screen.getByRole('button', { name: 'Check receipt' })).toBeEnabled();
      expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('hello');
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(1);
    } finally {
      harness.dispose();
    }
  });

  it('C11 ordinary Stop reaches its existing owner without waiting for optional bootstrap', async () => {
    const bootstrap = createDeferred<ReturnType<typeof manualCompactionSnapshot>>();
    const harness = await renderManualCompaction(
      manualCompactionSnapshot(),
      manualCompactionUsage(),
      (method, path) => {
        if (method === 'GET' && path === manualCompactionPath) return bootstrap.promise;
        throw new Error('Unexpected maintenance mutation for ordinary Stop');
      },
      true
    );
    try {
      await act(async () => {
        await runtimeOperationsProps.current?.onStop?.();
      });
      expect(stopInvokeMock).toHaveBeenCalledTimes(1);
      expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(0);
    } finally {
      harness.dispose();
      bootstrap.resolve(manualCompactionSnapshot());
    }
  });

  it('C11 shows retained-capacity error and retries allocation in the same composer scope without evicting unknown requests', async () => {
    setRendererAccountOwner(manualCompactionIdentity.ownerId);
    const capacity = await retainCompactionCapacity(manualCompactionIdentity.ownerId);
    const harness = await renderManualCompaction();
    try {
      fireEvent.click(screen.getByRole('button', { name: 'change' }));
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      expect(
        await screen.findByText(
          'Context maintenance session capacity reached. Reconcile pending requests in their original conversations, then retry.'
        )
      ).toBeInTheDocument();
      const retry = screen.getByRole('button', { name: 'Retry context maintenance access' });
      fireEvent.click(retry);
      expect(capacity.sessions.every((session) => session.controller.retained)).toBe(true);
      expect(capacity.read.mock.calls.some(([identity]) => identity.frameId === manualCompactionIdentity.frameId)).toBe(
        false
      );
      expect(screen.getByRole('button', { name: 'Retry context maintenance access' })).toBeInTheDocument();
      await act(async () => {
        await harness.view.i18n.changeLanguage('zh-CN');
      });
      expect(screen.getByText('上下文维护会话已达容量上限。请在原对话核对待定请求后重试。')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: '重试上下文维护入口' })).toBeInTheDocument();
      await act(async () => {
        await harness.view.i18n.changeLanguage('en-US');
      });
      await act(async () => {
        await capacity.settleOne();
      });
      fireEvent.click(screen.getByRole('button', { name: 'Retry context maintenance access' }));
      await waitFor(() =>
        expect(screen.queryByRole('button', { name: 'Retry context maintenance access' })).not.toBeInTheDocument()
      );
      await waitFor(() =>
        expect(
          capacity.read.mock.calls.filter(([identity]) => identity.frameId === manualCompactionIdentity.frameId)
        ).toHaveLength(1)
      );
      expect(capacity.sessions.slice(1).every((session) => session.controller.retained)).toBe(true);
      expect(capacity.submit).toHaveBeenCalledTimes(32);
      expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('hello');
    } finally {
      harness.dispose();
      capacity.dispose();
    }
  });

  it.each([
    ['recovery_needed', 'unavailable', 'Recovery required'],
    ['accepted', 'not_started', 'Accepted'],
    ['cancelled', 'draining', 'Waiting for execution cleanup'],
  ])(
    'C11 labels unresolved %s from the actual receipt instead of claiming compaction is running',
    async (state, observation, label) => {
      const operation = { ...manualCompactionReceipt(), state, execution_observation: observation };
      const harness = await renderManualCompaction(
        manualCompactionSnapshot(operation),
        manualCompactionUsage(50_000, true)
      );
      try {
        fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
        expect(await screen.findByRole('button', { name: label })).toBeDisabled();
        fireEvent.click(screen.getByTestId('context-usage-close'));
        fireEvent.click(screen.getByRole('button', { name: 'change' }));
        fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
        expect(await screen.findByRole('button', { name: label })).toBeDisabled();
        expect(screen.queryByRole('button', { name: 'Compacting context' })).not.toBeInTheDocument();
      } finally {
        harness.dispose();
      }
    }
  );

  it('C11 mobile uses the independent context inspector and same explicit request controller', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    const harness = await renderManualCompaction();
    try {
      fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
      fireEvent.click(await screen.findByTestId('mobile-action-sheet-context-window'));
      fireEvent.click(await screen.findByRole('button', { name: 'Compact context' }));
      await screen.findByRole('button', { name: 'Compacting context' });
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(1);
      fireEvent.click(screen.getByTestId('context-inspector-close'));
      expect(harness.transport.mock.calls.filter(([method]) => method === 'DELETE')).toHaveLength(0);
      expect(mobileActionSheetOpen.current).toBe(false);
    } finally {
      harness.dispose();
    }
  });

  it('C11 exposes exact cancellation in the existing card and keeps committed archive pending diagnostic', async () => {
    const receipt = manualCompactionReceipt();
    const capability = manualCompactionSnapshot(receipt);
    const harness = await renderManualCompaction(capability, manualCompactionUsage(), (method, path, body) => {
      if (method === 'GET' && path === manualCompactionPath) return capability;
      if (method === 'DELETE') {
        expect(body).toEqual(receipt.request);
        return {
          ...receipt,
          state: 'committed',
          cancellation_status: 'already_terminal',
          execution_observation: 'unavailable',
          can_submit_next_input: true,
          archive_status: 'pending',
        };
      }
      throw new Error('Unexpected C11 archive diagnostic request');
    });
    try {
      fireEvent.click(screen.getByTestId('x-science-context-usage-trigger'));
      fireEvent.click(await screen.findByRole('button', { name: 'Cancel context maintenance' }));
      expect(await screen.findByText('Context committed; its history archive is still pending.')).toBeInTheDocument();
      expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('30.0%');
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
      await act(async () => {
        await harness.view.i18n.changeLanguage('zh-CN');
      });
      expect(screen.getByText('上下文已提交；历史归档仍待完成。')).toBeInTheDocument();
    } finally {
      harness.dispose();
    }
  });

  it.each([false, true])('C6 scoped grants stay revocable from the running composer, mobile=%s', async (mobile) => {
    isMobileMock.current = mobile;
    realMobileActionSheet.current = mobile;
    realAgentControls.current = !mobile;
    realContextDropdown.current = !mobile;
    if (!mobile) loadSessionOptionsMock.mockRejectedValueOnce(new Error('optional session options unavailable'));
    setRendererAccountOwner(sessionGrantIdentity.ownerId);
    const modeSetter = vi.fn();
    useAcpConfigOptionsMock.mockReturnValue({
      setStatus: { state: 'idle' },
      mode: {
        id: 'mode',
        category: 'mode',
        currentValue: 'default',
        options: [{ value: 'default', label: 'Default', description: null }],
      },
      model: null,
      thoughtLevel: null,
      reload: vi.fn(),
      setConfigOption: modeSetter,
    });
    const transport = vi.spyOn(httpBridge, 'httpRequest').mockImplementation(async (method, path) => {
      if (method === 'GET' && path === '/api/conversations/grant-child/context-compactions') {
        return {
          ...manualCompactionSnapshot(null, false, 'active'),
          target: {
            ...manualCompactionTarget,
            frame_incarnation_id: sessionGrantIdentity.frameIncarnationId,
            root_frame_id: sessionGrantIdentity.rootFrameId,
            stream_uid: `frame:${sessionGrantIdentity.frameId}`,
          },
        } as never;
      }
      if (!path.startsWith('/api/conversations/grant-child/permission-grants')) {
        throw new Error(`Unexpected C6 request: ${method} ${path}`);
      }
      return (method === 'GET' ? sessionGrantsSnapshot() : sessionGrantRevokeResponse()) as never;
    });
    const grantReads = () =>
      transport.mock.calls.filter(
        ([method, path]) => method === 'GET' && path === '/api/conversations/grant-child/permission-grants'
      );
    try {
      await render(
        <AcpSendBox
          conversation_id={sessionGrantIdentity.frameId}
          ownerId={sessionGrantIdentity.ownerId}
          backend='xscience'
          messageState={makeMessageState()}
          initialConversation={
            {
              id: sessionGrantIdentity.frameId,
              type: 'acp',
              name: 'Grant child',
              extra: {
                backend: 'xscience',
                root_frame_id: sessionGrantIdentity.rootFrameId,
                frame_incarnation_id: sessionGrantIdentity.frameIncarnationId,
              },
            } as TChatConversation
          }
        />
      );
      const runtimeScope = {
        conversation_id: sessionGrantIdentity.frameId,
        frameIncarnationId: sessionGrantIdentity.frameIncarnationId,
      };
      expect(useAcpConfigOptionsMock).toHaveBeenCalledWith(expect.objectContaining(runtimeScope));
      expect(useAcpModelInfoMock).toHaveBeenCalledWith(expect.objectContaining(runtimeScope));
      if (mobile) {
        await waitFor(() => expect(grantReads()).toHaveLength(1));
        fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
        const permissionEntry = await screen.findByTestId('mobile-action-sheet-send-mode');
        expect(permissionEntry).toHaveTextContent('本会话已授权 1 项');
        fireEvent.click(permissionEntry);
      } else {
        expect(await screen.findByLabelText('本会话已授权 1 项')).toHaveTextContent('1');
        expect(screen.queryByTestId('x-science-permission-selector')).not.toBeInTheDocument();
        fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
        const review = await screen.findByRole('menuitemcheckbox', { name: '自动审阅' });
        await waitFor(() => expect(review).toBeEnabled());
        expect(loadSessionReviewMock.mock.calls.some(([id]) => id === sessionGrantIdentity.frameId)).toBe(true);
        expect(review).toHaveAttribute('aria-checked', 'false');
        expect(updateSessionConfigMock).not.toHaveBeenCalled();
      }
      expect(grantReads()).toHaveLength(1);
      fireEvent.click(await screen.findByRole('button', { name: '撤销本会话对 Python execution 的授权' }));
      await waitFor(() =>
        expect(transport).toHaveBeenCalledWith(
          'POST',
          '/api/conversations/grant-child/permission-grants/revoke',
          sessionGrantRevokeBody()
        )
      );
      expect(stopInvokeMock).not.toHaveBeenCalled();
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(modeSetter).not.toHaveBeenCalled();
      if (mobile) {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      } else {
        expect(screen.getByTestId('session-config-row-permission')).toBeInTheDocument();
      }
    } finally {
      transport.mockRestore();
      setRendererAccountOwner(null);
      realMobileActionSheet.current = false;
      realAgentControls.current = false;
      realContextDropdown.current = false;
    }
  });

  it('blocks sending and delegates runtime recovery to the permanent status center', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = false;
    runtimeViewMock.hydrationError = 'gateway unavailable';
    loadFramePlanMock.mockImplementationOnce(() => new Promise(() => {}));

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(runtimeOperationsProps.current?.runtimeAuthorityUnavailable).toBe(true);
    expect(screen.queryByRole('alert', { name: '运行状态不可用' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'send' })).toBeDisabled();
    runtimeOperationsProps.current?.onRetryRuntimeAuthority?.();
    expect(runtimeViewMock.retryHydration).toHaveBeenCalledOnce();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
  });

  it('delegates the runtime-authoritative task state and stop action to the single status center', async () => {
    runtimeViewMock.isProcessing = true;
    runtimeViewMock.state = 'running';
    const messageState = makeMessageState();
    messageState.aiProcessing = false;
    messageState.tokenUsage = { total_tokens: 24_576 };
    messageListMock.current = [
      {
        id: 'user-task-1',
        msg_id: 'user-task-1',
        type: 'text',
        position: 'right',
        conversation_id: 'conv-1',
        created_at: 1,
        content: { content: 'Run the task' },
      } as TMessage,
      {
        id: 'tool-1',
        msg_id: 'tool-1',
        type: 'tool_call',
        position: 'left',
        conversation_id: 'conv-1',
        created_at: 2,
        content: { call_id: 'call-1', name: 'bash', args: {}, status: 'completed' },
      } as TMessage,
    ];

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={messageState} />);

    expect(runtimeOperationsProps.current?.runtimeState).toBe('running');
    expect(runtimeOperationsProps.current?.onStop).toBeTypeOf('function');
    expect(runtimeOperationsProps.current?.taskCenterMetrics).toEqual({
      total: { tokenUsage: 24_576, modelCallCount: null, toolCallCount: 1 },
      latest: { tokenUsage: 24_576, modelCallCount: null, toolCallCount: 1 },
    });
    expect(screen.queryByTestId('x-science-runtime-status')).not.toBeInTheDocument();
  });

  it('keeps the composer available for queued follow-up tasks while the current task runs', async () => {
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(screen.getByTestId('sendbox-bottom-hint')).toHaveTextContent('');
    expect(screen.getByTestId('sendbox-placeholder').parentElement).toHaveAttribute(
      'data-allow-send-while-loading',
      'true'
    );
  });

  it('keeps the composer disabled until the initial runtime authority is hydrated', async () => {
    runtimeViewMock.hydrated = false;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    loadFramePlanMock.mockImplementationOnce(() => new Promise(() => {}));

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(screen.getByRole('button', { name: 'send' })).toBeDisabled();
    expect(screen.queryByRole('alert', { name: '运行状态不可用' })).not.toBeInTheDocument();
  });

  it('AutoReview leaves a committed-but-500 mutation unknown until GET-only Retry', async () => {
    clearXScienceFrameReads();
    realReviewAuthority.current = true;
    realAgentControls.current = true;
    realContextDropdown.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    let mode = 'off';
    const transport = vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      if (String(url) === '/api/memory/enabled') return Response.json({ enabled: true });
      if (init?.method === 'POST') {
        expect(String(url)).toBe('/api/frames/conv-1/session-config');
        expect(JSON.parse(String(init.body))).toEqual({ verifier_mode: 'on' });
        mode = 'on';
        return Response.json({ detail: 'session config updated but realtime delivery failed' }, { status: 500 });
      }
      return Response.json({
        id: 'conv-1',
        root_frame_id: 'conv-1',
        parent_frame_id: null,
        project_id: 'review-project',
        input_data: { verifier_mode: mode },
      });
    });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    try {
      fireEvent.click(screen.getByRole('button', { name: '会话选项' }));
      const review = await screen.findByTestId('session-config-row-autoReview');
      await waitFor(() => expect(review).toBeEnabled());
      expect(review).toHaveAttribute('aria-checked', 'false');
      fireEvent.click(review);
      await waitFor(() => expect(mode).toBe('on'));
      await waitFor(() => expect(messageErrorMock).toHaveBeenCalled());
      expect(review).toHaveAttribute('aria-checked', 'mixed');
      expect(review).toBeDisabled();
      fireEvent.click(review);
      const retry = await screen.findByRole('menuitem', { name: '刷新自动审阅偏好' });
      expect(retry).not.toHaveAttribute('aria-checked');
      fireEvent.click(retry);
      await waitFor(() => expect(review).toHaveAttribute('aria-checked', 'true'));
      expect(review).toBeEnabled();
      expect(transport.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(requestFrameAuditMock).not.toHaveBeenCalled();
    } finally {
      view.unmount();
      transport.mockRestore();
      clearXScienceFrameReads();
      realReviewAuthority.current = false;
    }
  });

  it('Memory406 keeps ordinary Send available with unknown Memory and omits a stale override', async () => {
    realMemoryAuthority.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    loadSessionMemoryMock.mockRejectedValue(new Error('synthetic preference unavailable'));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    try {
      expect(screen.getByRole('button', { name: 'send' })).toBeEnabled();
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(sendMessageInvokeMock.mock.calls[0][0].session_options).not.toHaveProperty('memory_mode');
    } finally {
      view.unmount();
    }
  });

  it('AutoReview reuses immutable input after an unknown-to-ready observation without replaying a new command', async () => {
    realMemoryAuthority.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    const preference = createDeferred<{ rootFrameId: string; autoReview: boolean }>();
    loadSessionReviewMock.mockReturnValue(preference.promise);
    sendMessageInvokeMock
      .mockReset()
      .mockRejectedValueOnce(new Error('response_lost_after_commit'))
      .mockResolvedValueOnce({
        msg_id: 'review-retry',
        turn_id: 'review-retry',
        runtime: { state: 'running', is_processing: true },
      });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    try {
      expect(screen.getByRole('button', { name: 'send' })).toBeEnabled();
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      const first = sendMessageInvokeMock.mock.calls[0][0];
      expect(first.session_options).not.toHaveProperty('verifier_mode');
      await act(async () => preference.resolve({ rootFrameId: 'conv-1', autoReview: true }));
      await waitFor(() => expect(sessionOptionsProps.current?.value).toMatchObject({ autoReview: true }));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
      expect(sendMessageInvokeMock.mock.calls[1][0]).toEqual(first);
      expect(queuedCommandSequence).toBe(1);
    } finally {
      view.unmount();
    }
  });

  it('AutoReview explicit mutation retires only staged review intent and starts a new command after lost ACK', async () => {
    realMemoryAuthority.current = true;
    realAgentControls.current = true;
    realContextDropdown.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    memoryRetryDraft.initial = { delegation: false, autoReview: false, memory: true, targetAgent: 'OPERON' };
    const availability = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => Response.json({ enabled: true }));
    sendMessageInvokeMock
      .mockReset()
      .mockRejectedValueOnce(new Error('response_lost_after_commit'))
      .mockResolvedValueOnce({
        msg_id: 'review-new',
        turn_id: 'review-new',
        runtime: { state: 'running', is_processing: true },
      });
    const messageState = makeMessageState();
    messageState.running = false;
    const view = await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={messageState} />);
    try {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      const first = sendMessageInvokeMock.mock.calls[0][0];
      expect(first.session_options.verifier_mode).toBe('off');
      fireEvent.click(screen.getByRole('button', { name: '会话选项' }));
      const review = await screen.findByTestId('session-config-row-autoReview');
      await waitFor(() => expect(review).toBeEnabled());
      fireEvent.click(review);
      await waitFor(() => expect(updateSessionConfigMock).toHaveBeenCalledTimes(1));
      expect(updateSessionConfigMock.mock.calls[0][1]).toEqual({ autoReview: true });
      await waitFor(() => expect(review).toBeEnabled());
      expect(review).toHaveAttribute('aria-checked', 'false');
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
      expect(sendMessageInvokeMock.mock.calls[1][0].loading_id).not.toBe(first.loading_id);
      expect(sendMessageInvokeMock.mock.calls[1][0].session_options.memory_mode).toBe('on');
      expect(queuedCommandSequence).toBe(2);
    } finally {
      view.unmount();
      availability.mockRestore();
    }
  });

  it('Memory406 R3 reuses the actual direct mutation after a lost ACK and automatic unknown-to-ready observation', async () => {
    realMemoryAuthority.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    const preference = createDeferred<{ rootFrameId: string; memory: boolean }>();
    loadSessionMemoryMock.mockReturnValue(preference.promise);
    sendMessageInvokeMock
      .mockReset()
      .mockRejectedValueOnce(new Error('response_lost_after_commit'))
      .mockResolvedValueOnce({
        msg_id: 'memory-retry',
        turn_id: 'memory-retry',
        runtime: { state: 'running', is_processing: true },
      });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    try {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      const first = sendMessageInvokeMock.mock.calls[0][0];
      expect(first.session_options).not.toHaveProperty('memory_mode');
      await act(async () => preference.resolve({ rootFrameId: 'conv-1', memory: true }));
      await waitFor(() => expect(sessionOptionsProps.current?.value).toMatchObject({ memory: true }));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
      expect(sendMessageInvokeMock.mock.calls[1][0]).toEqual(first);
      expect(queuedCommandSequence).toBe(1);
    } finally {
      view.unmount();
    }
  });

  it('Memory406 R3 treats an actual Memory user mutation as a new intent even with unchanged readback', async () => {
    realMemoryAuthority.current = true;
    realAgentControls.current = true;
    realContextDropdown.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    const availability = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => Response.json({ enabled: true }));
    sendMessageInvokeMock
      .mockReset()
      .mockRejectedValueOnce(new Error('response_lost_after_commit'))
      .mockResolvedValueOnce({
        msg_id: 'memory-new',
        turn_id: 'memory-new',
        runtime: { state: 'running', is_processing: true },
      });
    const messageState = makeMessageState();
    messageState.running = false;
    const view = await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={messageState} />);
    try {
      await waitFor(() =>
        expect(
          (
            sessionOptionsProps.current?.memoryControl as { preference?: { capture: () => boolean | null } }
          )?.preference?.capture()
        ).toBe(false)
      );
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      fireEvent.click(screen.getByRole('button', { name: '会话选项' }));
      const memory = await screen.findByTestId('session-config-row-memory');
      await waitFor(() => expect(memory).toBeEnabled());
      const before = loadSessionMemoryMock.mock.calls.length;
      fireEvent.click(memory);
      await waitFor(() => expect(updateSessionConfigMock).toHaveBeenCalledTimes(1));
      expect(updateSessionConfigMock.mock.calls[0][1]).toEqual({ memory: true });
      // A fresh read is an observation, not an atomic ACK: keep the same retained value here.
      await waitFor(() => expect(loadSessionMemoryMock.mock.calls.length).toBeGreaterThan(before));
      await waitFor(() => expect(memory).toBeEnabled());
      expect(memory).toHaveAttribute('aria-checked', 'false');
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
      expect(sendMessageInvokeMock.mock.calls[1][0].loading_id).not.toBe(
        sendMessageInvokeMock.mock.calls[0][0].loading_id
      );
      expect(queuedCommandSequence).toBe(2);
    } finally {
      view.unmount();
      availability.mockRestore();
    }
  });

  it('Memory406 R3 distinguishes changed staged Memory intent without treating observed preferences as intent', async () => {
    realMemoryAuthority.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    loadSessionMemoryMock.mockResolvedValue({ rootFrameId: 'conv-1', memory: true });
    const staged = { delegation: false, autoReview: false, memory: false, targetAgent: 'OPERON' };
    memoryRetryDraft.initial = staged;
    sendMessageInvokeMock
      .mockReset()
      .mockRejectedValueOnce(new Error('response_lost_after_commit'))
      .mockResolvedValueOnce({
        msg_id: 'memory-staged',
        turn_id: 'memory-staged',
        runtime: { state: 'running', is_processing: true },
      });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    try {
      await waitFor(() => expect(sessionOptionsProps.current?.value).toMatchObject({ memory: true }));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
      expect(sendMessageInvokeMock.mock.calls[0][0].session_options.memory_mode).toBe('off');
      act(() => memoryRetryDraft.replace?.({ ...staged, memory: true }));
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
      expect(sendMessageInvokeMock.mock.calls[1][0].loading_id).not.toBe(
        sendMessageInvokeMock.mock.calls[0][0].loading_id
      );
      expect(sendMessageInvokeMock.mock.calls[1][0].session_options.memory_mode).toBe('on');
      expect(queuedCommandSequence).toBe(2);
    } finally {
      view.unmount();
    }
  });

  it.each(['ready', 'retrying'] as const)(
    'Memory406 R4 preserves staged replay after %s legacy hydration without overriding observed Memory',
    async (status) => {
      realMemoryAuthority.current = true;
      runtimeViewMock.isProcessing = false;
      runtimeViewMock.canSendMessage = true;
      loadSessionMemoryMock.mockResolvedValue({ rootFrameId: 'conv-1', memory: false });
      if (status === 'retrying')
        loadSessionOptionsMock.mockRejectedValue(new Error('synthetic legacy hydration failure'));
      memoryRetryDraft.initial = { delegation: true, autoReview: true, memory: true, targetAgent: 'OPERON' };
      sendMessageInvokeMock.mockReset().mockResolvedValueOnce({
        msg_id: 'memory-staged-replay',
        turn_id: 'memory-staged-replay',
        runtime: { state: 'running', is_processing: true },
      });
      const view = await render(
        <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
      );
      try {
        await waitFor(() =>
          expect(sessionOptionsProps.current?.value).toMatchObject({
            delegation: true,
            autoReview: true,
            memory: false,
          })
        );
        expect(sessionOptionsProps.current?.disabled).toBe(status === 'retrying');
        fireEvent.click(screen.getByRole('button', { name: 'send' }));
        await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
        expect(sendMessageInvokeMock.mock.calls[0][0].session_options).toMatchObject({
          verifier_mode: 'on',
          memory_mode: 'on',
        });
      } finally {
        view.unmount();
      }
    }
  );

  it('keeps the core composer available and retries when optional session settings fail', async () => {
    loadSessionMemoryMock.mockResolvedValue({ rootFrameId: 'conv-1', memory: true });
    vi.useFakeTimers();
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    const failed = createDeferred<Record<string, unknown>>();
    loadSessionOptionsMock.mockReturnValueOnce(failed.promise).mockResolvedValueOnce({
      delegation: false,
      autoReview: false,
      memory: true,
      targetAgent: 'OPERON',
      goalText: null,
      asRoutine: false,
    });
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));

    try {
      await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
      expect(screen.queryByText('正在加载会话选项')).not.toBeInTheDocument();
      await act(async () => {
        failed.reject(new Error('memory_request_failed_503'));
        await failed.promise.catch(() => undefined);
      });

      expect(screen.queryByRole('alert', { name: '无法加载会话选项。' })).not.toBeInTheDocument();
      expect(screen.queryByText('正在加载会话选项')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'send' })).toBeEnabled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });
      expect(loadSessionOptionsMock).toHaveBeenCalledTimes(2);
      expect(sessionOptionsProps.current?.value).toMatchObject({ memory: true });
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not apply stale session options after switching conversations', async () => {
    loadSessionMemoryMock.mockImplementation(async (id: string) => ({ rootFrameId: id, memory: id === 'conv-2' }));
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    const stale = createDeferred<Record<string, unknown>>();
    const current = createDeferred<Record<string, unknown>>();
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    loadSessionOptionsMock.mockImplementation((frameId: string) =>
      frameId === 'conv-1' ? stale.promise : current.promise
    );

    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    await act(async () => {
      current.resolve({
        delegation: false,
        autoReview: true,
        memory: true,
        targetAgent: 'OPERON',
        goalText: null,
        asRoutine: false,
      });
      await current.promise;
    });
    await waitFor(() => expect(sessionOptionsProps.current?.value).toMatchObject({ memory: true }));

    await act(async () => {
      stale.resolve({
        delegation: false,
        autoReview: true,
        memory: false,
        targetAgent: 'OPERON',
        goalText: null,
        asRoutine: false,
      });
      await stale.promise;
    });

    expect(sessionOptionsProps.current?.value).toMatchObject({ memory: true });
  });

  it('keeps the task running and exposes recovery when stop fails', async () => {
    stopInvokeMock.mockRejectedValueOnce(new Error('gateway unavailable'));

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    await act(async () => {
      await runtimeOperationsProps.current?.onStop?.();
    });

    expect(runtimeViewMock.markStopRequested).toHaveBeenCalledWith('turn-1');
    expect(runtimeViewMock.resetLocalGate).toHaveBeenCalledWith('stop_failed');
    expect(resetStateMock).not.toHaveBeenCalled();
    expect(setAiProcessingMock).not.toHaveBeenCalled();
    expect(messageErrorMock).toHaveBeenCalledWith('停止请求失败，任务仍可能继续运行。请重试或刷新运行状态。');
  });

  it('exposes one authoritative cancellation handler through the status center only', async () => {
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(screen.queryByRole('button', { name: 'stop' })).not.toBeInTheDocument();
    await act(async () => {
      await runtimeOperationsProps.current?.onStop?.();
    });

    expect(runtimeOperationsProps.current?.onStop).toBeTypeOf('function');
    expect(stopInvokeMock).toHaveBeenCalledWith({
      conversation_id: 'conv-1',
      turn_id: 'turn-1',
    });
    expect(runtimeViewMock.markStopRequested).toHaveBeenCalledWith('turn-1');
    expect(runtimeViewMock.markStopAcknowledged).toHaveBeenCalledOnce();
  });

  it('hydrates authoritative messages after a runtime mutation completes', async () => {
    const onRuntimeMutation = vi.fn();
    await render(
      <AcpSendBox
        conversation_id='conv-1'
        backend='xscience'
        messageState={makeMessageState()}
        onRuntimeMutation={onRuntimeMutation}
      />
    );

    act(() => {
      runtimeOperationsProps.current?.onResumed?.('turn-2', {
        state: 'failed',
        can_send_message: true,
        has_task: true,
        task_status: 'failed',
        is_processing: false,
        pending_confirmations: 0,
        turn_id: 'turn-2',
      });
    });

    expect(onRuntimeMutation).toHaveBeenCalledOnce();
    expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledWith(
      'turn-2',
      expect.objectContaining({ state: 'failed' })
    );
  });

  it('applies a passive runtime refresh through the authoritative conversation gate', async () => {
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    act(() => {
      runtimeOperationsProps.current?.onRuntimeUpdated?.('turn-3', {
        state: 'idle',
        can_send_message: true,
        has_task: false,
        task_status: 'finished',
        is_processing: false,
        pending_confirmations: 0,
        turn_id: null,
      });
    });

    expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledWith(
      'turn-3',
      expect.objectContaining({ state: 'idle', task_status: 'finished' })
    );
    expect(emitterEmitMock).toHaveBeenCalledWith(
      'xscience.runtime.reconciled',
      'conv-1',
      expect.objectContaining({ state: 'idle', task_status: 'finished' })
    );
  });

  it('removes resume from session settings and leaves recovery to runtime operations', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.hasTask = false;
    runtimeViewMock.view.hasBackendRuntime = true;
    runtimeViewMock.view.taskStatus = 'error';
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(sessionOptionsProps.current).not.toHaveProperty('onResume');
    expect(sessionOptionsProps.current).not.toHaveProperty('resumeInFlight');
    expect(runtimeOperationsProps.current?.onResumed).toBeTypeOf('function');
    expect(resumeFrameMock).not.toHaveBeenCalled();
  });

  it('keeps polling while the backend acknowledges a cancelling runtime', async () => {
    stopInvokeMock.mockResolvedValueOnce({
      runtime: {
        state: 'cancelling',
        can_send_message: false,
        has_task: true,
        task_status: 'running',
        is_processing: true,
        pending_confirmations: 0,
        turn_id: 'turn-1',
      },
    });

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    await act(async () => {
      await runtimeOperationsProps.current?.onStop?.();
    });

    expect(runtimeViewMock.markStopAcknowledged).toHaveBeenCalledWith(
      'turn-1',
      expect.objectContaining({ state: 'cancelling', is_processing: true })
    );
    expect(setAiProcessingMock).toHaveBeenCalledWith(true);
    expect(resetStateMock).not.toHaveBeenCalled();
  });

  it('does not apply a completed stop request to a replacement conversation', async () => {
    const pendingStop = createDeferred<{ runtime: Record<string, unknown> }>();
    stopInvokeMock.mockReturnValueOnce(pendingStop.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    await act(async () => {
      void runtimeOperationsProps.current?.onStop?.();
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    runtimeViewMock.markStopAcknowledged.mockClear();
    setAiProcessingMock.mockClear();
    resetStateMock.mockClear();

    await act(async () => {
      pendingStop.resolve({
        runtime: {
          state: 'idle',
          can_send_message: true,
          has_task: true,
          task_status: 'cancelled',
          is_processing: false,
          pending_confirmations: 0,
          turn_id: null,
        },
      });
      await pendingStop.promise;
    });

    expect(runtimeViewMock.markStopAcknowledged).not.toHaveBeenCalled();
    expect(setAiProcessingMock).not.toHaveBeenCalled();
    expect(resetStateMock).not.toHaveBeenCalled();
  });

  it('does not expose a failed stop request after switching conversations', async () => {
    const pendingStop = createDeferred<never>();
    stopInvokeMock.mockReturnValueOnce(pendingStop.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    await act(async () => {
      void runtimeOperationsProps.current?.onStop?.();
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    runtimeViewMock.resetLocalGate.mockClear();
    messageErrorMock.mockClear();

    await act(async () => {
      pendingStop.reject(new Error('old stop failed'));
      await pendingStop.promise.catch(() => undefined);
    });

    expect(runtimeViewMock.resetLocalGate).not.toHaveBeenCalled();
    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it('does not fake a stopped state when the active turn id is unavailable', async () => {
    runtimeViewMock.activeTurnId = null;

    await render(<AcpSendBox conversation_id='conv-1' backend='claude' messageState={makeMessageState()} />);

    expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();

    await act(async () => {
      await runtimeOperationsProps.current?.onStop?.();
    });

    expect(stopInvokeMock).not.toHaveBeenCalled();
    expect(runtimeViewMock.resetLocalGate).toHaveBeenCalledWith('stop_missing_turn');
    expect(resetStateMock).not.toHaveBeenCalled();
    expect(messageErrorMock).toHaveBeenCalledWith('当前任务缺少运行标识，无法停止。请刷新运行状态后重试。');
  });

  it('cancels a X-Science frame when the live task is ahead of the conversation turn projection', async () => {
    runtimeViewMock.activeTurnId = null;

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    await act(async () => {
      await runtimeOperationsProps.current?.onStop?.();
    });

    expect(stopInvokeMock).toHaveBeenCalledWith({
      conversation_id: 'conv-1',
      turn_id: 'conv-1',
    });
    expect(runtimeViewMock.markStopRequested).toHaveBeenCalledWith('conv-1');
    expect(runtimeViewMock.markStopAcknowledged).toHaveBeenCalledOnce();
    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it('resets ACP loading state when sendMessage fails before any stream error arrives', async () => {
    sendMessageInvokeMock.mockRejectedValue(
      new BackendHttpError({
        method: 'POST',
        path: '/api/conversations/conv-1/messages',
        status: 400,
        body: {
          success: false,
          code: 'WORKSPACE_PATH_RUNTIME_UNAVAILABLE',
          error: 'Workspace path is unavailable during execution: /tmp/missing',
          details: { workspace_path: '/tmp/missing' },
        },
      })
    );

    await render(
      <AcpSendBox
        conversation_id='conv-1'
        backend='claude'
        workspacePath='/tmp/missing'
        messageState={makeMessageState()}
      />
    );
    await waitFor(() => expect(screen.getByRole('button', { name: 'send' })).toBeEnabled());

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
    });

    await waitFor(() => {
      expect(resetStateMock).toHaveBeenCalledTimes(1);
    });
  });

  it('adds authentication failures to the local conversation without using a realtime event bus', async () => {
    const authenticationError = new Error('[ACP-AUTH-401] authentication failed');
    sendMessageInvokeMock.mockRejectedValueOnce(authenticationError);

    await act(async () => {
      await render(<AcpSendBox conversation_id='conv-1' backend='codex' messageState={makeMessageState()} />);
    });

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
    });

    await waitFor(() =>
      expect(addOrUpdateMessageMock.mock.calls.filter(([message]) => message?.type === 'tips')).toHaveLength(1)
    );
    expect(addOrUpdateMessageMock).toHaveBeenCalledWith(
      expect.objectContaining({
        conversation_id: 'conv-1',
        position: 'center',
        type: 'tips',
        content: expect.objectContaining({
          content: expect.stringContaining('codex 认证失败'),
          error: expect.objectContaining({
            message: expect.stringContaining('[ACP-AUTH-401]'),
          }),
          type: 'error',
        }),
      }),
      true
    );
    expect(emitterEmitMock).not.toHaveBeenCalledWith(expect.stringContaining('response'), expect.anything());
    expect(runtimeViewMock.markSendFailed).toHaveBeenCalledWith(
      '[ACP-AUTH-401] authentication failed',
      runtimeViewMock.markSendStarted.mock.results[0]?.value
    );
    expect(resetStateMock).toHaveBeenCalledTimes(1);
    expect(setAiProcessingMock).toHaveBeenLastCalledWith(false);
  });

  it('redacts secrets and personal identifiers from local send failure details', async () => {
    sendMessageInvokeMock.mockRejectedValueOnce(
      new Error('provider rejected sk-live-secret-value for alice@example.com token=private-token-value')
    );
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'send' })).toBeEnabled());

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
    });
    await waitFor(() =>
      expect(addOrUpdateMessageMock.mock.calls.filter(([message]) => message?.type === 'tips')).toHaveLength(1)
    );

    const failureCall = addOrUpdateMessageMock.mock.calls.find(([message]) => message?.type === 'tips');
    expect(failureCall).toBeDefined();
    const payload = JSON.stringify(failureCall);
    expect(payload).toContain('[REDACTED_KEY]');
    expect(payload).toContain('[email]');
    expect(payload).not.toContain('live-secret-value');
    expect(payload).not.toContain('alice@example.com');
    expect(payload).not.toContain('private-token-value');
  });

  it('redacts a failed stop diagnostic before writing renderer logs', async () => {
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    stopInvokeMock.mockRejectedValueOnce(
      new Error('stop failed for sk-live-secret-value and alice@example.com token=private-token-value')
    );
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    try {
      await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

      await act(async () => {
        await runtimeOperationsProps.current?.onStop?.();
      });

      const diagnostic = JSON.stringify(consoleWarn.mock.calls);
      expect(diagnostic).toContain('[REDACTED_KEY]');
      expect(diagnostic).toContain('[email]');
      expect(diagnostic).not.toContain('live-secret-value');
      expect(diagnostic).not.toContain('alice@example.com');
      expect(diagnostic).not.toContain('private-token-value');
    } finally {
      consoleWarn.mockRestore();
    }
  });

  it('does not apply a completed send request to a replacement conversation', async () => {
    const pendingSend = createDeferred<{
      msg_id: string;
      turn_id: string;
      runtime: { state: string; is_processing: boolean };
    }>();
    sendMessageInvokeMock.mockReturnValueOnce(pendingSend.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    runtimeViewMock.markSendAccepted.mockClear();
    emitterEmitMock.mockClear();

    await act(async () => {
      pendingSend.resolve({
        msg_id: 'message-old',
        turn_id: 'turn-old',
        runtime: { state: 'running', is_processing: true },
      });
      await pendingSend.promise;
    });

    expect(runtimeViewMock.markSendAccepted).not.toHaveBeenCalled();
    expect(emitterEmitMock).not.toHaveBeenCalledWith('chat.history.refresh');
  });

  it('settles a terminal send acknowledgement without another stream event', async () => {
    const receipt = {
      version: 1,
      frame_id: 'conv-1',
      frame_incarnation_id: 'incarnation',
      input_message_id: 'original-input',
      request_sha256: 'a'.repeat(64),
      replayed: true,
      state: 'accepted',
      source: 'transcript',
      event_id: 'original-event',
      input_revision: 2,
      branch_id: 'branch',
      branch_generation: 1,
    };
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'original-input',
      turn_id: 'conv-1',
      input_receipt: receipt,
      runtime: { state: 'idle', is_processing: false },
    });
    runtimeViewMock.markSendAccepted.mockReturnValueOnce({ state: 'idle', isProcessing: false });
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
    });
    await waitFor(() =>
      expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledWith(
        'conv-1',
        expect.objectContaining({ state: 'idle' }),
        'original-input',
        { ticket: runtimeViewMock.markSendStarted.mock.results[0]?.value, receipt }
      )
    );
    expect(setAiProcessingMock).toHaveBeenLastCalledWith(false);
    expect(emitterEmitMock).toHaveBeenCalledWith('chat.history.refresh');
  });

  it('does not reset a newer submission when its acknowledgement is stale', async () => {
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'old-input',
      turn_id: 'conv-1',
      runtime: { state: 'idle', is_processing: false },
    });
    runtimeViewMock.markSendAccepted.mockReturnValueOnce(null);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
    });
    await waitFor(() => expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledOnce());
    expect(setAiProcessingMock).not.toHaveBeenCalledWith(false);
    expect(addOrUpdateMessageMock).not.toHaveBeenCalledWith(expect.objectContaining({ msg_id: 'old-input' }), false);
    expect(emitterEmitMock).toHaveBeenCalledWith('chat.history.refresh');
  });

  it('does not expose a failed send request after switching conversations', async () => {
    const pendingSend = createDeferred<never>();
    sendMessageInvokeMock.mockReturnValueOnce(pendingSend.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    runtimeViewMock.markSendFailed.mockClear();
    addOrUpdateMessageMock.mockClear();
    resetStateMock.mockClear();
    setAiProcessingMock.mockClear();

    await act(async () => {
      pendingSend.reject(new Error('old send failed'));
      await pendingSend.promise.catch(() => undefined);
    });

    expect(runtimeViewMock.markSendFailed).not.toHaveBeenCalled();
    expect(addOrUpdateMessageMock).not.toHaveBeenCalled();
    expect(resetStateMock).not.toHaveBeenCalled();
    expect(setAiProcessingMock).not.toHaveBeenCalled();
  });

  it('uses container-responsive fluid width instead of a fixed max width', async () => {
    await act(async () => {
      await render(
        <AcpSendBox
          conversation_id='conv-1'
          backend='codex'
          workspacePath='/tmp/workspace'
          messageState={makeMessageState()}
        />
      );
      await Promise.resolve();
    });

    const wrapper = screen.getByRole('button', { name: 'send' }).parentElement?.parentElement;
    expect(wrapper?.className).toContain('chat-surface-fluid');
    expect(wrapper?.className).toContain('conversation-composer');
    expect(wrapper?.className).toContain('box-border');
    expect(wrapper?.className).toContain('min-w-0');
    expect(wrapper?.className).not.toContain('w-[calc(100%-24px)]');
    expect(wrapper?.className).not.toContain('md:w-[calc(100%-clamp(80px,10vw,240px))]');
    expect(wrapper?.className).not.toContain('max-w-800px');
  });

  it('matches the v1.1 composer placeholder and keeps model selection beside the input', async () => {
    await act(async () => {
      await render(
        <AcpSendBox
          conversation_id='conv-1'
          backend='xscience'
          agent_name='OPERON'
          workspacePath='/tmp/workspace'
          messageState={makeMessageState()}
        />
      );
      await Promise.resolve();
    });

    expect(screen.getByTestId('sendbox-placeholder')).toHaveTextContent(
      '输入你的问题 - @ 引用产物，# 引用会话，/ 调用 Skills，Ctrl+K 搜索...'
    );
    expect(screen.getByTestId('sendbox-bottom-hint')).toHaveTextContent('');
    expect(screen.getByTestId('composer-model-selector')).toHaveTextContent('模型 conv-1');
  });

  it('exposes one primary send path without a secondary send-mode menu', async () => {
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'message-1',
      turn_id: 'turn-1',
      runtime: { state: 'running', is_processing: true },
    });
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'send' })).toBeEnabled());

    expect(screen.getAllByRole('button', { name: 'send' })).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Plan first' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '侧边对话' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Branch in new session' })).not.toBeInTheDocument();

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
      await Promise.resolve();
    });

    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({ conversation_id: 'conv-1', input: 'Hello' })
    );
  });

  it('promotes the accepted user message and removes its optimistic duplicate', async () => {
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'message-1',
      turn_id: 'turn-1',
      runtime: { state: 'running', is_processing: true },
    });
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
      await Promise.resolve();
    });

    expect(removeMessageByMsgIdMock).toHaveBeenCalledWith(expect.stringMatching(/^xscience-optimistic-user:/));
    expect(addOrUpdateMessageMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'message-1',
        msg_id: 'message-1',
        status: 'finish',
      }),
      false
    );
  });

  it('sends files, exact artifact versions, fixed Skills, and fixed MCP connectors through one request', async () => {
    draftContextItemsMock.current = [
      {
        kind: 'artifact',
        artifactId: 'artifact-1',
        versionId: 'version-2',
        label: 'cohort.csv',
        contentType: 'text/csv',
        sizeBytes: 42,
      },
      { kind: 'skill', name: 'single-cell-analysis', label: 'single-cell-analysis' },
      { kind: 'mcp', serverId: 'pubmed', label: 'PubMed' },
    ];
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'message-context',
      turn_id: 'turn-context',
      runtime: { state: 'running', is_processing: true },
    });
    await render(<AcpSendBox conversation_id='conv-context' backend='xscience' messageState={makeMessageState()} />);

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
      await Promise.resolve();
    });

    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        conversation_id: 'conv-context',
        input: 'Hello',
        artifact_refs: [
          expect.objectContaining({
            artifact_id: 'artifact-1',
            version_id: 'version-2',
            relation: 'attached',
          }),
        ],
        inject_skills: ['single-cell-analysis'],
        inject_mcp_server_ids: ['pubmed'],
      })
    );
  });

  it('loads conversation-authorized Skill and MCP identities into the composer picker', async () => {
    loadComposerCapabilitiesMock.mockResolvedValueOnce({
      skills: ['single-cell-analysis'],
      mcpStatuses: [{ id: 'pubmed-id', name: 'PubMed', status: 'loaded' }],
    });
    await render(
      <AcpSendBox conversation_id='conv-capabilities' backend='xscience' messageState={makeMessageState()} />
    );

    await waitFor(() => {
      expect(fileAttachProps.current?.loadedSkills).toEqual(['single-cell-analysis']);
      expect(fileAttachProps.current?.loadedMcpStatuses).toEqual([
        { id: 'pubmed-id', name: 'PubMed', status: 'loaded' },
      ]);
    });
    expect(loadComposerCapabilitiesMock).toHaveBeenCalledWith('conv-capabilities');
  });

  it('binds a selected branch continuation to one stable queued command identity', async () => {
    getSelectedBranchMock.mockReturnValue('br_00000002');
    loadBranchStateMock.mockResolvedValue({
      activeBranchId: 'br_00000001',
      selectedBranchId: 'br_00000002',
      generation: 7,
      branches: [
        { id: 'br_00000001', active: true },
        { id: 'br_00000002', active: false },
      ],
    });
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'message-branch',
      turn_id: 'turn-branch',
      runtime: { state: 'running', is_processing: true },
    });
    await render(<AcpSendBox conversation_id='conv-branch' backend='xscience' messageState={makeMessageState()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'send' })).toBeEnabled());

    await act(async () => {
      screen.getByRole('button', { name: 'send' }).click();
      await Promise.resolve();
    });

    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        conversation_id: 'conv-branch',
        loading_id: 'command-1',
        session_options: expect.objectContaining({
          target_branch_id: 'br_00000002',
          expected_branch_id: 'br_00000001',
          expected_generation: 7,
        }),
      })
    );
  });

  it('reuses the direct command identity after an ambiguous response failure', async () => {
    sendMessageInvokeMock.mockRejectedValueOnce(new Error('response_lost_after_commit')).mockResolvedValueOnce({
      msg_id: 'message-retry',
      turn_id: 'turn-retry',
      runtime: { state: 'running', is_processing: true },
    });
    await render(<AcpSendBox conversation_id='conv-retry' backend='xscience' messageState={makeMessageState()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'send' })).toBeEnabled());

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
    });
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
    });
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));

    const first = sendMessageInvokeMock.mock.calls[0]?.[0] as {
      loading_id?: string;
    };
    const second = sendMessageInvokeMock.mock.calls[1]?.[0] as {
      loading_id?: string;
    };
    expect(first.loading_id).toBe('command-1');
    expect(second.loading_id).toBe(first.loading_id);
    expect(queuedCommandSequence).toBe(1);
  });

  it('refreshes stale branch authority after an explicit conflict while preserving the mutation identity', async () => {
    getSelectedBranchMock.mockReturnValue('br_00000002');
    loadBranchStateMock
      .mockResolvedValueOnce({
        activeBranchId: 'br_00000001',
        selectedBranchId: 'br_00000002',
        generation: 7,
        branches: [
          { id: 'br_00000001', active: true },
          { id: 'br_00000002', active: false },
        ],
      })
      .mockResolvedValueOnce({
        activeBranchId: 'br_00000003',
        selectedBranchId: 'br_00000002',
        generation: 8,
        branches: [
          { id: 'br_00000002', active: false },
          { id: 'br_00000003', active: true },
        ],
      });
    sendMessageInvokeMock
      .mockRejectedValueOnce(
        new BackendHttpError({
          method: 'POST',
          path: '/api/conversations/conv-branch-retry/messages',
          status: 409,
          body: {
            code: 'CONFLICT',
            error: 'Conversation branch changed; refresh and retry',
          },
        })
      )
      .mockResolvedValueOnce({
        msg_id: 'message-branch-retry',
        turn_id: 'turn-branch-retry',
        runtime: { state: 'running', is_processing: true },
      });
    await render(
      <AcpSendBox conversation_id='conv-branch-retry' backend='xscience' messageState={makeMessageState()} />
    );
    await waitFor(() => expect(screen.getByRole('button', { name: 'send' })).toBeEnabled());

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
    });
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
    });
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));

    const first = sendMessageInvokeMock.mock.calls[0]?.[0] as {
      loading_id?: string;
      session_options?: Record<string, unknown>;
    };
    const second = sendMessageInvokeMock.mock.calls[1]?.[0] as {
      loading_id?: string;
      session_options?: Record<string, unknown>;
    };
    expect(first.loading_id).toBe('command-1');
    expect(second.loading_id).toBe(first.loading_id);
    expect(first.session_options).toMatchObject({
      target_branch_id: 'br_00000002',
      expected_branch_id: 'br_00000001',
      expected_generation: 7,
    });
    expect(second.session_options).toMatchObject({
      target_branch_id: 'br_00000002',
      expected_branch_id: 'br_00000003',
      expected_generation: 8,
    });
    expect(loadBranchStateMock).toHaveBeenCalledTimes(2);
    expect(queuedCommandSequence).toBe(1);
  });

  it('does not render the notebook entry in the composer', async () => {
    await act(async () => {
      await render(
        <AcpSendBox
          conversation_id='conv-1'
          backend='xscience'
          workspacePath='/tmp/workspace'
          messageState={makeMessageState()}
        />
      );
      await Promise.resolve();
    });

    expect(screen.queryByRole('button', { name: '笔记本' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('notebook-dock-badge')).not.toBeInTheDocument();
  });

  it('does not expose a composer-global branch selector on mobile', async () => {
    isMobileMock.current = true;
    await render(<AcpSendBox conversation_id='conv-mobile' backend='xscience' messageState={makeMessageState()} />);

    expect(screen.queryByTestId('branch-menu')).not.toBeInTheDocument();
  });

  it('projects model-list failure and retry into the existing mobile action sheet', async () => {
    const retryModelList = vi.fn().mockResolvedValue(undefined);
    isMobileMock.current = true;
    useAcpModelInfoMock.mockReturnValue({
      model_info: null,
      canSwitch: false,
      isModelListLoading: false,
      modelListError: new Error('api_key=mobile-secret-value'),
      retryModelList,
      selectModel: vi.fn(),
    });

    await act(async () => {
      await render(
        <AcpSendBox
          conversation_id='conv-1'
          backend='xscience'
          workspacePath='/tmp/workspace'
          messageState={makeMessageState()}
        />
      );
      await Promise.resolve();
    });

    const modelEntry = mobileActionSheetEntries.current.find((entry) => entry.key === 'model');
    expect(modelEntry?.label).toBe('模型列表不可用');
    expect(String(modelEntry?.description)).not.toContain('mobile-secret-value');
    modelEntry?.onClick?.();
    await waitFor(() => expect(retryModelList).toHaveBeenCalledOnce());
  });

  it('keeps a read-only model identity in the mobile sheet when switching is unavailable', async () => {
    isMobileMock.current = true;

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(mobileActionSheetEntries.current.find((entry) => entry.key === 'model')).toMatchObject({
      label: '模型',
      meta: '选择模型',
      disabled: true,
    });
  });

  it('routes mobile View plan to the existing task center without duplicate discovery', async () => {
    isMobileMock.current = true;
    loadFramePlanMock.mockResolvedValue({
      artifactId: 'artifact-plan-1',
      versionId: 'version-1',
      filename: 'plan_conv-1.json',
      createdAt: '2026-07-14T00:00:00Z',
    });

    await render(
      <AcpSendBox
        conversation_id='conv-1'
        backend='xscience'
        workspacePath='/tmp/workspace'
        messageState={makeMessageState()}
      />
    );

    const keys = mobileActionSheetEntries.current.map((entry) => entry.key);
    expect(keys).not.toContain('view-plan');
    expect(runtimeOperationsProps.current?.onPlanReferenceChange).toEqual(expect.any(Function));
    act(() =>
      runtimeOperationsProps.current?.onPlanReferenceChange?.('conv-1', {
        artifactId: 'artifact-plan-1',
        versionId: 'version-1',
        progress: { completed: 0, total: 3 },
      })
    );
    expect(mobileActionSheetEntries.current.find((entry) => entry.key === 'view-plan')).toMatchObject({ meta: '0/3' });
    expect(keys).not.toContain('request-review');
    expect(keys).toContain('save-skill');
    expect(mobileActionSheetEntries.current.find((entry) => entry.key === 'save-skill')).toMatchObject({
      disabled: true,
      busy: false,
      keepDisabledFocusable: true,
      description: '任务空闲时可用',
    });
    expect(loadFramePlanMock).not.toHaveBeenCalled();
    act(() => mobileActionSheetEntries.current.find((entry) => entry.key === 'view-plan')?.onClick?.());
    expect(emitterEmitMock).toHaveBeenCalledWith('xscience.runtime.plan.open', 'conv-1');
  });

  it('does not run duplicate plan discovery or retry logic in the composer', async () => {
    isMobileMock.current = true;
    loadFramePlanMock.mockRejectedValue(new Error('plan unavailable'));

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(mobileActionSheetEntries.current.find((entry) => entry.key === 'view-plan')).toBeUndefined();
    expect(loadFramePlanMock).not.toHaveBeenCalled();
  });

  it('uses the observed runtime contract for mobile send mode and suppresses duplicate selection', async () => {
    isMobileMock.current = true;
    let resolveSet!: (value: unknown[]) => void;
    const setConfigOption = vi.fn(
      () =>
        new Promise<unknown[]>((resolve) => {
          resolveSet = resolve;
        })
    );
    useAcpConfigOptionsMock.mockReturnValue({
      mode: {
        id: 'mode',
        category: 'mode',
        currentValue: 'send',
        options: [
          { value: 'send', label: '直接发送' },
          { value: 'plan', label: '先制定计划' },
        ],
      },
      model: null,
      thoughtLevel: null,
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
    });

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await act(async () => {
      screen.getByRole('button', { name: 'mobile more' }).click();
    });
    expect(mobileActionSheetOpen.current).toBe(true);
    const modeEntry = mobileActionSheetEntries.current.find((entry) => entry.key === 'send-mode');
    expect(modeEntry?.label).toBe('发送模式');
    act(() => {
      modeEntry?.submenu?.onSelect?.('plan');
      modeEntry?.submenu?.onSelect?.('plan');
    });
    expect(setConfigOption).toHaveBeenCalledTimes(1);
    expect(setConfigOption).toHaveBeenCalledWith('mode', 'plan');

    await act(async () => resolveSet([]));
    expect(messageSuccessMock).toHaveBeenCalled();
    expect(mobileActionSheetOpen.current).toBe(false);
  });

  it('keeps the mobile sheet recoverable when send mode update fails', async () => {
    isMobileMock.current = true;
    const setConfigOption = vi.fn().mockRejectedValue(new Error('config_not_observed'));
    useAcpConfigOptionsMock.mockReturnValue({
      mode: {
        id: 'mode',
        category: 'mode',
        currentValue: 'send',
        options: [{ value: 'plan', label: '先制定计划' }],
      },
      model: null,
      thoughtLevel: null,
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
    });

    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await act(async () => {
      mobileActionSheetEntries.current.find((entry) => entry.key === 'send-mode')?.submenu?.onSelect?.('plan');
      await Promise.resolve();
    });
    expect(setConfigOption).toHaveBeenCalledOnce();
    expect(messageErrorMock).toHaveBeenCalled();
  });

  it('M454 exposes the existing Plus action owners in mobile More in stable order without dispatching', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    mobilePlusProject.current = { projectId: 'project-mobile-plus' };
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendBoxDraftSeed.current = 'Keep the ordinary draft.';
    await render(
      <AcpSendBox conversation_id='conv-mobile-plus' backend='xscience' messageState={makeMessageState()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
    const expected = ['project-files', 'your-files', 'manual-review', 'save-skill', 'context-window'];
    await waitFor(() =>
      expect(mobileActionSheetEntries.current.map((entry) => entry.key)).toEqual(expect.arrayContaining(expected))
    );
    const actual = mobileActionSheetEntries.current.map((entry) => entry.key).filter((key) => expected.includes(key));
    expect(actual).toEqual(expected);
    for (const key of expected) expect(screen.getByTestId(`mobile-action-sheet-${key}`)).toBeInTheDocument();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    expect(requestFrameAuditMock).not.toHaveBeenCalled();
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep the ordinary draft.');
  });

  it('M454 opens the same Context owner from mobile More with an idle nonempty draft', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    sendBoxDraftSeed.current = 'Keep this nonempty draft.';
    const harness = await renderManualCompaction();
    try {
      expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
      fireEvent.click(await screen.findByTestId('mobile-action-sheet-context-window'));
      expect(await screen.findByRole('dialog', { name: 'Context window' })).toBeInTheDocument();
      expect(screen.getAllByTestId('context-window-inspector')).toHaveLength(1);
      expect(screen.queryByTestId('context-usage-panel')).not.toBeInTheDocument();
      expect(mobileActionSheetOpen.current).toBe(false);
      expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this nonempty draft.');
      expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      fireEvent.click(screen.getByTestId('context-inspector-close'));
      expect(screen.getByRole('button', { name: 'mobile more' })).toHaveFocus();
    } finally {
      harness.dispose();
    }
  });

  it.each([false, true])(
    'M462 does not offer Context from model-only mobile openers (prior More: %s)',
    async (priorMore) => {
      isMobileMock.current = true;
      realMobileActionSheet.current = true;
      sendBoxDraftSeed.current = 'Keep this recovery draft.';
      const harness = await renderManualCompaction();
      try {
        const more = screen.getByRole('button', { name: 'mobile more' });
        expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();
        if (priorMore) {
          fireEvent.click(more);
          fireEvent.click(await screen.findByTestId('mobile-action-sheet-context-window'));
          await screen.findByRole('dialog', { name: 'Context window' });
          fireEvent.click(screen.getByTestId('context-inspector-close'));
          expect(more).toHaveFocus();
        }
        // Both provider notice and TaskCenter use this same model-only callback.
        // Neither supplies the native More opener, and neither may reuse one.
        expect(runtimeOperationsProps.current?.onChooseModel).toBeTypeOf('function');
        await act(async () => runtimeOperationsProps.current?.onChooseModel?.());
        await screen.findByTestId('mobile-action-sheet-save-skill');
        expect(screen.queryByTestId('mobile-action-sheet-context-window')).not.toBeInTheDocument();
        expect(mobileActionSheetOpen.current).toBe(true);
        fireEvent.keyDown(window, { key: 'Escape' });
        expect(mobileActionSheetOpen.current).toBe(false);
        fireEvent.click(more);
        fireEvent.click(await screen.findByTestId('mobile-action-sheet-context-window'));
        await screen.findByRole('dialog', { name: 'Context window' });
        fireEvent.click(screen.getByTestId('context-inspector-close'));
        expect(more).toHaveFocus();
        expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this recovery draft.');
        expect(harness.transport.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
        expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      } finally {
        harness.dispose();
      }
    }
  );

  it('M454 invokes the existing hidden Save Skill command once from mobile More without a Save dialog', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendBoxDraftSeed.current = 'Preserve the ordinary draft during Skill saving.';
    sendMessageInvokeMock.mockImplementationOnce(acceptSkillTaskSend);
    await render(
      <AcpSendBox conversation_id='conv-mobile-save' backend='xscience' messageState={makeMessageState()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
    const save = await screen.findByTestId('mobile-action-sheet-save-skill');
    await act(async () => {
      fireEvent.click(save);
      fireEvent.click(save);
    });
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledOnce());
    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        conversation_id: 'conv-mobile-save',
        turn_intent: 'save-as-skill',
        files: [],
      })
    );
    expect(mobileActionSheetOpen.current).toBe(false);
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { name: '保存为 Skill' })).not.toBeInTheDocument();
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Preserve the ordinary draft during Skill saving.');
  });

  it('M454 invokes the existing manual-review request rather than changing AutoReview from mobile More', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    await render(
      <AcpSendBox conversation_id='conv-mobile-review' backend='xscience' messageState={makeMessageState()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-manual-review'));
    await waitFor(() => expect(requestFrameAuditMock).toHaveBeenCalledOnce());
    expect(requestFrameAuditMock).toHaveBeenCalledWith('conv-mobile-review');
    expect(notifyRuntimeInvalidationMock).toHaveBeenCalledWith('conv-mobile-review');
    expect(updateSessionConfigMock).not.toHaveBeenCalled();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    expect(mobileActionSheetOpen.current).toBe(false);
  });

  it('M454 Project files selects an exact version through the existing modal and Cancel adds nothing', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    mobilePlusProject.current = { projectId: 'project-mobile-plus' };
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    loadProjectArtifactsMock.mockResolvedValue([
      {
        artifactId: 'artifact-mobile',
        versionId: 'version-mobile',
        projectId: 'project-mobile-plus',
        filename: 'selected.csv',
        sizeBytes: 42,
      },
    ]);
    sendMessageInvokeMock.mockResolvedValueOnce({
      msg_id: 'selected-artifact-input',
      turn_id: 'selected-artifact-turn',
      runtime: { state: 'running', is_processing: true },
    });
    const view = await render(
      <AcpSendBox conversation_id='conv-mobile-project' backend='xscience' messageState={makeMessageState()} />
    );
    await act(async () => {
      await view.i18n.changeLanguage('en-US');
    });
    fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-project-files'));
    fireEvent.click(await screen.findByTestId('guid-project-file-option'));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByTestId('composer-context-chips')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-project-files'));
    fireEvent.click(await screen.findByTestId('guid-project-file-option'));
    fireEvent.click(screen.getByRole('button', { name: 'Select files' }));
    expect(screen.getByTestId('composer-context-chips')).toHaveTextContent('selected.csv');
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'send' }));
    });
    await waitFor(() =>
      expect(sendMessageInvokeMock).toHaveBeenCalledWith(
        expect.objectContaining({
          conversation_id: 'conv-mobile-project',
          artifact_refs: [
            expect.objectContaining({
              artifact_id: 'artifact-mobile',
              version_id: 'version-mobile',
              relation: 'attached',
            }),
          ],
        })
      )
    );
    expect(loadProjectArtifactsMock).toHaveBeenCalledTimes(2);
  });

  it('M454 Project files closes the old owner and ignores its late same-project response', async () => {
    isMobileMock.current = true;
    realMobileActionSheet.current = true;
    mobilePlusProject.current = { projectId: 'project-mobile-plus' };
    const pending =
      createDeferred<
        Array<{ artifactId: string; versionId: string; projectId: string; filename: string; sizeBytes: number }>
      >();
    loadProjectArtifactsMock.mockReturnValueOnce(pending.promise);
    const view = await render(
      <AcpSendBox
        conversation_id='conv-project-old'
        ownerId='owner-old'
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-project-files'));
    await waitFor(() => expect(loadProjectArtifactsMock).toHaveBeenCalledOnce());
    await act(async () => {
      view.rerender(
        <AcpSendBox
          conversation_id='conv-project-new'
          ownerId='owner-new'
          backend='xscience'
          messageState={makeMessageState()}
        />
      );
      pending.resolve([
        {
          artifactId: 'artifact-old-owner',
          versionId: 'version-old-owner',
          projectId: 'project-mobile-plus',
          filename: 'old-owner.csv',
          sizeBytes: 1,
        },
      ]);
      await pending.promise;
    });
    expect(screen.queryByTestId('guid-project-files-modal')).not.toBeInTheDocument();
    expect(screen.queryByText('old-owner.csv')).not.toBeInTheDocument();
    expect(screen.queryByTestId('composer-context-chips')).not.toBeInTheDocument();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
  });

  it.each([
    ['owner', false],
    ['owner', true],
    ['account', false],
    ['account', true],
  ] as const)(
    'M462 Project files fences %s in the committed layout before passive cleanup, pending=%s',
    async (transition, initiallyPending) => {
      isMobileMock.current = true;
      realMobileActionSheet.current = true;
      mobilePlusProject.current = { projectId: 'project-mobile-plus' };
      const previousAccount = getRendererAccountOwnerId();
      setRendererAccountOwner('selector-account-old');
      const oldArtifact = {
        artifactId: 'artifact-committed-old',
        versionId: 'version-committed-old',
        projectId: 'project-mobile-plus',
        filename: 'committed-old.csv',
        sizeBytes: 1,
      };
      const newArtifact = {
        ...oldArtifact,
        artifactId: 'artifact-current',
        versionId: 'version-current',
        filename: 'current.csv',
      };
      const pending = createDeferred<Array<typeof oldArtifact>>();
      loadProjectArtifactsMock.mockResolvedValue([newArtifact]);
      if (initiallyPending) loadProjectArtifactsMock.mockReturnValueOnce(pending.promise);
      else loadProjectArtifactsMock.mockResolvedValueOnce([oldArtifact]);
      const actualSelectionHook = composerContextSelection.useAcpComposerContextSelection;
      const selection = { current: null as ReturnType<typeof actualSelectionHook> | null };
      let oldConfirm: ReturnType<typeof actualSelectionHook>['handleProjectArtifactsSelected'] | undefined;
      const contextWrites = vi.fn();
      const selectorHook = vi
        .spyOn(composerContextSelection, 'useAcpComposerContextSelection')
        .mockImplementation((project, items, setItems, scope) => {
          const result = actualSelectionHook(
            project,
            items,
            (update) => {
              contextWrites();
              setItems(update);
            },
            scope
          );
          selection.current = result;
          return result;
        });
      const layoutObservations: boolean[] = [];
      const ScopeHarness = ({ switched }: { switched: boolean }) => {
        React.useLayoutEffect(() => {
          if (!switched) return;
          // This is the new owner's committed layout, before the selector's
          // passive close effect can run. Exercise both a retained event and a
          // saved real callback, even if the old DOM has already been removed.
          layoutObservations.push(Boolean(screen.queryByTestId('guid-project-files-modal')));
          screen.queryByRole<HTMLButtonElement>('button', { name: 'Select files' })?.click();
          oldConfirm?.([oldArtifact]);
          if (initiallyPending) pending.resolve([oldArtifact]);
        }, [switched]);
        return (
          <AcpSendBox
            conversation_id='conv-project-layout'
            ownerId={switched && transition === 'owner' ? 'owner-new' : 'owner-old'}
            backend='xscience'
            messageState={makeMessageState()}
          />
        );
      };
      let view: Awaited<ReturnType<typeof render>> | undefined;
      try {
        view = await render(<ScopeHarness switched={false} />);
        await act(async () => {
          await view!.i18n.changeLanguage('en-US');
        });
        fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
        fireEvent.click(await screen.findByTestId('mobile-action-sheet-project-files'));
        await waitFor(() => expect(loadProjectArtifactsMock).toHaveBeenCalledOnce());
        if (!initiallyPending) fireEvent.click(await screen.findByTestId('guid-project-file-option'));
        oldConfirm = selection.current!.handleProjectArtifactsSelected;
        contextWrites.mockClear();
        await act(async () => {
          if (transition === 'account') setRendererAccountOwner('selector-account-new');
          view!.rerender(<ScopeHarness switched />);
        });
        if (initiallyPending)
          await act(async () => {
            await pending.promise;
          });
        expect(layoutObservations).toEqual([false]);
        expect(contextWrites).not.toHaveBeenCalled();
        expect(screen.queryByText('committed-old.csv')).not.toBeInTheDocument();
        expect(screen.queryByTestId('composer-context-chips')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'mobile more' }));
        fireEvent.click(await screen.findByTestId('mobile-action-sheet-project-files'));
        fireEvent.click(await screen.findByTestId('guid-project-file-option'));
        fireEvent.click(screen.getByRole('button', { name: 'Select files' }));
        expect(screen.getByTestId('composer-context-chips')).toHaveTextContent('current.csv');
        expect(loadProjectArtifactsMock).toHaveBeenCalledTimes(2);
        expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      } finally {
        view?.unmount();
        selectorHook.mockRestore();
        setRendererAccountOwner(previousAccount);
      }
    }
  );

  it('places the real frame audit in the v1.1 add menu and removes the separate global branch selector', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    loadFramePlanMock.mockResolvedValue({
      artifactId: 'artifact-plan-desktop',
      versionId: null,
      filename: 'plan_conv-1.json',
      createdAt: null,
    });
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    expect(fileAttachProps.current?.onViewPlan).toBeUndefined();
    expect(runtimeOperationsProps.current?.onPlanReferenceChange).toEqual(expect.any(Function));
    act(() =>
      runtimeOperationsProps.current?.onPlanReferenceChange?.('conv-1', {
        artifactId: 'artifact-plan-desktop',
        versionId: null,
      })
    );
    const viewPlan = fileAttachProps.current?.onViewPlan as (() => void) | undefined;
    expect(viewPlan).toEqual(expect.any(Function));
    if (!viewPlan) throw new Error('Expected the current plan action');
    act(viewPlan);
    expect(emitterEmitMock).toHaveBeenCalledWith('xscience.runtime.plan.open', 'conv-1');
    expect(document.querySelector('[data-aside-owner="external"]')).toBeInTheDocument();
    expect(fileAttachProps.current?.onRequestReview).toEqual(expect.any(Function));
    expect(fileAttachProps.current?.reviewInFlight).toBe(false);
    expect(fileAttachProps.current?.reviewDisabled).toBe(false);
    expect(sessionOptionsProps.current?.onRequestReview).toBeUndefined();
    expect(sessionOptionsProps.current?.reviewInFlight).toBeUndefined();
    expect(sessionOptionsProps.current?.reviewDisabled).toBeUndefined();
    expect(fileAttachProps.current?.reviewLabelMode).toBe('manual');
    expect(fileAttachProps.current?.onSaveSkill).toEqual(expect.any(Function));
    expect(screen.queryByTestId('branch-menu')).not.toBeInTheDocument();
    const requestReview = fileAttachProps.current?.onRequestReview;
    expect(requestReview).toEqual(expect.any(Function));
    if (typeof requestReview !== 'function') throw new Error('Expected a frame audit handler');
    await act(async () => {
      await requestReview();
    });
    expect(requestFrameAuditMock).toHaveBeenCalledWith('conv-1');
    expect(notifyRuntimeInvalidationMock).toHaveBeenCalledWith('conv-1');
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
  });

  it('keeps manual review state on the add menu only', async () => {
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    expect(runtimeOperationsProps.current?.onReviewActivityChange).toEqual(expect.any(Function));

    await act(async () => {
      runtimeOperationsProps.current?.onReviewActivityChange?.(true);
    });
    expect(fileAttachProps.current?.reviewInFlight).toBe(true);
    expect(sessionOptionsProps.current?.reviewInFlight).toBeUndefined();
    expect(sessionOptionsProps.current?.onRequestReview).toBeUndefined();

    await act(async () => {
      runtimeOperationsProps.current?.onReviewActivityChange?.(false);
    });
    expect(fileAttachProps.current?.reviewInFlight).toBe(false);
  });

  it('starts Save-as-Skill as one hidden typed control with canonical acceptance, no dialog, and an intact draft', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendBoxDraftSeed.current = 'Keep this unsent draft.';
    const accepted = createDeferred<{
      msg_id: string;
      turn_id: string;
      runtime: { state: string; is_processing: boolean };
    }>();
    sendMessageInvokeMock.mockReturnValueOnce(accepted.promise);
    await render(<AcpSendBox conversation_id='conv-skill-task' backend='xscience' messageState={makeMessageState()} />);
    const action = fileAttachProps.current?.onSaveSkill;
    if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');
    let first!: Promise<void>;
    await act(async () => {
      first = action() as Promise<void>;
      void action();
    });
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        conversation_id: 'conv-skill-task',
        turn_intent: 'save-as-skill',
        files: [],
        input: '将上面任务的执行过程梳理为可复用的 Skill，并保存到我的技能库。',
      })
    );
    expect(addOrUpdateMessageMock).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByRole('status', { name: '保存为 Skill' })).not.toBeInTheDocument();
    expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(true);
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    await act(async () => {
      accepted.resolve({
        msg_id: 'skill-control-message',
        turn_id: 'skill-control-turn',
        runtime: { state: 'running', is_processing: true },
      });
      await first;
    });
    expect(runtimeViewMock.markSendAccepted).toHaveBeenCalledWith(
      'skill-control-turn',
      expect.objectContaining({ state: 'running' }),
      'skill-control-message',
      { ticket: runtimeViewMock.markSendStarted.mock.results[0]?.value, receipt: undefined }
    );
    expect(addOrUpdateMessageMock).toHaveBeenCalledWith(
      expect.objectContaining({
        msg_id: 'skill-control-message',
        hidden: true,
        turn_intent: 'save-as-skill',
        status: 'finish',
      }),
      false
    );
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this unsent draft.');
  });

  it('reserves ordinary send and draft editing before awaiting Save-as-Skill branch authority', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendBoxDraftSeed.current = 'Keep this draft while resolving the branch.';
    getSelectedBranchMock.mockReturnValue('branch-save');
    const branch = createDeferred<{
      activeBranchId: string;
      generation: number;
      branches: Array<{ id: string }>;
    }>();
    loadBranchStateMock.mockReturnValueOnce(branch.promise);
    sendMessageInvokeMock.mockImplementationOnce(acceptSkillTaskSend);
    await render(
      <AcpSendBox conversation_id='conv-skill-admission' backend='xscience' messageState={makeMessageState()} />
    );
    const save = fileAttachProps.current?.onSaveSkill;
    if (typeof save !== 'function') throw new Error('Missing Save-as-Skill entry');
    const send = screen.getByRole('button', { name: 'send' });
    let saving!: Promise<void>;
    await act(async () => {
      saving = save() as Promise<void>;
      // These controls still hold the pre-render handlers in the same tick.
      fireEvent.click(send);
      fireEvent.click(screen.getByRole('button', { name: 'change' }));
      void save();
    });
    expect(runtimeViewMock.markSendStarted).toHaveBeenCalledOnce();
    expect(loadBranchStateMock).toHaveBeenCalledOnce();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'send' })).toBeDisabled();
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this draft while resolving the branch.');
    expect(addOrUpdateMessageMock).not.toHaveBeenCalled();
    await act(async () => {
      branch.resolve({ activeBranchId: 'branch-main', generation: 8, branches: [{ id: 'branch-save' }] });
      await saving;
    });
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        turn_intent: 'save-as-skill',
        session_options: expect.objectContaining({
          target_branch_id: 'branch-save',
          expected_branch_id: 'branch-main',
          expected_generation: 8,
        }),
      })
    );
    expect(runtimeViewMock.markSendStarted).toHaveBeenCalledOnce();
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
  });

  it('does not launch Save-as-Skill while another task owns the conversation', async () => {
    await render(
      <AcpSendBox conversation_id='conv-skill-owned' backend='xscience' messageState={makeMessageState()} />
    );
    expect(fileAttachProps.current?.saveAsSkillDisabled).toBe(true);
    const action = fileAttachProps.current?.onSaveSkill;
    if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');
    await act(async () => {
      await action();
    });
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it.each(['conversation-switch', 'unmount'] as const)(
    'releases its own unsubmitted Save reservation on %s without dispatching a stale command',
    async (transition) => {
      runtimeViewMock.isProcessing = false;
      runtimeViewMock.canSendMessage = true;
      runtimeViewMock.state = 'idle';
      runtimeViewMock.view.taskStatus = 'finished';
      getSelectedBranchMock.mockReturnValue('branch-save');
      const branch = createDeferred<{ activeBranchId: string; generation: number; branches: Array<{ id: string }> }>();
      loadBranchStateMock.mockReturnValueOnce(branch.promise);
      const view = await render(
        <AcpSendBox conversation_id='conv-preparing' backend='xscience' messageState={makeMessageState()} />
      );
      const save = fileAttachProps.current?.onSaveSkill;
      if (typeof save !== 'function') throw new Error('Missing Save-as-Skill entry');
      let pending!: Promise<void>;
      await act(async () => {
        pending = save() as Promise<void>;
      });
      const ticket = runtimeViewMock.markSendStarted.mock.results[0]?.value;
      expect(ticket).toEqual({});
      if (transition === 'unmount') view.unmount();
      else
        view.rerender(
          <AcpSendBox conversation_id='conv-replacement' backend='xscience' messageState={makeMessageState()} />
        );
      expect(runtimeViewMock.markSendFailed).toHaveBeenCalledWith('control_submission_cancelled', ticket);
      await act(async () => {
        branch.resolve({ activeBranchId: 'main', generation: 1, branches: [{ id: 'branch-save' }] });
        await pending;
      });
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
      expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    }
  );

  it.each(['hidden-control', 'ordinary-input'] as const)(
    'restores the Save menu only when the active input is %s, not merely the same frame',
    async (activeInput) => {
      runtimeViewMock.activeTurnId = 'conv-restored';
      runtimeViewMock.view.activeInputMessageId = activeInput;
      messageListMock.current = [
        {
          id: 'hidden-control',
          msg_id: 'hidden-control',
          conversation_id: 'conv-restored',
          type: 'text',
          position: 'right',
          content: { content: 'Save as skill' },
          hidden: true,
          turn_intent: 'save-as-skill',
        },
        {
          id: 'ordinary-input',
          conversation_id: 'conv-restored',
          type: 'text',
          position: 'right',
          content: { content: 'Continue the task' },
        },
      ];
      await render(<AcpSendBox conversation_id='conv-restored' backend='xscience' messageState={makeMessageState()} />);
      expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(activeInput === 'hidden-control');
      expect(screen.queryByRole('status', { name: '保存为 Skill' })).not.toBeInTheDocument();
      expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    }
  );

  it('refreshes Skill catalogs after the conversation task finishes without selecting or replacing the draft', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendBoxDraftSeed.current = 'Keep this unsent draft.';
    loadComposerCapabilitiesMock.mockResolvedValue({ skills: ['existing-workflow'], mcpStatuses: [] });
    sendMessageInvokeMock.mockImplementationOnce(acceptSkillTaskSend);
    const messageState = makeMessageState();
    const view = await render(
      <AcpSendBox conversation_id='conv-skill-completion' backend='xscience' messageState={messageState} />
    );
    const action = fileAttachProps.current?.onSaveSkill;
    if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');

    await act(async () => {
      await action();
    });
    expect(fileAttachProps.current?.saveAsSkillDisabled).toBe(true);
    expect(fileAttachProps.current?.loadedSkills).toEqual(['existing-workflow']);
    expect(loadComposerCapabilitiesMock).toHaveBeenCalledTimes(1);
    expect(messageState.fetchSlashCommands).not.toHaveBeenCalled();
    expect(screen.queryByRole('status', { name: '保存为 Skill' })).not.toBeInTheDocument();
    expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(true);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    loadComposerCapabilitiesMock.mockResolvedValue({
      skills: ['existing-workflow', 'session-skill'],
      mcpStatuses: [],
    });
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    view.rerender(
      <AcpSendBox conversation_id='conv-skill-completion' backend='xscience' messageState={messageState} />
    );

    await waitFor(() => expect(fileAttachProps.current?.loadedSkills).toEqual(['existing-workflow', 'session-skill']));
    expect(loadComposerCapabilitiesMock).toHaveBeenCalledTimes(2);
    expect(messageState.fetchSlashCommands).toHaveBeenCalledExactlyOnceWith({ fresh: true });
    expect(fileAttachProps.current?.selectedSkillNames).toEqual([]);
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this unsent draft.');
    expect(screen.queryByRole('status', { name: '保存为 Skill' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '使用 Skill' })).not.toBeInTheDocument();
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
    expect(messageSuccessMock).not.toHaveBeenCalled();
  });

  it('refreshes Save-as-Skill branch authority only after an explicit rejected CAS while retaining the command and input', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendBoxDraftSeed.current = 'Keep this unsent draft.';
    loadBranchStateMock
      .mockResolvedValueOnce({ activeBranchId: 'branch-main', generation: 1, branches: [{ id: 'branch-main' }] })
      .mockResolvedValueOnce({ activeBranchId: 'branch-main', generation: 2, branches: [{ id: 'branch-main' }] });
    sendMessageInvokeMock
      .mockRejectedValueOnce(
        new BackendHttpError({
          method: 'POST',
          path: '/api/conversations/conv-skill-cas-retry/messages',
          status: 409,
          body: { code: 'CONVERSATION_BRANCH_STATE_STALE', error: 'Conversation branch changed; refresh and retry' },
        })
      )
      .mockImplementationOnce(acceptSkillTaskSend);
    const view = await render(
      <AcpSendBox conversation_id='conv-skill-cas-retry' backend='xscience' messageState={makeMessageState()} />
    );
    const action = fileAttachProps.current?.onSaveSkill;
    if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');
    await act(async () => {
      await action();
    });
    expect(screen.getByRole('button', { name: '重试保存' })).toBeEnabled();
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
    expect(loadBranchStateMock).toHaveBeenCalledOnce();
    await act(async () => {
      await view.i18n.changeLanguage('en-US');
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Retry save' }));
    });
    await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
    const first = sendMessageInvokeMock.mock.calls[0]?.[0] as {
      loading_id: string;
      input: string;
      session_options: Record<string, unknown>;
    };
    const retry = sendMessageInvokeMock.mock.calls[1]?.[0] as typeof first;
    expect(first.session_options).toMatchObject({
      target_branch_id: 'branch-main',
      expected_branch_id: 'branch-main',
      expected_generation: 1,
    });
    expect(retry).toEqual({ ...first, session_options: { ...first.session_options, expected_generation: 2 } });
    expect(retry.loading_id).toBe(first.loading_id);
    expect(retry.input).toBe('将上面任务的执行过程梳理为可复用的 Skill，并保存到我的技能库。');
    expect(loadBranchStateMock).toHaveBeenCalledTimes(2);
    expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(true);
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this unsent draft.');
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
  });

  it.each([
    ['network failure', new Error('synthetic-dispatch-failure')],
    [
      'server failure',
      new BackendHttpError({
        method: 'POST',
        path: '/api/conversations/conv-skill-dispatch-retry/messages',
        status: 503,
        body: { code: 'CONVERSATION_BRANCH_STATE_STALE' },
      }),
    ],
    [
      'unrelated conflict',
      new BackendHttpError({
        method: 'POST',
        path: '/api/conversations/conv-skill-dispatch-retry/messages',
        status: 409,
        body: { code: 'CONFLICT', error: 'Command identity conflicts with an existing payload' },
      }),
    ],
  ] as const)(
    'retries an unconfirmed Save-as-Skill submission with the same typed-send identity and no dialog (%s)',
    async (_label, failure) => {
      runtimeViewMock.isProcessing = false;
      runtimeViewMock.canSendMessage = true;
      runtimeViewMock.state = 'idle';
      runtimeViewMock.view.taskStatus = 'finished';
      sendBoxDraftSeed.current = 'Keep this unsent draft.';
      getSelectedBranchMock.mockReturnValue('branch-original');
      loadBranchStateMock.mockResolvedValue({
        activeBranchId: 'branch-main',
        generation: 8,
        branches: [{ id: 'branch-main' }, { id: 'branch-original' }],
      });
      sendMessageInvokeMock.mockRejectedValueOnce(failure).mockImplementationOnce(acceptSkillTaskSend);
      const messageState = makeMessageState();
      const view = await render(
        <AcpSendBox conversation_id='conv-skill-dispatch-retry' backend='xscience' messageState={messageState} />
      );
      const action = fileAttachProps.current?.onSaveSkill;
      if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');
      await act(async () => {
        await action();
      });

      const alert = screen.getByRole('alert', { name: '保存为 Skill' });
      expect(alert).toHaveTextContent('尚未确认 Skill 任务已提交');
      expect(alert).not.toHaveTextContent('synthetic-dispatch-failure');
      expect(screen.getByRole('button', { name: '重试保存' })).toBeEnabled();
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(messageState.fetchSlashCommands).not.toHaveBeenCalled();
      expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
      await act(async () => {
        await view.i18n.changeLanguage('en-US');
      });
      expect(screen.getByRole('alert', { name: 'Save as Skill' })).toHaveTextContent(
        'The Skill task could not be confirmed'
      );
      getSelectedBranchMock.mockReturnValue('branch-newer');
      getBranchRevisionMock.mockReturnValue(9);
      loadBranchStateMock.mockResolvedValue({
        activeBranchId: 'branch-newer',
        generation: 9,
        branches: [{ id: 'branch-newer' }, { id: 'branch-original' }],
      });

      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Retry save' }));
      });
      await waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
      const first = sendMessageInvokeMock.mock.calls[0]?.[0] as {
        loading_id: string;
        turn_intent: string;
        input: string;
        session_options: Record<string, unknown>;
      };
      const retry = sendMessageInvokeMock.mock.calls[1]?.[0] as {
        loading_id: string;
        turn_intent: string;
        input: string;
      };
      expect(retry.loading_id).toBe(first.loading_id);
      expect(first.input).toBe('将上面任务的执行过程梳理为可复用的 Skill，并保存到我的技能库。');
      expect(first.session_options).toMatchObject({
        target_branch_id: 'branch-original',
        expected_branch_id: 'branch-main',
        expected_generation: 8,
      });
      expect(retry).toEqual(first);
      expect(loadBranchStateMock).toHaveBeenCalledOnce();
      expect(first.turn_intent).toBe('save-as-skill');
      expect(retry.turn_intent).toBe('save-as-skill');
      expect(screen.queryByRole('alert', { name: 'Save as Skill' })).not.toBeInTheDocument();
      expect(screen.queryByRole('status', { name: 'Save as Skill' })).not.toBeInTheDocument();
      expect(fileAttachProps.current?.saveAsSkillInFlight).toBe(true);
      expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('Keep this unsent draft.');
      expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    }
  );

  it('retries only catalog reads when a completed Skill task cannot refresh its lists', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    sendMessageInvokeMock.mockImplementationOnce(acceptSkillTaskSend);
    const messageState = makeMessageState();
    messageState.fetchSlashCommands = vi
      .fn()
      .mockRejectedValueOnce(new Error('slash list unavailable'))
      .mockResolvedValue(undefined);
    const view = await render(
      <AcpSendBox conversation_id='conv-skill-refresh' backend='xscience' messageState={messageState} />
    );
    const action = fileAttachProps.current?.onSaveSkill;
    if (typeof action !== 'function') throw new Error('Missing Save-as-Skill entry');
    await act(async () => {
      await action();
    });

    loadComposerCapabilitiesMock.mockRejectedValue(new Error('capability list unavailable'));
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    view.rerender(<AcpSendBox conversation_id='conv-skill-refresh' backend='xscience' messageState={messageState} />);
    const alert = await screen.findByRole('alert', { name: '保存为 Skill' });
    expect(alert).toHaveTextContent('Skill 任务已结束，但列表刷新失败');
    expect(alert).toHaveTextContent('不重新执行任务');
    expect(messageState.fetchSlashCommands).toHaveBeenCalledTimes(1);
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();

    loadComposerCapabilitiesMock.mockResolvedValue({ skills: ['session-skill'], mcpStatuses: [] });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '重试刷新' }));
    });
    await waitFor(() => expect(fileAttachProps.current?.loadedSkills).toEqual(['session-skill']));
    expect(loadComposerCapabilitiesMock).toHaveBeenCalledTimes(3);
    expect(messageState.fetchSlashCommands).toHaveBeenCalledTimes(2);
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert', { name: '保存为 Skill' })).not.toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(fileAttachProps.current?.selectedSkillNames).toEqual([]);
  });

  it('SkillActivation still requires explicit expert confirmation without saving or sending automatically', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    loadComposerCapabilitiesMock.mockResolvedValue({ skills: ['existing-workflow'], mcpStatuses: [] });
    await render(
      <AcpSendBox
        conversation_id='conv-skill-explicit-activation'
        backend='xscience'
        initialConversation={
          {
            id: 'conv-skill-explicit-activation',
            type: 'acp',
            extra: { agent_name: 'canonical-expert' },
          } as TChatConversation
        }
        messageState={makeMessageState()}
      />
    );
    expect(fileAttachProps.current?.onEnableSkill).toBeTypeOf('function');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(loadSkillLibraryMock).not.toHaveBeenCalled();
    const enableSkill = fileAttachProps.current?.onEnableSkill;
    if (typeof enableSkill !== 'function') throw new Error('Expected an expert activation handler');
    act(() => {
      enableSkill('session-skill');
    });
    expect(screen.getByRole('dialog', { name: '为当前专家启用 Skill' })).toHaveTextContent('session-skill');
    expect(loadSkillLibraryMock).not.toHaveBeenCalled();
    expect(fileAttachProps.current?.selectedSkillNames).toEqual([]);
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
  });

  it('SkillActivation includes enabled library Skills in the mobile picker without bypassing confirmation', async () => {
    isMobileMock.current = true;
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    loadComposerCapabilitiesMock.mockResolvedValue({ skills: ['existing-workflow'], mcpStatuses: [] });
    loadSkillLibraryMock.mockResolvedValue([
      { name: 'new-workflow', enabled: true, source: 'personal' },
      { name: 'disabled-workflow', enabled: false, source: 'personal' },
      { name: 'outside-builtin', enabled: true, source: 'x_science_llm' },
    ]);
    await render(
      <AcpSendBox
        conversation_id='conv-skill-mobile-activation'
        ownerId='owner-mobile-activation'
        backend='xscience'
        initialConversation={
          {
            id: 'conv-skill-mobile-activation',
            type: 'acp',
            extra: { agent_name: 'canonical-expert' },
          } as TChatConversation
        }
        messageState={makeMessageState()}
      />
    );
    await waitFor(() =>
      expect(mobileActionSheetEntries.current.find((entry) => entry.key === 'skills')?.submenu?.options).toEqual(
        expect.arrayContaining([expect.objectContaining({ key: 'new-workflow', description: '启用…' })])
      )
    );
    const entry = mobileActionSheetEntries.current.find((item) => item.key === 'skills');
    expect(entry?.submenu?.options?.some((item) => item.key === 'disabled-workflow')).toBe(false);
    expect(entry?.submenu?.options?.some((item) => item.key === 'outside-builtin')).toBe(false);
    expect(entry?.submenu?.options?.some((item) => item.active)).toBe(false);
    act(() => {
      entry?.submenu?.onSelect?.('new-workflow');
    });
    expect(screen.getByRole('dialog', { name: '为当前专家启用 Skill' })).toHaveTextContent('new-workflow');
    expect(
      mobileActionSheetEntries.current
        .find((item) => item.key === 'skills')
        ?.submenu?.options?.some((item) => item.active)
    ).toBe(false);
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
  });

  it('keeps completed Skill tasks separate from current-assistant permissions and selection', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    loadComposerCapabilitiesMock.mockResolvedValue({ skills: ['existing-skill'], mcpStatuses: [] });
    sendMessageInvokeMock.mockImplementationOnce(acceptSkillTaskSend);
    const messageState = makeMessageState();
    messageState.fetchSlashCommands = vi.fn().mockResolvedValue(undefined);
    const view = await render(
      <AcpSendBox conversation_id='conv-skill-restricted-assistant' backend='xscience' messageState={messageState} />
    );
    await waitFor(() => expect(fileAttachProps.current?.loadedSkills).toEqual(['existing-skill']));
    const saveAsSkill = fileAttachProps.current?.onSaveSkill;
    if (typeof saveAsSkill !== 'function') throw new Error('Expected a save-as-skill handler');
    await act(async () => {
      await saveAsSkill();
    });

    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    view.rerender(
      <AcpSendBox conversation_id='conv-skill-restricted-assistant' backend='xscience' messageState={messageState} />
    );
    await waitFor(() => expect(loadComposerCapabilitiesMock).toHaveBeenCalledTimes(2));
    expect(messageState.fetchSlashCommands).toHaveBeenCalledExactlyOnceWith({ fresh: true });
    expect(fileAttachProps.current?.loadedSkills).toEqual(['existing-skill']);
    expect(fileAttachProps.current?.selectedSkillNames).toEqual([]);
    expect(fileAttachProps.current?.onEnableSkill).toBeUndefined();
    expect(screen.queryByRole('button', { name: '使用 Skill' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '启用并选择' })).not.toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '重试保存' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '重试刷新' })).not.toBeInTheDocument();
    expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
  });

  it.each(['conversation-switch', 'unmount'] as const)(
    'isolates late Save-as-Skill dispatch acceptance after %s',
    async (transition) => {
      runtimeViewMock.isProcessing = false;
      runtimeViewMock.canSendMessage = true;
      runtimeViewMock.state = 'idle';
      runtimeViewMock.view.taskStatus = 'finished';
      const pendingSave = createDeferred<{
        msg_id: string;
        turn_id: string;
        runtime: { state: string; is_processing: boolean };
      }>();
      sendMessageInvokeMock.mockReturnValueOnce(pendingSave.promise);
      const conversationId = `conv-skill-late-${transition}`;
      const originalState = makeMessageState();
      const replacementState = makeMessageState();
      const view = await render(
        <AcpSendBox conversation_id={conversationId} backend='xscience' messageState={originalState} />
      );
      const saveAsSkill = fileAttachProps.current?.onSaveSkill;
      if (typeof saveAsSkill !== 'function') throw new Error('Expected a save-as-skill handler');
      let saving!: Promise<void>;
      await act(async () => {
        saving = saveAsSkill() as Promise<void>;
        await Promise.resolve();
      });
      if (transition === 'unmount') {
        view.unmount();
      } else {
        await act(async () => {
          view.rerender(
            <AcpSendBox conversation_id='conv-skill-replacement' backend='xscience' messageState={replacementState} />
          );
        });
        await act(async () => {
          view.rerender(
            <AcpSendBox conversation_id={conversationId} backend='xscience' messageState={replacementState} />
          );
        });
      }
      const capabilityReads = loadComposerCapabilitiesMock.mock.calls.length;
      messageSuccessMock.mockClear();
      messageErrorMock.mockClear();
      emitterEmitMock.mockClear();
      await act(async () => {
        pendingSave.resolve({
          msg_id: 'late-skill-message',
          turn_id: 'late-skill-turn',
          runtime: { state: 'running', is_processing: true },
        });
        await saving;
      });
      expect(loadComposerCapabilitiesMock).toHaveBeenCalledTimes(capabilityReads);
      expect(originalState.fetchSlashCommands).not.toHaveBeenCalled();
      expect(replacementState.fetchSlashCommands).not.toHaveBeenCalled();
      expect(screen.queryByRole('status', { name: '保存为 Skill' })).not.toBeInTheDocument();
      expect(screen.queryByRole('alert', { name: '保存为 Skill' })).not.toBeInTheDocument();
      expect(messageSuccessMock).not.toHaveBeenCalled();
      expect(messageErrorMock).not.toHaveBeenCalled();
      expect(emitterEmitMock).not.toHaveBeenCalledWith('chat.history.refresh');
      expect(runtimeViewMock.markSendAccepted).not.toHaveBeenCalled();
      expect(sendMessageInvokeMock).toHaveBeenCalledOnce();
      expect(saveConversationAsSkillMock).not.toHaveBeenCalled();
    }
  );

  it('does not expose a completed review request after switching conversations', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    const pendingReview = createDeferred<{ accepted: boolean }>();
    requestFrameAuditMock.mockReturnValueOnce(pendingReview.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    const requestReview = fileAttachProps.current?.onRequestReview;
    if (typeof requestReview !== 'function') throw new Error('Expected a frame audit handler');

    let request!: Promise<void>;
    await act(async () => {
      request = requestReview() as Promise<void>;
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    messageSuccessMock.mockClear();
    emitterEmitMock.mockClear();

    await act(async () => {
      pendingReview.resolve({ accepted: true });
      await request;
    });

    expect(messageSuccessMock).not.toHaveBeenCalled();
    expect(emitterEmitMock).not.toHaveBeenCalledWith('chat.history.refresh');
    expect(notifyRuntimeInvalidationMock).not.toHaveBeenCalled();
  });

  it('does not expose a failed review request after switching conversations', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    const pendingReview = createDeferred<never>();
    requestFrameAuditMock.mockReturnValueOnce(pendingReview.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    const requestReview = fileAttachProps.current?.onRequestReview;
    if (typeof requestReview !== 'function') throw new Error('Expected a frame audit handler');

    let request!: Promise<void>;
    await act(async () => {
      request = requestReview() as Promise<void>;
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    messageErrorMock.mockClear();

    await act(async () => {
      pendingReview.reject(new Error('old review failed'));
      await request;
    });

    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it('does not apply an old persistent option response to a replacement conversation', async () => {
    const pendingUpdate = createDeferred<void>();
    updateSessionConfigMock.mockReturnValueOnce(pendingUpdate.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    const updateOption = sessionOptionsProps.current?.onPersistentOptionChange;
    if (typeof updateOption !== 'function') throw new Error('Expected a persistent option handler');

    let request!: Promise<void>;
    await act(async () => {
      request = updateOption('autoReview', true) as Promise<void>;
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);

    await act(async () => {
      pendingUpdate.resolve();
      await request;
    });

    expect((sessionOptionsProps.current?.value as { autoReview?: boolean } | undefined)?.autoReview).toBe(false);
  });

  it('does not expose a completed goal clear after switching conversations', async () => {
    const pendingUpdate = createDeferred<void>();
    updateSessionConfigMock.mockReturnValueOnce(pendingUpdate.promise);
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );
    const clearGoal = sessionOptionsProps.current?.onClearGoal;
    if (typeof clearGoal !== 'function') throw new Error('Expected a clear goal handler');

    let request!: Promise<void>;
    await act(async () => {
      request = clearGoal() as Promise<void>;
      await Promise.resolve();
    });
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    messageSuccessMock.mockClear();

    await act(async () => {
      pendingUpdate.resolve();
      await request;
    });

    expect(messageSuccessMock).not.toHaveBeenCalled();
  });

  it('does not expose a completed mode change after switching conversations', async () => {
    const pendingMode = createDeferred<unknown[]>();
    const setConfigOption = vi.fn().mockReturnValueOnce(pendingMode.promise);
    isMobileMock.current = true;
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    useAcpConfigOptionsMock.mockReturnValue({
      mode: {
        id: 'session_mode',
        category: 'mode',
        currentValue: 'send',
        options: [
          { value: 'send', label: 'Send' },
          { value: 'plan', label: 'Plan' },
        ],
      },
      model: null,
      thoughtLevel: null,
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
    });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    mobileActionSheetEntries.current.find((entry) => entry.key === 'send-mode')?.submenu?.onSelect?.('plan');
    await act(async () => Promise.resolve());
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    messageSuccessMock.mockClear();

    await act(async () => {
      pendingMode.resolve([]);
      await pendingMode.promise;
    });

    expect(messageSuccessMock).not.toHaveBeenCalled();
  });

  it('C394 mobile unified owner displays authoritative readback rather than optimistic membership', async () => {
    setRendererAccountOwner('compute-owner');
    isMobileMock.current = true;
    loadComputeProvidersMock.mockResolvedValue([
      { name: 'ssh:first', displayName: 'First', checked: true },
      { name: 'ssh:canonical', displayName: 'Canonical', checked: true },
    ]);
    loadSessionComputeProvidersMock.mockResolvedValueOnce(['ssh:first']).mockResolvedValue(['ssh:canonical']);
    await render(
      <AcpSendBox
        conversation_id='conv-1'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-1', 'inc-1', 'root-1')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    const entry = () => mobileActionSheetEntries.current.find((item) => item.key === 'session-compute');
    await waitFor(() => expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toBeDefined());
    await act(async () => entry()?.submenu?.onSelect?.('ssh:first'));
    await waitFor(() => expect(loadSessionComputeProvidersMock).toHaveBeenCalledTimes(2));
    expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:canonical')).toMatchObject({
      kind: 'checkbox',
      checked: true,
    });
    expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toMatchObject({ checked: false });
  });

  it('C394 mobile unified owner freezes a lost ACK and retries GET without replaying PUT', async () => {
    setRendererAccountOwner('compute-owner');
    isMobileMock.current = true;
    loadComputeProvidersMock.mockResolvedValue([{ name: 'ssh:first', displayName: 'First', checked: true }]);
    loadSessionComputeProvidersMock.mockResolvedValueOnce(['ssh:first']).mockResolvedValue([]);
    setSessionComputeProviderMock.mockRejectedValueOnce(new TypeError('synthetic lost ACK'));
    await render(
      <AcpSendBox
        conversation_id='conv-1'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-1', 'inc-1', 'root-1')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    const entry = () => mobileActionSheetEntries.current.find((item) => item.key === 'session-compute');
    await waitFor(() => expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toBeDefined());
    await act(async () => entry()?.submenu?.onSelect?.('ssh:first'));
    await waitFor(() =>
      expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toMatchObject({
        disabled: true,
        checked: true,
      })
    );
    expect(entry()?.submenu?.options?.find((row) => row.kind === 'action')).toBeDefined();
    await act(async () => entry()?.submenu?.onSelect?.('ssh:first'));
    await act(async () => entry()?.submenu?.onSelect?.('__retry__'));
    await waitFor(() => expect(loadSessionComputeProvidersMock).toHaveBeenCalledTimes(2));
    expect(setSessionComputeProviderMock).toHaveBeenCalledTimes(1);
    expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toMatchObject({
      disabled: false,
      checked: false,
    });
  });

  it('C394 mobile unified owner retains the pending write across viewport changes and ignores an older inspection close', async () => {
    setRendererAccountOwner('compute-owner');
    isMobileMock.current = true;
    const pending = createDeferred<void>();
    loadComputeProvidersMock.mockResolvedValue([{ name: 'ssh:first', displayName: 'First', checked: true }]);
    loadSessionComputeProvidersMock.mockResolvedValueOnce(['ssh:first']).mockResolvedValue([]);
    setSessionComputeProviderMock.mockReturnValueOnce(pending.promise);
    const node = () => (
      <AcpSendBox
        conversation_id='conv-1'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-1', 'inc-1', 'root-1')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    const view = await render(node());
    const entry = () => mobileActionSheetEntries.current.find((item) => item.key === 'session-compute');
    await waitFor(() => expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toBeDefined());
    const oldSelect = entry()?.submenu?.onSelect;
    await act(async () => oldSelect?.('ssh:first'));
    isMobileMock.current = false;
    view.rerender(node());
    const desktop = () => sessionOptionsProps.current?.computeControl as SessionComputeControl;
    let closeOld!: () => void;
    let closeNew!: () => void;
    await act(async () => {
      closeOld = desktop().inspect();
    });
    await act(async () => {
      closeNew = desktop().inspect();
      closeOld();
    });
    expect(loadSessionComputeProvidersMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      oldSelect?.('ssh:first');
      await desktop().change('ssh:first', false);
    });
    expect(setSessionComputeProviderMock).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.resolve();
      await pending.promise;
    });
    await waitFor(() => expect(desktop()).toMatchObject({ status: 'ready', selection: [], pending: null }));
    expect(loadSessionComputeProvidersMock).toHaveBeenCalledTimes(2);
    await act(async () => closeNew());
    isMobileMock.current = true;
    view.rerender(node());
    await waitFor(() =>
      expect(entry()?.submenu?.options?.find((row) => row.key === 'ssh:first')).toMatchObject({
        kind: 'checkbox',
        checked: false,
        disabled: false,
      })
    );
    expect(setSessionComputeProviderMock).toHaveBeenCalledTimes(1);
  });

  it('C394 mobile unified owner keeps failed and empty catalogs reachable without a false Local observation', async () => {
    setRendererAccountOwner('compute-owner');
    isMobileMock.current = true;
    loadComputeProvidersMock.mockRejectedValueOnce(new Error('synthetic catalog failure')).mockResolvedValue([]);
    loadSessionComputeProvidersMock.mockResolvedValue([]);
    await render(
      <AcpSendBox
        conversation_id='conv-1'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-1', 'inc-1', 'root-1')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    const entry = () => mobileActionSheetEntries.current.find((item) => item.key === 'session-compute');
    await waitFor(() =>
      expect(entry()?.submenu?.options?.find((row) => row.key === '__retry__')).toMatchObject({ kind: 'action' })
    );
    expect(entry()?.submenu?.options?.find((row) => row.key === '__local__')).toMatchObject({
      active: false,
      disabled: true,
    });
    await act(async () => entry()?.submenu?.onSelect?.('__retry__'));
    await waitFor(() =>
      expect(entry()?.submenu?.options?.find((row) => row.key === '__local__')).toMatchObject({
        active: true,
        disabled: false,
      })
    );
    expect(setSessionComputeProviderMock).not.toHaveBeenCalled();
  });

  it('does not apply an old compute response to a replacement conversation', async () => {
    const pendingCompute = createDeferred<void>();
    setRendererAccountOwner('compute-owner');
    isMobileMock.current = true;
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    loadAssistantsMock.mockResolvedValue([]);
    loadComputeProvidersMock.mockResolvedValue([{ name: 'local-cpu', displayName: 'Local CPU', checked: true }]);
    loadSessionComputeProvidersMock.mockResolvedValue([]);
    setSessionComputeProviderMock.mockReturnValueOnce(pendingCompute.promise);
    const view = await render(
      <AcpSendBox
        conversation_id='conv-1'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-1', 'inc-1', 'root-1')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    await waitFor(() =>
      expect(
        mobileActionSheetEntries.current
          .find((entry) => entry.key === 'session-compute')
          ?.submenu?.options?.find((row) => row.key === 'local-cpu')
      ).toMatchObject({ disabled: false })
    );
    const oldSelect = mobileActionSheetEntries.current.find((entry) => entry.key === 'session-compute')?.submenu
      ?.onSelect;
    oldSelect?.('local-cpu');
    await act(async () => Promise.resolve());
    expect(setSessionComputeProviderMock).toHaveBeenCalledWith('root-1', 'local-cpu', true);
    loadAssistantsMock.mockImplementationOnce(() => new Promise(() => {}));
    loadComputeProvidersMock.mockImplementationOnce(() => new Promise(() => {}));
    loadSessionComputeProvidersMock.mockImplementationOnce(() => new Promise(() => {}));
    view.rerender(
      <AcpSendBox
        conversation_id='conv-2'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-2', 'inc-2', 'root-2')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );

    await act(async () => {
      pendingCompute.resolve();
      await pendingCompute.promise;
    });

    const current = mobileActionSheetEntries.current.find((entry) => entry.key === 'session-compute');
    expect(current).toBeDefined();
    expect(current?.submenu?.options).toEqual([
      expect.objectContaining({ key: '__local__', active: false, disabled: true }),
    ]);
    await act(async () => oldSelect?.('local-cpu'));
    expect(setSessionComputeProviderMock).toHaveBeenCalledTimes(1);
  });

  it('does not expose an old compute failure after switching conversations', async () => {
    const pendingCompute = createDeferred<never>();
    setRendererAccountOwner('compute-owner');
    isMobileMock.current = true;
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    loadAssistantsMock.mockResolvedValue([]);
    loadComputeProvidersMock.mockResolvedValue([{ name: 'local-cpu', displayName: 'Local CPU', checked: true }]);
    loadSessionComputeProvidersMock.mockResolvedValue([]);
    setSessionComputeProviderMock.mockReturnValueOnce(pendingCompute.promise);
    const view = await render(
      <AcpSendBox
        conversation_id='conv-1'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-1', 'inc-1', 'root-1')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    await waitFor(() =>
      expect(mobileActionSheetEntries.current.find((entry) => entry.key === 'session-compute')).toBeDefined()
    );

    mobileActionSheetEntries.current.find((entry) => entry.key === 'session-compute')?.submenu?.onSelect?.('local-cpu');
    await act(async () => Promise.resolve());
    view.rerender(
      <AcpSendBox
        conversation_id='conv-2'
        ownerId='compute-owner'
        initialConversation={computeRootProjection('conv-2', 'inc-2', 'root-2')}
        backend='xscience'
        messageState={makeMessageState()}
      />
    );
    messageErrorMock.mockClear();

    await act(async () => {
      pendingCompute.reject(new Error('old compute failed'));
      await pendingCompute.promise.catch(() => undefined);
    });

    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it('keeps ACP config options enabled on desktop without rendering a standalone thought selector', async () => {
    useAcpConfigOptionsMock.mockReturnValue({
      setStatus: { state: 'idle' },
      mode: null,
      model: null,
      thoughtLevel: {
        id: 'reasoning_effort',
        category: 'thought_level',
        currentValue: 'high',
        options: [{ value: 'high', label: 'High' }],
      },
      reload: vi.fn(),
      setConfigOption: vi.fn(),
    });

    await act(async () => {
      await render(
        <AcpSendBox
          conversation_id='conv-1'
          backend='codex'
          workspacePath='/tmp/workspace'
          messageState={makeMessageState()}
        />
      );
      await Promise.resolve();
    });

    expect(useAcpConfigOptionsMock).toHaveBeenCalledWith(expect.objectContaining({ enabled: true }));
    expect(screen.queryByTestId('mock-thought-selector')).not.toBeInTheDocument();
  });

  it('applies runtime thought level from the mobile action sheet without persisting a global preference', async () => {
    isMobileMock.current = true;
    const setConfigOption = vi.fn().mockResolvedValue([]);
    useAcpConfigOptionsMock.mockReturnValue({
      mode: null,
      model: null,
      thoughtLevel: {
        id: 'reasoning_effort',
        category: 'thought_level',
        currentValue: 'medium',
        options: [
          { value: 'medium', label: 'Medium' },
          { value: 'high', label: 'High' },
        ],
      },
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
      isLoading: false,
      configOptions: [],
    });

    await render(
      <AcpSendBox
        conversation_id='conv-1'
        backend='codex'
        workspacePath='/tmp/workspace'
        messageState={makeMessageState()}
      />
    );

    await act(async () => {
      mobileActionSheetEntries.current.find((entry) => entry.key === 'thought-level')?.submenu?.onSelect?.('high');
    });

    // This branch dropped global-preference persistence: only the runtime
    // config option is set; nothing is saved to a global agent preference.
    await waitFor(() => {
      expect(setConfigOption).toHaveBeenCalledWith('reasoning_effort', 'high');
    });
  });

  it('does not apply runtime thought level when observed confirmation fails', async () => {
    isMobileMock.current = true;
    const setConfigOption = vi.fn().mockRejectedValue(new Error('command_ack'));
    useAcpConfigOptionsMock.mockReturnValue({
      mode: null,
      model: null,
      thoughtLevel: {
        id: 'reasoning_effort',
        category: 'thought_level',
        currentValue: 'medium',
        options: [{ value: 'high', label: 'High' }],
      },
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
      isLoading: false,
      configOptions: [],
    });

    await render(
      <AcpSendBox
        conversation_id='conv-1'
        backend='codex'
        workspacePath='/tmp/workspace'
        messageState={makeMessageState()}
      />
    );

    await act(async () => {
      mobileActionSheetEntries.current.find((entry) => entry.key === 'thought-level')?.submenu?.onSelect?.('high');
    });

    await waitFor(() => {
      expect(setConfigOption).toHaveBeenCalledWith('reasoning_effort', 'high');
    });
  });

  it('does not expose a completed thought-level change after switching conversations', async () => {
    const pendingChange = createDeferred<unknown[]>();
    const setConfigOption = vi.fn().mockReturnValueOnce(pendingChange.promise);
    isMobileMock.current = true;
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    useAcpConfigOptionsMock.mockReturnValue({
      mode: null,
      model: null,
      thoughtLevel: {
        id: 'reasoning_effort',
        category: 'thought_level',
        currentValue: 'medium',
        options: [{ value: 'high', label: 'High' }],
      },
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
    });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    mobileActionSheetEntries.current.find((entry) => entry.key === 'thought-level')?.submenu?.onSelect?.('high');
    await act(async () => Promise.resolve());
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    messageSuccessMock.mockClear();

    await act(async () => {
      pendingChange.resolve([]);
      await pendingChange.promise;
    });

    expect(messageSuccessMock).not.toHaveBeenCalled();
  });

  it('does not expose a failed thought-level change after switching conversations', async () => {
    const pendingChange = createDeferred<never>();
    const setConfigOption = vi.fn().mockReturnValueOnce(pendingChange.promise);
    isMobileMock.current = true;
    loadFramePlanMock.mockImplementation(() => new Promise(() => {}));
    useAcpConfigOptionsMock.mockReturnValue({
      mode: null,
      model: null,
      thoughtLevel: {
        id: 'reasoning_effort',
        category: 'thought_level',
        currentValue: 'medium',
        options: [{ value: 'high', label: 'High' }],
      },
      setStatus: { state: 'idle' },
      setConfigOption,
      reload: vi.fn(),
    });
    const view = await render(
      <AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />
    );

    mobileActionSheetEntries.current.find((entry) => entry.key === 'thought-level')?.submenu?.onSelect?.('high');
    await act(async () => Promise.resolve());
    view.rerender(<AcpSendBox conversation_id='conv-2' backend='xscience' messageState={makeMessageState()} />);
    messageErrorMock.mockClear();

    await act(async () => {
      pendingChange.reject(new Error('old thought level failed'));
      await pendingChange.promise.catch(() => undefined);
    });

    expect(messageErrorMock).not.toHaveBeenCalled();
  });

  it('keeps plus-menu context access while typing without replacing the draft or submitting a task', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'change' })));
    expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();
    const openContext = fileAttachProps.current?.onViewContextWindow;
    expect(openContext).toBeTypeOf('function');
    const sends = sendMessageInvokeMock.mock.calls.length;
    await act(async () => (openContext as (opener: HTMLButtonElement | null) => void)(null));
    expect(screen.getAllByTestId('context-window-inspector')).toHaveLength(1);
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('hello');
    expect(sendMessageInvokeMock).toHaveBeenCalledTimes(sends);
    fireEvent.click(screen.getByTestId('context-inspector-close'));
    expect(screen.queryByTestId('context-window-inspector')).not.toBeInTheDocument();
    expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();
    await act(async () => (openContext as (opener: HTMLButtonElement | null) => void)(null));
    expect(screen.getAllByTestId('context-window-inspector')).toHaveLength(1);
  });

  it('swaps the context-usage ring for the optimize-prompt action while typing', async () => {
    runtimeViewMock.isProcessing = false;
    runtimeViewMock.canSendMessage = true;
    runtimeViewMock.state = 'idle';
    runtimeViewMock.view.taskStatus = 'finished';
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(screen.getByTestId('x-science-context-usage-trigger')).toBeInTheDocument();
    expect(screen.queryByTestId('x-science-optimize-prompt-trigger')).not.toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'change' }));
    });

    expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-optimize-prompt-trigger')).toBeInTheDocument();
  });

  it('keeps the ring while running and shows both icons when typing during a run', async () => {
    runtimeViewMock.isProcessing = true;
    runtimeViewMock.canSendMessage = false;
    runtimeViewMock.state = 'running';
    await render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={makeMessageState()} />);

    expect(screen.getByTestId('x-science-context-usage-trigger')).toBeInTheDocument();
    expect(screen.queryByTestId('x-science-optimize-prompt-trigger')).not.toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'change' }));
    });

    expect(screen.getByTestId('x-science-context-usage-trigger')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-optimize-prompt-trigger')).toBeInTheDocument();
  });
});
