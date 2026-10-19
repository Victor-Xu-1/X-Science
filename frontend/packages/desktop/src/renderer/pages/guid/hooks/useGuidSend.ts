/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import { uuid } from '@/common/utils';
import type { IMcpServer } from '@/common/config/storage';
import { toSessionMcpServer } from '@/renderer/hooks/mcp/catalog';
import { emitter } from '@/renderer/utils/emitter';
import { updateWorkspaceTime } from '@/renderer/utils/workspace/workspaceHistory';
import { Message } from '@arco-design/web-react';
import { useCallback, useRef, useSyncExternalStore } from 'react';
import { type TFunction } from 'i18next';
import type { NavigateFunction } from 'react-router';
import { useSWRConfig } from 'swr';
import { prefetchConversationRoute } from '@/renderer/pages/conversation/conversationRoute';
import { rememberConversationRouteSnapshot } from '@/renderer/pages/conversation/utils/conversationCache';
import { getConversationCreateErrorMessage } from '@/renderer/pages/conversation/utils/conversationCreateError';
import type { AcpModelInfo } from '../types';
import {
  toXScienceMessageSessionOptions,
  type XScienceSessionOptions,
} from '@/renderer/services/xScienceSessionOptions';
import type { XScienceSessionDefaults } from '@/renderer/services/xScienceSessionDefaults';
import { buildXScienceConversationTitle } from '@/renderer/services/xScienceConversationTitle';
import { loadXScienceProjects } from '@/renderer/services/xScienceGateway';
import {
  toOnboardingAttachedArtifactReference,
  uploadXScienceProjectAttachment,
  type OnboardingArtifactCacheEntry,
  type OnboardingPendingUpload,
  type OnboardingUploadedArtifact,
} from '@/renderer/services/onboardingService';
import type { GuidLocalFile } from './useGuidInput';
import { suggestPdfReading, type PdfReadingSourcePosition } from '@/renderer/services/runtime/pdfReadingHandoff';
import { useComposerLifetime } from '@/renderer/hooks/chat/useComposerLifetime';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  GuidDelegationPreparationError,
  useGuidDelegationHandoff,
  type GuidSpecialistIntent,
} from './useGuidDelegationHandoff';
import { SpecialistPreparationRequired } from '@/renderer/services/runtime/sessionSpecialistTypes';
import {
  EMPTY_SPECIALIST_VIEW,
  type SessionSpecialistView,
} from '@/renderer/services/runtime/sessionSpecialistController';
import type { DelegationPolicy } from '@/renderer/services/runtime/sessionDelegationPolicy';
import {
  buildComposerCapabilityPayload,
  normalizeComposerContextItems,
  type ComposerContextItem,
  type ComposerArtifactContext,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';

export type GuidSendDeps = {
  // Input state
  input: string;
  setInput: React.Dispatch<React.SetStateAction<string>>;
  files: string[];
  setFiles: React.Dispatch<React.SetStateAction<string[]>>;
  localFiles: GuidLocalFile[];
  setLocalFiles: React.Dispatch<React.SetStateAction<GuidLocalFile[]>>;
  contextItems: ComposerContextItem[];
  pdfReadingEnabled?: boolean;
  pdfReadingPosition?: PdfReadingSourcePosition;
  setContextItems: React.Dispatch<React.SetStateAction<ComposerContextItem[]>>;
  dir: string;
  setDir: React.Dispatch<React.SetStateAction<string>>;
  setLoading: React.Dispatch<React.SetStateAction<boolean>>;
  loading: boolean;

  // Assistant state
  selectedAssistantId: string | null;
  selectedAssistantBackend: string;
  selectedMode: string;
  selectedAcpModel: string | null;
  selectedThoughtLevelValue?: string;
  currentAcpCachedModelInfo: AcpModelInfo | null;

  guidDisabledBuiltinSkills: string[] | undefined;
  guidEnabledSkills: string[] | undefined;
  assistantDefaultSkillIds?: string[];
  assistantDefaultDisabledBuiltinSkillIds?: string[];
  assistantDefaultSkillMode?: 'auto' | 'fixed';
  availableMcpServers: IMcpServer[];
  selectedMcpServerIds: string[] | undefined;
  assistantDefaultMcpIds?: string[];
  sessionOptions: XScienceSessionOptions;
  delegationPolicyOverride?: DelegationPolicy;
  specialistIntent?: GuidSpecialistIntent;
  draftGeneration?: number;
  sessionDefaults: XScienceSessionDefaults;
  sessionComputeProviders: string[];
  planModeRef?: { current: boolean };

  // Mention state reset
  setMentionOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setMentionQuery: React.Dispatch<React.SetStateAction<string | null>>;
  setMentionSelectorOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setMentionActiveIndex: React.Dispatch<React.SetStateAction<number>>;

  // Navigation
  ownerId: string;
  navigate: NavigateFunction;
  t: TFunction;
  localeKey: string;
};

export type GuidSendResult = {
  specialistView: SessionSpecialistView;
  retrySpecialist: () => Promise<void>;
  refreshSpecialist: () => Promise<void>;
  handleSend: () => Promise<boolean>;
  sendMessageHandler: () => Promise<boolean>;
  isButtonDisabled: boolean;
};

type XScienceProjectWorkspace = {
  workspace: string;
  projectId: string;
  projectName?: string;
  artifactCount?: number;
  conversationCount?: number;
};

const parseNonNegativeCount = (value: string | null): number | undefined => {
  if (!value) return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : undefined;
};

export function parseXScienceProjectWorkspace(workspace: string): XScienceProjectWorkspace | null {
  if (!workspace.startsWith('x-science://project/')) {
    return null;
  }

  try {
    const url = new URL(workspace);
    if (url.protocol !== 'x-science:' || url.hostname !== 'project') {
      return null;
    }
    const projectId = decodeURIComponent(url.pathname.replace(/^\/+/, ''));
    if (!projectId) {
      return null;
    }
    const projectName = url.searchParams.get('name') || undefined;
    return {
      workspace,
      projectId,
      projectName,
      artifactCount: parseNonNegativeCount(url.searchParams.get('artifacts')),
      conversationCount: parseNonNegativeCount(url.searchParams.get('conversations')),
    };
  } catch {
    return null;
  }
}

function projectWorkspaceFromComposerArtifacts(
  contextItems: readonly ComposerContextItem[]
): XScienceProjectWorkspace | null {
  const projectIds = new Set(
    normalizeComposerContextItems(contextItems)
      .filter((item) => item.kind === 'artifact' && item.projectId)
      .map((item) => (item.kind === 'artifact' ? item.projectId! : ''))
  );
  if (projectIds.size > 1) {
    throw new Error('Selected project artifacts must belong to one X-Science project');
  }
  const projectId = projectIds.values().next().value as string | undefined;
  return projectId
    ? {
        workspace: `x-science://project/${encodeURIComponent(projectId)}`,
        projectId,
      }
    : null;
}

function resolveGuidProjectAuthority(
  selectedProject: XScienceProjectWorkspace | null,
  artifactProject: XScienceProjectWorkspace | null,
  hasCustomWorkspace: boolean
): XScienceProjectWorkspace | null {
  if (!artifactProject) return selectedProject;
  if (hasCustomWorkspace) {
    throw new Error('Project artifacts cannot be combined with a custom local workspace');
  }
  if (selectedProject && selectedProject.projectId !== artifactProject.projectId) {
    throw new Error('Selected project artifacts conflict with the active X-Science project');
  }
  return selectedProject ?? artifactProject;
}

async function resolveGuidAttachmentProject(
  selectedProject: XScienceProjectWorkspace | null,
  hasCustomWorkspace: boolean
): Promise<XScienceProjectWorkspace> {
  if (selectedProject) return selectedProject;
  if (hasCustomWorkspace) {
    throw new Error('Local attachments require a X-Science project workspace');
  }

  const projects = await loadXScienceProjects();
  const personalWorkspace = projects.find((project) => project.name.trim().toLowerCase() === 'personal workspace');
  const fallbackProject = personalWorkspace ?? projects[0];
  if (!fallbackProject) {
    throw new Error('No X-Science project is available for local attachments');
  }

  return {
    workspace: `x-science://project/${encodeURIComponent(fallbackProject.projectId)}?name=${encodeURIComponent(fallbackProject.name)}`,
    projectId: fallbackProject.projectId,
    projectName: fallbackProject.name,
    artifactCount: fallbackProject.artifactCount,
    conversationCount: fallbackProject.conversationCount,
  };
}

/**
 * Hook that manages the send logic for X-Science assistant conversations.
 */
function normalizeXScienceEffort(value?: string): XScienceSessionDefaults['effort'] {
  return value === 'low' || value === 'medium' || value === 'high' ? value : undefined;
}

export const useGuidSend = (deps: GuidSendDeps): GuidSendResult => {
  const { mutate: swrMutate } = useSWRConfig();
  const {
    input,
    setInput,
    files,
    setFiles,
    localFiles,
    setLocalFiles,
    contextItems,
    setContextItems,
    dir,
    setDir,
    setLoading,
    loading,
    selectedAssistantId,
    selectedAssistantBackend,
    selectedMode,
    selectedAcpModel,
    selectedThoughtLevelValue,
    currentAcpCachedModelInfo,
    guidDisabledBuiltinSkills,
    guidEnabledSkills,
    assistantDefaultSkillIds,
    assistantDefaultDisabledBuiltinSkillIds,
    assistantDefaultSkillMode,
    availableMcpServers,
    selectedMcpServerIds,
    assistantDefaultMcpIds,
    sessionOptions,
    sessionDefaults,
    sessionComputeProviders,
    planModeRef,
    setMentionOpen,
    setMentionQuery,
    setMentionSelectorOpen,
    setMentionActiveIndex,
    ownerId,
    navigate,
    t,
    localeKey,
  } = deps;
  const sendingRef = useRef<{ ownerKey: string } | null>(null);
  const sendOwnerKey = JSON.stringify([getRendererAccountScopeToken(), ownerId, deps.draftGeneration]);
  const captureDraftLifetime = useComposerLifetime(ownerId, deps.draftGeneration);
  const creationLifetime = JSON.stringify([
    deps.draftGeneration,
    dir,
    selectedAssistantId,
    selectedAssistantBackend,
    selectedMode,
    selectedAcpModel,
    currentAcpCachedModelInfo?.current_model_id,
    selectedThoughtLevelValue,
    selectedMcpServerIds,
    availableMcpServers.filter((server) => (selectedMcpServerIds ?? assistantDefaultMcpIds ?? []).includes(server.id)),
    guidEnabledSkills,
    guidDisabledBuiltinSkills,
    assistantDefaultSkillIds,
    assistantDefaultDisabledBuiltinSkillIds,
    assistantDefaultMcpIds,
    assistantDefaultSkillMode,
    localeKey,
    input,
    files,
    localFiles.map(({ id }) => id),
    contextItems,
    sessionOptions,
    sessionDefaults,
    sessionComputeProviders,
    deps.pdfReadingEnabled,
    deps.pdfReadingPosition,
    deps.delegationPolicyOverride,
    deps.specialistIntent,
  ]);
  const captureLifetime = useComposerLifetime(ownerId, creationLifetime);
  const delegationHandoff = useGuidDelegationHandoff(ownerId, deps.draftGeneration);
  const specialistView = useSyncExternalStore(
    delegationHandoff.subscribe,
    delegationHandoff.getSpecialistSnapshot,
    () => EMPTY_SPECIALIST_VIEW
  );
  const uploadedAttachmentCacheRef = useRef(new WeakMap<File, OnboardingArtifactCacheEntry>());
  const pendingAttachmentUploadsRef = useRef(new WeakMap<File, OnboardingPendingUpload>());

  const handleSend = useCallback(async () => {
    const isCurrent = captureLifetime();
    const ensureCurrent = () => {
      if (!isCurrent()) throw new Error('Conversation draft owner changed');
    };
    if (!selectedAssistantId) {
      Message.warning(t('conversation.welcome.assistantUnavailable'));
      return false;
    }

    if (selectedAssistantBackend !== 'xscience') {
      Message.error(t('conversation.welcome.xScienceOnly'));
      return false;
    }

    const messageInput = input.trim() || t('conversation.sendbox.contextOnlyPrompt');
    const capabilityPayload = buildComposerCapabilityPayload(contextItems);
    const selectedReading = contextItems.filter(
      (item): item is ComposerArtifactContext => item.kind === 'artifact' && item.readingSelected === true
    );
    const wantsReading =
      deps.pdfReadingEnabled === true &&
      (selectedReading.length > 0 ||
        (files.length === 0 &&
          suggestPdfReading([
            ...localFiles.map(({ file }) => ({ name: file.name, size: file.size, contentType: file.type })),
            ...contextItems
              .filter((item) => item.kind === 'artifact')
              .map((item) => ({ name: item.label, size: item.sizeBytes, contentType: item.contentType })),
          ])));

    const xScienceProjectWorkspace = parseXScienceProjectWorkspace(dir);
    const isCustomWorkspace = !!dir && !xScienceProjectWorkspace;
    const artifactProjectWorkspace = projectWorkspaceFromComposerArtifacts(contextItems);
    const selectedProjectWorkspace = resolveGuidProjectAuthority(
      xScienceProjectWorkspace,
      artifactProjectWorkspace,
      isCustomWorkspace
    );
    const attachmentProjectWorkspace =
      localFiles.length > 0 ? await resolveGuidAttachmentProject(selectedProjectWorkspace, isCustomWorkspace) : null;
    ensureCurrent();
    const conversationProjectWorkspace = attachmentProjectWorkspace ?? selectedProjectWorkspace;
    const finalWorkspace = conversationProjectWorkspace?.workspace ?? (dir || '');

    const assistantConversationId = selectedAssistantId;
    const conversationRouteReady = prefetchConversationRoute().catch((error: unknown) => {
      console.warn('[conversation-prefetch] New conversation route unavailable:', error);
    });
    const enabledSkillsToSend =
      guidEnabledSkills !== undefined
        ? guidEnabledSkills
        : assistantDefaultSkillMode === 'fixed'
          ? assistantDefaultSkillIds
          : undefined;
    const excludeBuiltinSkills =
      guidDisabledBuiltinSkills !== undefined
        ? guidDisabledBuiltinSkills
        : assistantDefaultSkillMode === 'fixed'
          ? assistantDefaultDisabledBuiltinSkillIds
          : undefined;
    const selectedAllMcpServerIds = selectedMcpServerIds ?? [];
    const selectedMcpServerIdSet = new Set(selectedAllMcpServerIds);
    const selectedUserMcpServerIds = availableMcpServers
      .filter((server) => selectedMcpServerIdSet.has(server.id) && server.builtin !== true)
      .map((server) => server.id);
    const selectedAllSessionMcpServers = availableMcpServers
      .filter((server) => selectedMcpServerIdSet.has(server.id))
      .map((server) => toSessionMcpServer(server));
    const selectedSessionMcpServers = availableMcpServers
      .filter((server) => selectedMcpServerIdSet.has(server.id) && server.builtin === true)
      .map((server) => toSessionMcpServer(server));
    const defaultSelectedMcpServerIds = assistantDefaultMcpIds;
    const defaultSelectedUserMcpServerIds = availableMcpServers
      .filter((server) => (defaultSelectedMcpServerIds ?? []).includes(server.id) && server.builtin !== true)
      .map((server) => server.id);
    const assistantOverrideMcpIds =
      selectedMcpServerIds !== undefined ? selectedAllMcpServerIds : defaultSelectedMcpServerIds;
    const selectedUserMcpServerIdsToSend =
      selectedMcpServerIds !== undefined ? selectedUserMcpServerIds : defaultSelectedUserMcpServerIds;
    const selectedSessionMcpServersToSend =
      selectedMcpServerIds !== undefined
        ? selectedAllSessionMcpServers
        : availableMcpServers
            .filter((server) => (defaultSelectedMcpServerIds ?? []).includes(server.id))
            .map((server) => toSessionMcpServer(server));

    const assistantOverrideModel = selectedAcpModel || currentAcpCachedModelInfo?.current_model_id || undefined;
    const assistantOverrides = {
      model: assistantOverrideModel,
      permission: selectedMode || undefined,
      thought_level: selectedThoughtLevelValue || undefined,
      ...(selectedMcpServerIds !== undefined || assistantDefaultMcpIds !== undefined
        ? { mcp_ids: assistantOverrideMcpIds ?? [] }
        : {}),
      ...(enabledSkillsToSend !== undefined ? { skill_ids: enabledSkillsToSend } : {}),
      ...(excludeBuiltinSkills !== undefined ? { disabled_builtin_skill_ids: excludeBuiltinSkills } : {}),
    };

    try {
      const uploadedArtifacts: OnboardingUploadedArtifact[] = [];
      for (const localFile of localFiles) {
        const projectId = attachmentProjectWorkspace!.projectId;
        const cached = uploadedAttachmentCacheRef.current.get(localFile.file);
        if (cached && cached.projectId !== projectId) {
          uploadedAttachmentCacheRef.current.delete(localFile.file);
        }
        const pending = pendingAttachmentUploadsRef.current.get(localFile.file);
        if (pending && pending.projectId !== projectId) {
          pendingAttachmentUploadsRef.current.delete(localFile.file);
        }
        let artifact = cached?.projectId === projectId ? cached.artifact : null;
        // Keep uploads ordered so the first message has deterministic artifact
        // references and only one bounded upload is active at a time.
        if (!artifact) {
          // eslint-disable-next-line no-await-in-loop
          artifact = await uploadXScienceProjectAttachment(projectId, localFile.file, {
            pending: pending?.projectId === projectId ? pending : undefined,
            onInitialized: (next) => pendingAttachmentUploadsRef.current.set(localFile.file, next),
            onAbandoned: () => pendingAttachmentUploadsRef.current.delete(localFile.file),
          });
          ensureCurrent();
          uploadedAttachmentCacheRef.current.set(localFile.file, { projectId, artifact });
          pendingAttachmentUploadsRef.current.delete(localFile.file);
        }
        uploadedArtifacts.push(artifact);
      }

      const createInput = {
        name: buildXScienceConversationTitle(messageInput),
        assistant: {
          id: assistantConversationId,
          locale: localeKey,
          conversation_overrides: assistantOverrides,
        },
        extra: {
          workspace: finalWorkspace,
          ...(conversationProjectWorkspace
            ? {
                backend: 'xscience',
                project_id: conversationProjectWorkspace.projectId,
                ...(conversationProjectWorkspace.projectName
                  ? { project_name: conversationProjectWorkspace.projectName }
                  : {}),
                ...(conversationProjectWorkspace.artifactCount !== undefined
                  ? { artifact_count: conversationProjectWorkspace.artifactCount }
                  : {}),
                ...(conversationProjectWorkspace.conversationCount !== undefined
                  ? { conversation_count: conversationProjectWorkspace.conversationCount }
                  : {}),
              }
            : {}),
          custom_workspace: isCustomWorkspace,
          default_files: files,
          ...(selectedMcpServerIds !== undefined || assistantDefaultMcpIds !== undefined
            ? {
                selected_mcp_server_ids: selectedUserMcpServerIdsToSend,
                selected_session_mcp_servers:
                  selectedMcpServerIds !== undefined ? selectedSessionMcpServers : selectedSessionMcpServersToSend,
              }
            : {}),
        },
      };
      const conversation = await delegationHandoff.materialize(
        JSON.stringify({ assistant: createInput.assistant, extra: createInput.extra }),
        () => ipcBridge.conversation.create.invoke(createInput),
        deps.delegationPolicyOverride,
        isCurrent
      );
      ensureCurrent();
      if (!conversation || !conversation.id) {
        Message.error(t('conversation.createFailed'));
        return false;
      }
      const specialistAuthority = await delegationHandoff.prepareSpecialist(deps.specialistIntent, isCurrent);
      ensureCurrent();

      if (isCustomWorkspace) {
        updateWorkspaceTime(finalWorkspace);
      }

      const effort = normalizeXScienceEffort(selectedThoughtLevelValue) ?? sessionDefaults.effort;
      const combinedArtifactReferences = [
        ...capabilityPayload.artifactRefs,
        ...uploadedArtifacts.map(toOnboardingAttachedArtifactReference),
      ].filter(
        (reference, index, all) =>
          all.findIndex(
            (candidate) =>
              candidate.artifact_id === reference.artifact_id && candidate.version_id === reference.version_id
          ) === index
      );
      const initialMessage = {
        input: messageInput,
        // Persist the existing runtime idempotency identity with the handoff,
        // so an ambiguous response followed by reload cannot create a new turn.
        loading_id: uuid(),
        files: files.length > 0 ? files : undefined,
        artifact_refs: combinedArtifactReferences.length > 0 ? combinedArtifactReferences : undefined,
        ...(wantsReading
          ? {
              pdf_reading_sources:
                selectedReading.length > 0
                  ? selectedReading.map((item) => ({ artifact_id: item.artifactId, version_id: item.versionId }))
                  : combinedArtifactReferences.map(({ artifact_id, version_id }) => ({ artifact_id, version_id })),
              pdf_reading_owner_id: ownerId,
              ...(deps.pdfReadingPosition ? { pdf_reading_position: deps.pdfReadingPosition } : {}),
            }
          : {}),
        inject_skills: capabilityPayload.injectSkills.length > 0 ? capabilityPayload.injectSkills : undefined,
        inject_mcp_server_ids:
          capabilityPayload.injectMcpServerIds.length > 0 ? capabilityPayload.injectMcpServerIds : undefined,
        session_options: {
          ...toXScienceMessageSessionOptions(sessionOptions, {
            delegation: deps.delegationPolicyOverride !== 'deny',
            targetAgent: specialistAuthority.targetAgent ?? null,
            specialistBinding: specialistAuthority.reference,
            ...(planModeRef?.current ? { planMode: true } : {}),
          }),
          model: assistantOverrideModel,
          subagent_model: sessionDefaults.subagentModelId,
          effort,
        },
        compute_providers: sessionComputeProviders,
        // Send is the user's execution intent. The conversation's existing
        // initial-message controller submits it once when realtime is ready.
      };
      delegationHandoff.storeMessage(conversation.id, initialMessage, (value) =>
        sessionStorage.setItem(`acp_initial_message_${conversation.id}`, value)
      );

      // The create response is the authoritative first conversation snapshot.
      // Hand it to the route before navigation so the new chat paints directly
      // instead of showing a second welcome/loading surface while re-fetching it.
      rememberConversationRouteSnapshot(ownerId, conversation, 'detail');
      await conversationRouteReady;
      ensureCurrent();
      await navigate(`/conversation/${conversation.id}`);
      if (!isCurrent()) return true;
      delegationHandoff.dispose();

      // The new conversation is already authoritative once create returns.
      // Cache revalidation and sidebar refresh must not sit on the critical
      // click -> conversation paint path; run them after navigation instead.
      if (assistantConversationId) {
        void Promise.allSettled([
          swrMutate(`guid.assistant.detail.${assistantConversationId}.${localeKey}`),
          swrMutate('assistants.list'),
        ]);
      }
      emitter.emit('chat.history.refresh');
      if (conversationProjectWorkspace) emitter.emit('xscience.projects.refresh');
      uploadedAttachmentCacheRef.current = new WeakMap<File, OnboardingArtifactCacheEntry>();
      pendingAttachmentUploadsRef.current = new WeakMap<File, OnboardingPendingUpload>();
      return true;
    } catch (error: unknown) {
      console.error('Failed to create ACP conversation:', error);
      throw error;
    }
  }, [
    input,
    files,
    localFiles,
    contextItems,
    deps.pdfReadingEnabled,
    deps.pdfReadingPosition,
    captureLifetime,
    delegationHandoff,
    deps.delegationPolicyOverride,
    deps.specialistIntent,
    dir,
    selectedAssistantId,
    selectedAssistantBackend,
    selectedMode,
    selectedAcpModel,
    selectedThoughtLevelValue,
    currentAcpCachedModelInfo,
    guidDisabledBuiltinSkills,
    guidEnabledSkills,
    assistantDefaultSkillIds,
    assistantDefaultDisabledBuiltinSkillIds,
    assistantDefaultSkillMode,
    availableMcpServers,
    selectedMcpServerIds,
    assistantDefaultMcpIds,
    sessionOptions,
    sessionDefaults,
    sessionComputeProviders,
    planModeRef,
    ownerId,
    navigate,
    t,
    localeKey,
  ]);

  const sendMessageHandler = useCallback(async (): Promise<boolean> => {
    if (loading || sendingRef.current?.ownerKey === sendOwnerKey) return false;
    const isCurrent = captureLifetime();
    const isDraftCurrent = captureDraftLifetime();
    const request = { ownerKey: sendOwnerKey };
    sendingRef.current = request;
    setLoading(true);
    try {
      const sent = await handleSend();
      if (!sent) return false;
      if (!isCurrent()) return sent;
      setInput('');
      setMentionOpen(false);
      setMentionQuery(null);
      setMentionSelectorOpen(false);
      setMentionActiveIndex(0);
      setFiles([]);
      setLocalFiles([]);
      setContextItems([]);
      setDir('');
      return true;
    } catch (error) {
      if (!isCurrent()) return false;
      console.error('Failed to send message:', error);
      Message.error(
        error instanceof GuidDelegationPreparationError
          ? t('conversation.xscienceRuntime.sessionOptions.delegationState.prepareFailed')
          : error instanceof SpecialistPreparationRequired
            ? t('conversation.xscienceRuntime.sessionOptions.specialistState.prepareFailed')
            : getConversationCreateErrorMessage(error, t)
      );
      return false;
    } finally {
      if (sendingRef.current === request) sendingRef.current = null;
      if (isDraftCurrent()) setLoading(false);
    }
  }, [
    loading,
    handleSend,
    captureLifetime,
    captureDraftLifetime,
    sendOwnerKey,
    setLoading,
    setInput,
    setMentionOpen,
    setMentionQuery,
    setMentionSelectorOpen,
    setMentionActiveIndex,
    setFiles,
    setLocalFiles,
    setContextItems,
    setDir,
    t,
  ]);

  // Calculate button disabled state
  const hasMessageInput = Boolean(input.trim() || files.length > 0 || localFiles.length > 0 || contextItems.length > 0);
  const isButtonDisabled = loading || !hasMessageInput || !selectedAssistantId;

  return {
    specialistView,
    retrySpecialist: delegationHandoff.retrySpecialist,
    refreshSpecialist: delegationHandoff.refreshSpecialist,
    handleSend,
    sendMessageHandler,
    isButtonDisabled,
  };
};
