import type { Dispatch, SetStateAction } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { IConversationMcpStatus } from '@/common/config/storage';
import {
  addComposerContextItem,
  createComposerArtifactContext,
  type ComposerContextItem,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';
import type { ArtifactComposerReference } from '@/renderer/components/chat/SendBox/composerReferenceModel';
import type { XScienceProjectArtifact } from '@/renderer/services/xScienceGateway';
import {
  uploadXScienceProjectAttachment,
  type OnboardingArtifactCacheEntry,
  type OnboardingPendingUpload,
} from '@/renderer/services/onboardingService';

export function useAcpComposerContextSelection(
  projectId: string | undefined,
  contextItems: ComposerContextItem[],
  setContextItems: Dispatch<SetStateAction<ComposerContextItem[]>>,
  ownerScope = ''
) {
  const [projectFilesOpen, setProjectFilesOpen] = useState(false);
  const uploadedAttachmentsRef = useRef(new WeakMap<File, OnboardingArtifactCacheEntry>());
  const pendingUploadsRef = useRef(new WeakMap<File, OnboardingPendingUpload>());
  const currentScope = `${ownerScope}:${projectId ?? ''}:${getRendererAccountScopeToken()}`;
  const scopeRef = useRef({ key: currentScope });
  if (scopeRef.current.key !== currentScope) {
    scopeRef.current = { key: currentScope };
    uploadedAttachmentsRef.current = new WeakMap();
    pendingUploadsRef.current = new WeakMap();
  }
  useEffect(() => {
    const token = { key: currentScope };
    scopeRef.current = token;
    setProjectFilesOpen(false);
    return () => {
      if (scopeRef.current === token) scopeRef.current = { key: '' };
    };
  }, [currentScope]);
  const canSelectProjectFiles = Boolean(projectId?.trim());
  const openProjectFiles = useCallback(() => setProjectFilesOpen(true), []);
  const handleProjectArtifactsSelected = useCallback(
    (selectedArtifacts: XScienceProjectArtifact[]) => {
      setContextItems((current) =>
        selectedArtifacts.reduce<ComposerContextItem[]>((items, artifact) => {
          const context = createComposerArtifactContext({
            artifactId: artifact.artifactId,
            versionId: artifact.versionId,
            projectId: artifact.projectId ?? projectId,
            label: artifact.filename,
            ...(artifact.contentType ? { contentType: artifact.contentType } : {}),
            sizeBytes: artifact.sizeBytes,
          });
          return context ? addComposerContextItem(items, context) : items;
        }, current)
      );
      setProjectFilesOpen(false);
    },
    [projectId, setContextItems]
  );
  const handleArtifactReferenceSelected = useCallback(
    (reference: ArtifactComposerReference) => {
      const context = createComposerArtifactContext({
        artifactId: reference.artifactId,
        versionId: reference.versionId,
        projectId,
        label: reference.label,
      });
      if (!context) return;
      setContextItems((current) => addComposerContextItem(current, context));
    },
    [projectId, setContextItems]
  );
  const handleLocalFilesSelected = useCallback(
    async (files: File[]) => {
      const scope = scopeRef.current;
      const account = getRendererAccountScopeToken();
      const ensureCurrent = () => {
        if (scopeRef.current !== scope || getRendererAccountScopeToken() !== account)
          throw new Error('Attachment composer changed');
      };
      const targetProjectId = projectId?.trim();
      if (!targetProjectId) throw new Error('A X-Science project is required for browser attachments');
      for (const file of files) {
        ensureCurrent();
        const cached = uploadedAttachmentsRef.current.get(file);
        if (cached && cached.projectId !== targetProjectId) uploadedAttachmentsRef.current.delete(file);
        const pending = pendingUploadsRef.current.get(file);
        if (pending && pending.projectId !== targetProjectId) pendingUploadsRef.current.delete(file);
        let artifact = cached?.projectId === targetProjectId ? cached.artifact : null;
        if (!artifact) {
          // Keep canonical uploads ordered so the draft and eventual message
          // preserve the user's file order while only one bounded stream is active.
          // eslint-disable-next-line no-await-in-loop
          artifact = await uploadXScienceProjectAttachment(targetProjectId, file, {
            pending: pending?.projectId === targetProjectId ? pending : undefined,
            onInitialized: (next) => {
              if (scopeRef.current === scope) pendingUploadsRef.current.set(file, next);
            },
            onAbandoned: () => {
              if (scopeRef.current === scope) pendingUploadsRef.current.delete(file);
            },
          });
          ensureCurrent();
          uploadedAttachmentsRef.current.set(file, { projectId: targetProjectId, artifact });
          pendingUploadsRef.current.delete(file);
        }
        const context = createComposerArtifactContext({
          artifactId: artifact.artifactId,
          versionId: artifact.versionId,
          projectId: targetProjectId,
          label: artifact.filename,
          sizeBytes: artifact.sizeBytes,
          contentType: file.type,
        });
        if (context) setContextItems((current) => addComposerContextItem(current, context));
      }
    },
    [projectId, setContextItems, currentScope]
  );
  const handleSkillSelected = useCallback(
    (name: string) => {
      setContextItems((current) => addComposerContextItem(current, { kind: 'skill', name, label: name }));
    },
    [setContextItems]
  );
  const handleMcpSelected = useCallback(
    (server: IConversationMcpStatus) => {
      if (server.status !== 'loaded') return;
      setContextItems((current) =>
        addComposerContextItem(current, { kind: 'mcp', serverId: server.id, label: server.name })
      );
    },
    [setContextItems]
  );
  const selectedSkillNames = useMemo(
    () => contextItems.filter((item) => item.kind === 'skill').map((item) => item.name),
    [contextItems]
  );
  const selectedMcpServerIds = useMemo(
    () => contextItems.filter((item) => item.kind === 'mcp').map((item) => item.serverId),
    [contextItems]
  );

  return {
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
  };
}
