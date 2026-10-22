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

type ComposerSelectionScope = { key: string; active: boolean };
type ProjectFilesSelection = { scope: ComposerSelectionScope; account: string; key: string };

export function useAcpComposerContextSelection(
  projectId: string | undefined,
  contextItems: ComposerContextItem[],
  setContextItems: Dispatch<SetStateAction<ComposerContextItem[]>>,
  ownerScope = ''
) {
  const [projectFilesSelection, setProjectFilesSelection] = useState<ProjectFilesSelection | null>(null);
  const projectFilesSelectionRef = useRef<ProjectFilesSelection | null>(null);
  const projectFilesSequenceRef = useRef(0);
  const uploadedAttachmentsRef = useRef(new WeakMap<File, OnboardingArtifactCacheEntry>());
  const pendingUploadsRef = useRef(new WeakMap<File, OnboardingPendingUpload>());
  const account = getRendererAccountScopeToken();
  const currentScope = `${ownerScope}:${projectId ?? ''}:${account}`;
  const scopeRef = useRef<ComposerSelectionScope>({ key: currentScope, active: true });
  if (scopeRef.current.key !== currentScope) {
    scopeRef.current = { key: currentScope, active: true };
    projectFilesSelectionRef.current = null;
    uploadedAttachmentsRef.current = new WeakMap();
    pendingUploadsRef.current = new WeakMap();
  }
  const scope = scopeRef.current;
  useEffect(() => {
    // The render already retired a different owner. Effect replay only changes
    // this token's lifetime, never installs another owner after that render.
    scope.active = true;
    return () => {
      scope.active = false;
    };
  }, [scope]);
  const canSelectProjectFiles = Boolean(projectId?.trim());
  const projectFilesOpen = Boolean(
    projectFilesSelection &&
    projectFilesSelectionRef.current === projectFilesSelection &&
    projectFilesSelection.scope === scope &&
    scope.active &&
    projectFilesSelection.account === account
  );
  const projectFilesSelectorKey = projectFilesOpen ? projectFilesSelection!.key : currentScope;
  const openProjectFiles = useCallback(() => {
    if (scopeRef.current !== scope || !scope.active || getRendererAccountScopeToken() !== account) return;
    const selection = { scope, account, key: `${currentScope}:${++projectFilesSequenceRef.current}` };
    projectFilesSelectionRef.current = selection;
    setProjectFilesSelection(selection);
  }, [scope, account, currentScope]);
  const setProjectFilesOpen = useCallback(
    (next: SetStateAction<boolean>) => {
      if (scopeRef.current !== scope || !scope.active || getRendererAccountScopeToken() !== account) return;
      const visible = projectFilesSelectionRef.current?.scope === scope;
      if (typeof next === 'function' ? next(visible) : next) {
        openProjectFiles();
        return;
      }
      projectFilesSelectionRef.current = null;
      setProjectFilesSelection(null);
    },
    [scope, account, openProjectFiles]
  );
  const handleProjectArtifactsSelected = useCallback(
    (selectedArtifacts: XScienceProjectArtifact[]) => {
      const selection = projectFilesSelection;
      if (
        !selection ||
        projectFilesSelectionRef.current !== selection ||
        scopeRef.current !== selection.scope ||
        !selection.scope.active ||
        getRendererAccountScopeToken() !== selection.account
      )
        return;
      projectFilesSelectionRef.current = null;
      setProjectFilesSelection(null);
      setContextItems((current) => {
        // A deferred state updater must not cross the owner/account boundary
        // after an otherwise valid confirmation has already been accepted.
        if (
          scopeRef.current !== selection.scope ||
          !selection.scope.active ||
          getRendererAccountScopeToken() !== selection.account
        )
          return current;
        return selectedArtifacts.reduce<ComposerContextItem[]>((items, artifact) => {
          const context = createComposerArtifactContext({
            artifactId: artifact.artifactId,
            versionId: artifact.versionId,
            projectId: artifact.projectId ?? projectId,
            label: artifact.filename,
            ...(artifact.contentType ? { contentType: artifact.contentType } : {}),
            sizeBytes: artifact.sizeBytes,
          });
          return context ? addComposerContextItem(items, context) : items;
        }, current);
      });
    },
    [projectId, setContextItems, projectFilesSelection]
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
        if (scopeRef.current !== scope || !scope.active || getRendererAccountScopeToken() !== account)
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
              if (scopeRef.current === scope && scope.active) pendingUploadsRef.current.set(file, next);
            },
            onAbandoned: () => {
              if (scopeRef.current === scope && scope.active) pendingUploadsRef.current.delete(file);
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
    projectFilesSelectorKey,
    selectedMcpServerIds,
    selectedSkillNames,
    setProjectFilesOpen,
  };
}
