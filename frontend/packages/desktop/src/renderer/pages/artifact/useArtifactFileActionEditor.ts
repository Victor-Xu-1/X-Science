/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useRef, useState } from 'react';
import { copySynonBiomedArtifact, moveSynonBiomedArtifact } from '@/renderer/services/synonBiomedArtifacts';
import { exportSynonBiomedArtifactToCloud } from '@/renderer/services/synonBiomedWorkspaceSettings';
import type { SynonBiomedProjectArtifact } from '@/renderer/services/synonBiomedGateway';
import { useArtifactCloudExport } from './useArtifactCloudExport';
import { useArtifactRequestScope } from './useArtifactRequestScope';

export type ArtifactFileAction = 'copy' | 'move' | 'export';
export type ArtifactFileActionResult = { copiedArtifactId: string | null; folderId: string | null };
export const ARTIFACT_ROOT_FOLDER = '__project_root__';

export function useArtifactFileActionEditor(
  action: ArtifactFileAction,
  artifact: Pick<SynonBiomedProjectArtifact, 'artifactId' | 'filename' | 'folderId'>,
  copySuffix: string,
  onCompleted: (result: ArtifactFileActionResult) => void
) {
  const scope = useArtifactRequestScope(artifact.artifactId, '', action);
  const completedCallback = useRef(onCompleted);
  useLayoutEffect(() => {
    completedCallback.current = onCompleted;
  }, [onCompleted]);
  const cloud = useArtifactCloudExport(action === 'export');
  const [filename, setFilename] = useState(() => makeCopyFilename(artifact.filename, copySuffix));
  const [folderId, setFolderId] = useState(
    action === 'move' ? (artifact.folderId ?? ARTIFACT_ROOT_FOLDER) : ARTIFACT_ROOT_FOLDER
  );
  const [objectKey, setObjectKey] = useState(artifact.filename);
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const [completed, setCompleted] = useState(false);
  const canSubmit =
    !pending &&
    !completed &&
    (action === 'copy' ? Boolean(filename.trim()) : action === 'move' || (cloud.ready && Boolean(objectKey.trim())));

  const submit = async () => {
    if (!canSubmit) return;
    const token = scope.begin();
    if (token === null) return;
    const targetFolderId = folderId === ARTIFACT_ROOT_FOLDER ? null : folderId;
    const result: ArtifactFileActionResult = { copiedArtifactId: null, folderId: targetFolderId };
    setPending(true);
    setFailed(false);
    try {
      if (action === 'copy') {
        const copy = await copySynonBiomedArtifact({
          artifactId: artifact.artifactId,
          newFilename: filename.trim(),
          targetFolderId,
        });
        result.copiedArtifactId = copy.artifactId;
      } else if (action === 'move') {
        await moveSynonBiomedArtifact({ artifactId: artifact.artifactId, folderId: targetFolderId });
      } else {
        await exportSynonBiomedArtifactToCloud(cloud.credentialId, {
          artifactId: artifact.artifactId,
          bucket: cloud.bucket,
          key: objectKey.trim(),
        });
      }
    } catch {
      if (scope.isCurrent(token)) setFailed(true);
      return;
    } finally {
      if (scope.isCurrent(token)) setPending(false);
      scope.finish(token);
    }
    if (!scope.isCurrent(token)) return;
    // A UI callback failure must never offer a second POST for an already committed file operation.
    setCompleted(true);
    completedCallback.current(result);
  };

  return {
    filename,
    setFilename,
    folderId,
    setFolderId,
    objectKey,
    setObjectKey,
    cloud,
    pending,
    failed,
    canSubmit,
    submit,
    close: scope.close,
  };
}

function makeCopyFilename(filename: string, suffix: string): string {
  const dotIndex = filename.lastIndexOf('.');
  return dotIndex <= 0
    ? `${filename}-${suffix}`
    : `${filename.slice(0, dotIndex)}-${suffix}${filename.slice(dotIndex)}`;
}
