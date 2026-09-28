/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { parseSynonBiomedArtifactLink } from '@/renderer/services/synonBiomedArtifactReferences';
import { requestSynonBiomedJson } from '@/renderer/services/synonBiomedHttp';

type SnapshotArtifact = { artifact_id: string; version_id: string };

/** Save a native render as a derived artifact, never overwrite its coordinates. */
export async function saveStructureSnapshot(
  contentUrl: string,
  filename: string,
  dataUri: string
): Promise<SnapshotArtifact> {
  const source = parseSynonBiomedArtifactLink(contentUrl);
  if (source?.kind !== 'content' || !source.versionId) throw new Error('STRUCTURE_SNAPSHOT_VERSION_REQUIRED');
  const prefix = 'data:image/png;base64,';
  if (!dataUri.startsWith(prefix)) throw new Error('STRUCTURE_SNAPSHOT_PNG_REQUIRED');
  const bytes = Uint8Array.from(atob(dataUri.slice(prefix.length)), (character) => character.charCodeAt(0));
  if (![137, 80, 78, 71, 13, 10, 26, 10].every((value, index) => bytes[index] === value)) {
    throw new Error('STRUCTURE_SNAPSHOT_PNG_REQUIRED');
  }
  const name = `${filename.replace(/\.[^.]+$/, '') || 'structure'}-snapshot.png`;
  const body = new FormData();
  body.append('file', new Blob([bytes], { type: 'image/png' }), name);
  const query = new URLSearchParams({
    content_type: 'image/png',
    parent_version_id: source.versionId,
    branch_as_filename: name,
  });
  const result = await requestSynonBiomedJson<SnapshotArtifact>(
    `/api/artifacts/${encodeURIComponent(source.artifactId)}/versions/binary?${query}`,
    { method: 'POST', body },
    { timeoutMs: 60000 }
  );
  if (!result.artifact_id || !result.version_id || result.artifact_id === source.artifactId) {
    throw new Error('STRUCTURE_SNAPSHOT_RECEIPT_INVALID');
  }
  return result;
}
