/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import type { ISynonBiomedScientificFile } from '@/common/adapter/ipcBridge';
import type { ArtifactReferenceWire } from '@/common/adapter/messageStreamProtocol';
import {
  createConversationArtifactIndex,
  useConversationArtifactIndex,
  useResolveConversationArtifactWindow,
  type ConversationArtifactIndex,
} from '@/renderer/pages/conversation/Messages/artifacts';
import type { PreviewMetadata } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import {
  isSynonBiomedArtifactPreviewEditable,
  resolveSynonBiomedArtifactPreviewPlan,
  SYNON_BIOMED_TEXT_ACCEPT_HEADER,
} from '@/renderer/services/synonBiomedArtifactPreview';
import {
  createSynonBiomedCompanionArtifactUrls,
  getSynonBiomedArtifactImageFilename,
  getSynonBiomedArtifactReferenceId,
  parseSynonBiomedArtifactLink,
  type SynonBiomedArtifactContentReference,
  type SynonBiomedArtifactLink,
} from '@/renderer/services/synonBiomedArtifactReferences';
import { Message } from '@arco-design/web-react';
import { useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

type UseSynonBiomedArtifactLinkPreviewOptions = {
  conversationId?: string;
  workspace?: string;
  artifactReferences?: readonly ArtifactReferenceWire[];
};

type ArtifactPreviewMetadata = PreviewMetadata & {
  artifactId: string;
  versionId: string;
  contentUrl: string;
};

export function resolveSynonBiomedArtifactRootFrameId(
  file: Pick<ISynonBiomedScientificFile, 'root_frame_id' | 'frame_id'>,
  conversationId?: string
): string | undefined {
  return file.root_frame_id?.trim() || file.frame_id?.trim() || conversationId?.trim() || undefined;
}

export {
  getSynonBiomedArtifactImageFilename,
  getSynonBiomedArtifactReferenceId,
  getSynonBiomedRelativeArtifactFilename,
} from '@/renderer/services/synonBiomedArtifactReferences';

function availableArtifactReferences(
  references: readonly ArtifactReferenceWire[] | undefined
): Array<{ artifact_id: string; version_id: string }> {
  const result: Array<{ artifact_id: string; version_id: string }> = [];
  const seen = new Set<string>();
  for (const reference of references ?? []) {
    if (reference.availability && reference.availability !== 'available') continue;
    const artifactId = reference.artifact_id.trim();
    const versionId = reference.version_id.trim();
    if (!artifactId || !versionId) continue;
    const key = `${artifactId}\0${versionId}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push({ artifact_id: artifactId, version_id: versionId });
  }
  return result;
}

export function resolveSynonBiomedArtifactFile(
  index: ConversationArtifactIndex,
  rawReferenceId: string | null,
  rawFilename: string | null,
  references: readonly ArtifactReferenceWire[] | undefined,
  contentReference?: SynonBiomedArtifactContentReference,
  referencesResolved = false
): ISynonBiomedScientificFile | null {
  if (contentReference) {
    const file = contentReference.versionId
      ? index.byVersionId.get(contentReference.versionId)
      : index.byArtifactId.get(contentReference.artifactId);
    // A URL claiming one artifact cannot borrow another artifact's version or filename.
    return file?.artifact_id === contentReference.artifactId &&
      (!contentReference.versionId || file.version_id === contentReference.versionId)
      ? file
      : null;
  }
  const referenceId = rawReferenceId?.trim() ?? '';
  if (referenceId) {
    const exact = index.byVersionId.get(referenceId) ?? index.byArtifactId.get(referenceId);
    if (exact) return exact;
  }

  const filename = rawFilename?.trim().toLocaleLowerCase() ?? '';
  if (filename) {
    const unavailableNamedReference = (references ?? []).some((reference) => {
      const file = index.byVersionId.get(reference.version_id);
      const available = !reference.availability || reference.availability === 'available';
      if (available && file?.artifact_id === reference.artifact_id) return false;
      const referenceFilename = reference.filename || index.byArtifactId.get(reference.artifact_id)?.filename;
      return referenceFilename?.toLocaleLowerCase() === filename;
    });
    if (unavailableNamedReference) return null;
    const referencedFiles = availableArtifactReferences(references).map((reference) => {
      const file = index.byVersionId.get(reference.version_id);
      return file?.artifact_id === reference.artifact_id ? file : undefined;
    });
    // Resolve the message's exact versions before falling back to a current file
    // with the same name; a partially populated index must not change history.
    const hasUnresolvedReferences = referencedFiles.some((file) => !file);
    if (hasUnresolvedReferences && !referencesResolved) return null;
    const exactVersions = referencedFiles.filter((file): file is ISynonBiomedScientificFile =>
      Boolean(file && file.filename.toLocaleLowerCase() === filename)
    );
    if (exactVersions.length === 1) return exactVersions[0];
    if (exactVersions.length > 1 || hasUnresolvedReferences) return null;
    const byFilename = index.byFilename.get(filename);
    if (byFilename) return byFilename;
  }
  return null;
}

export function useSynonBiomedArtifactResolver({
  conversationId,
  workspace,
  artifactReferences,
}: UseSynonBiomedArtifactLinkPreviewOptions) {
  const { openPreview } = usePreviewContext();
  const { t } = useTranslation();
  const artifactIndex = useConversationArtifactIndex();
  const resolveWindowReferences = useResolveConversationArtifactWindow();
  const exactReferences = useMemo(() => availableArtifactReferences(artifactReferences), [artifactReferences]);
  const companionArtifactUrls = useMemo(
    () => createSynonBiomedCompanionArtifactUrls([...artifactIndex.byVersionId.values()]),
    [artifactIndex]
  );

  const resolveFile = useCallback(
    async (link: SynonBiomedArtifactLink): Promise<ISynonBiomedScientificFile | null> => {
      const referenceId = link.kind === 'reference' ? link.referenceId : null;
      const filename = link.kind === 'filename' ? link.filename : null;
      const contentReference = link.kind === 'content' ? link : undefined;
      const requestedReferences = contentReference?.versionId
        ? [{ artifact_id: contentReference.artifactId, version_id: contentReference.versionId }]
        : exactReferences;
      const cached = resolveSynonBiomedArtifactFile(
        artifactIndex,
        referenceId,
        filename,
        artifactReferences,
        contentReference
      );
      const exactReferenceIsAlreadyIncluded = exactReferences.some(
        (reference) => reference.version_id === referenceId || reference.artifact_id === referenceId
      );
      const versionIds = referenceId && !exactReferenceIsAlreadyIncluded ? [referenceId] : [];
      if (cached || !conversationId || (requestedReferences.length === 0 && versionIds.length === 0)) return cached;
      const collections = await resolveWindowReferences(requestedReferences, versionIds);
      const resolvedCollections =
        collections ??
        (await ipcBridge.conversation.listArtifacts.invoke({
          conversation_id: conversationId,
          references: requestedReferences,
          version_ids: versionIds,
        }));
      return resolveSynonBiomedArtifactFile(
        createConversationArtifactIndex(resolvedCollections),
        referenceId,
        filename,
        artifactReferences,
        contentReference,
        true
      );
    },
    [artifactIndex, artifactReferences, conversationId, exactReferences, resolveWindowReferences]
  );

  const resolveImage = useCallback(
    async (src: string): Promise<string | null> => {
      if (!workspace?.startsWith('synonbiomed://') || !conversationId) return null;
      const artifactReferenceId = getSynonBiomedArtifactReferenceId(src);
      if (artifactReferenceId) {
        return (await resolveFile({ kind: 'reference', referenceId: artifactReferenceId }))?.content_url ?? null;
      }
      const filename = getSynonBiomedArtifactImageFilename(src);
      if (!filename) return null;
      return (await resolveFile({ kind: 'filename', filename }))?.content_url ?? null;
    },
    [conversationId, resolveFile, workspace]
  );

  const resolveLinkHref = useCallback(
    async (href: string): Promise<string | null> => {
      if (!workspace?.startsWith('synonbiomed://') || !conversationId) return null;
      const link = parseSynonBiomedArtifactLink(href);
      return link ? ((await resolveFile(link))?.content_url ?? null) : null;
    },
    [conversationId, resolveFile, workspace]
  );

  const handleLink = useCallback(
    async (href: string): Promise<boolean> => {
      const link = parseSynonBiomedArtifactLink(href);
      if (!link) return false;
      if (!workspace?.startsWith('synonbiomed://') || !conversationId) {
        if (link.kind === 'filename') return false;
        Message.error(t('conversation.scientificFiles.loadFailed'));
        return true;
      }

      try {
        const file = await resolveFile(link);
        if (!file) {
          Message.error(t('conversation.scientificFiles.loadFailed'));
          return true;
        }

        const plan = resolveSynonBiomedArtifactPreviewPlan({
          filename: file.filename,
          contentType: file.content_type,
          previewKind: file.preview_kind,
        });
        let content = file.content_url;
        if (plan.fetchText) {
          const response = await fetch(file.content_url, {
            headers: { accept: SYNON_BIOMED_TEXT_ACCEPT_HEADER },
          });
          if (!response.ok) throw new Error(`Artifact request failed: ${response.status}`);
          content = await response.text();
        }
        const metadata: ArtifactPreviewMetadata = {
          title: file.filename,
          file_name: file.filename,
          artifactId: file.artifact_id,
          versionId: file.version_id,
          contentUrl: file.content_url,
          rootFrameId: resolveSynonBiomedArtifactRootFrameId(file, conversationId),
          workspace,
          language: plan.language,
          editable: isSynonBiomedArtifactPreviewEditable(plan),
          companionArtifactUrls,
        };
        openPreview(content, plan.type, metadata, { presentation: 'board' });
      } catch (error) {
        console.error('[useSynonBiomedArtifactLinkPreview] Failed to preview artifact link:', error);
        Message.error(t('conversation.scientificFiles.loadFailed'));
      }
      return true;
    },
    [companionArtifactUrls, conversationId, openPreview, resolveFile, t, workspace]
  );

  return { handleLink, resolveImage, resolveLinkHref };
}

export function useSynonBiomedArtifactLinkPreview(options: UseSynonBiomedArtifactLinkPreviewOptions) {
  return useSynonBiomedArtifactResolver(options).handleLink;
}
