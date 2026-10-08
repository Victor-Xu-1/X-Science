/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useRef, useState } from 'react';
import {
  loadSynonBiomedArtifactLineage,
  loadSynonBiomedArtifactVersions,
  type SynonBiomedArtifactLineage,
  type SynonBiomedArtifactVersion,
} from '@/renderer/services/synonBiomedArtifacts';
import {
  loadSynonBiomedProjectArtifacts,
  loadSynonBiomedProjectFolders,
  type SynonBiomedProjectArtifact,
  type SynonBiomedProjectFolder,
} from '@/renderer/services/synonBiomedGateway';

export type ArtifactReadState<T> = { status: 'loading' | 'ready' | 'failed'; value: T };
type MetadataValues = {
  versions: SynonBiomedArtifactVersion[];
  lineage: SynonBiomedArtifactLineage | null;
  folders: SynonBiomedProjectFolder[];
  resources: SynonBiomedProjectArtifact[];
};
export type ArtifactMetadataSection = keyof MetadataValues;
type ReadSnapshot = {
  owner: string;
  lineageVersion: string | null;
  sections: { [Key in ArtifactMetadataSection]: ArtifactReadState<MetadataValues[Key]> };
};
type ReadLoaders = { [Key in ArtifactMetadataSection]: () => Promise<MetadataValues[Key]> };
type ReadOwner = {
  active: boolean;
  owner: string;
  lineageVersion: string | null;
  revisions: Record<ArtifactMetadataSection, number>;
  pending: Set<ArtifactMetadataSection>;
};

function initialSnapshot(
  owner: string,
  artifact: SynonBiomedProjectArtifact | null,
  lineageVersion: string | null
): ReadSnapshot {
  const projectReady = Boolean(artifact && !artifact.projectId);
  return {
    owner,
    lineageVersion,
    sections: {
      versions: { status: 'loading', value: [] },
      lineage: { status: 'loading', value: null },
      folders: { status: projectReady ? 'ready' : 'loading', value: [] },
      resources: { status: projectReady ? 'ready' : 'loading', value: projectReady && artifact ? [artifact] : [] },
    },
  };
}

/** Independent reads cannot hide the primary file or relabel a failed read as empty. */
export function useArtifactMetadataReads(
  artifact: SynonBiomedProjectArtifact | null,
  revision: number,
  selectedVersionId?: string | null
) {
  const owner = artifact ? JSON.stringify([artifact.artifactId, artifact.versionId, artifact.projectId, revision]) : '';
  const lineageVersion = artifact ? (selectedVersionId ?? artifact.versionId) : null;
  const [snapshot, setSnapshot] = useState(() => initialSnapshot(owner, artifact, lineageVersion));
  const scope = useRef<ReadOwner | null>(null);
  const loaders = useRef<ReadLoaders | null>(null);

  const start = <Key extends ArtifactMetadataSection>(key: Key) => {
    const captured = scope.current;
    const currentLoaders = loaders.current;
    if (!captured?.active || captured.owner !== owner || !currentLoaders || captured.pending.has(key)) return;
    captured.pending.add(key);
    const requestRevision = ++captured.revisions[key];
    const isCurrent = () =>
      scope.current === captured && captured.active && captured.revisions[key] === requestRevision;
    const update = (
      next: (previous: ArtifactReadState<MetadataValues[Key]>) => ArtifactReadState<MetadataValues[Key]>
    ) => {
      if (!isCurrent()) return;
      setSnapshot((previous) =>
        previous.owner === captured.owner
          ? { ...previous, sections: { ...previous.sections, [key]: next(previous.sections[key]) } }
          : previous
      );
    };
    const settle = (
      next: (previous: ArtifactReadState<MetadataValues[Key]>) => ArtifactReadState<MetadataValues[Key]>
    ) => {
      if (!isCurrent()) return;
      captured.pending.delete(key);
      update(next);
    };
    update((previous) => ({ ...previous, status: 'loading' }));
    void Promise.resolve()
      .then(() => {
        if (!isCurrent()) throw new Error('obsolete_artifact_metadata_read');
        return currentLoaders[key]();
      })
      .then((value) => settle(() => ({ status: 'ready', value })))
      .catch(() => settle((previous) => ({ ...previous, status: 'failed' })));
  };

  useLayoutEffect(() => {
    const captured: ReadOwner = {
      active: Boolean(artifact),
      owner,
      lineageVersion,
      revisions: { versions: 0, lineage: 0, folders: 0, resources: 0 },
      pending: new Set(),
    };
    scope.current = captured;
    setSnapshot(initialSnapshot(owner, artifact, lineageVersion));
    if (artifact) {
      loaders.current = {
        versions: () => loadSynonBiomedArtifactVersions(artifact.artifactId),
        lineage: () =>
          loadSynonBiomedArtifactLineage(artifact.artifactId, {
            slim: true,
            ...(lineageVersion ? { versionId: lineageVersion } : {}),
          }),
        folders: () => (artifact.projectId ? loadSynonBiomedProjectFolders(artifact.projectId) : Promise.resolve([])),
        resources: () =>
          artifact.projectId ? loadSynonBiomedProjectArtifacts(artifact.projectId) : Promise.resolve([artifact]),
      };
      start('versions');
      start('lineage');
      if (artifact.projectId) {
        start('folders');
        start('resources');
      }
    } else loaders.current = null;
    return () => {
      captured.active = false;
    };
    // The immutable owner contains every loader argument plus explicit refresh.
    // Re-rendering from an independent section is not a reason to read its peers again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [owner]);

  useLayoutEffect(() => {
    const captured = scope.current;
    const currentLoaders = loaders.current;
    if (
      !artifact ||
      !captured?.active ||
      captured.owner !== owner ||
      !currentLoaders ||
      captured.lineageVersion === lineageVersion
    )
      return;
    captured.lineageVersion = lineageVersion;
    captured.revisions.lineage += 1;
    captured.pending.delete('lineage');
    loaders.current = {
      ...currentLoaders,
      lineage: () =>
        loadSynonBiomedArtifactLineage(artifact.artifactId, {
          slim: true,
          ...(lineageVersion ? { versionId: lineageVersion } : {}),
        }),
    };
    setSnapshot((previous) =>
      previous.owner === owner
        ? {
            ...previous,
            lineageVersion,
            sections: { ...previous.sections, lineage: { status: 'loading', value: null } },
          }
        : previous
    );
    start('lineage');
    // Only the displayed source version changed; healthy peer reads stay intact.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [owner, lineageVersion]);

  const current = snapshot.owner === owner ? snapshot : initialSnapshot(owner, artifact, lineageVersion);
  return {
    owner,
    lineageOwner: JSON.stringify([owner, lineageVersion]),
    ...current.sections,
    lineage:
      current.lineageVersion === lineageVersion
        ? current.sections.lineage
        : { status: 'loading' as const, value: null },
    retry: start,
  };
}
