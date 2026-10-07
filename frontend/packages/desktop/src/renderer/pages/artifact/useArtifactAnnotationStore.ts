/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  createSynonBiomedArtifactAnnotation,
  deleteSynonBiomedArtifactAnnotation,
  loadSynonBiomedArtifactAnnotations,
  updateSynonBiomedArtifactAnnotation,
  type CreateSynonBiomedArtifactAnnotationInput,
  type SynonBiomedArtifactAnnotation,
} from '@/renderer/services/synonBiomedAnnotations';

/** One version owns the list, checksum, requests and notifications. */
export function useArtifactAnnotationStore(
  artifactId: string,
  versionId: string,
  refreshToken: number,
  onChange?: (annotations: SynonBiomedArtifactAnnotation[]) => void
) {
  const [annotations, setAnnotations] = useState<SynonBiomedArtifactAnnotation[]>([]);
  const [checksum, setChecksum] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const items = useRef<SynonBiomedArtifactAnnotation[]>([]);
  const notify = useRef(onChange);
  const lifecycle = useRef(0);
  const revision = useRef(0);
  const owner = useRef<{ artifactId: string; versionId: string } | null>(null);
  const writing = useRef(false);
  const refreshAfterWrite = useRef(false);

  useLayoutEffect(() => {
    notify.current = onChange;
  }, [onChange]);
  const publish = useCallback((next: SynonBiomedArtifactAnnotation[]) => {
    items.current = next;
    setAnnotations(next);
    notify.current?.(next);
  }, []);
  const ownsCurrentVersion = () => owner.current?.artifactId === artifactId && owner.current.versionId === versionId;

  const reload = useCallback(async () => {
    if (owner.current?.artifactId !== artifactId || owner.current.versionId !== versionId) return;
    if (writing.current) {
      refreshAfterWrite.current = true;
      return;
    }
    const scope = lifecycle.current;
    const request = ++revision.current;
    const isCurrent = () => scope === lifecycle.current && request === revision.current;
    setLoading(true);
    setFailed(false);
    try {
      const result = await loadSynonBiomedArtifactAnnotations(artifactId, versionId);
      if (!isCurrent()) return;
      publish(result.annotations);
      setChecksum(result.currentChecksum);
    } catch (error) {
      if (!isCurrent()) return;
      console.error('[ArtifactAnnotationsPanel] Failed to load annotations', error);
      publish([]);
      setChecksum(null);
      setFailed(true);
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [artifactId, versionId, publish]);

  useLayoutEffect(() => {
    lifecycle.current += 1;
    owner.current = { artifactId, versionId };
    writing.current = false;
    refreshAfterWrite.current = false;
    setBusy(false);
    setLoading(true);
    setFailed(false);
    setChecksum(null);
    publish([]);
    return () => {
      owner.current = null;
      lifecycle.current += 1;
      revision.current += 1;
    };
  }, [artifactId, versionId, publish]);

  useEffect(() => {
    void reload();
  }, [reload, refreshToken]);

  const mutate = async <T>(operation: () => Promise<T>, commit: (result: T) => void): Promise<boolean> => {
    if (!ownsCurrentVersion() || loading || writing.current) return false;
    const scope = lifecycle.current;
    writing.current = true;
    setBusy(true);
    try {
      const result = await operation();
      if (scope !== lifecycle.current) return false;
      commit(result);
      return true;
    } catch (error) {
      if (scope !== lifecycle.current) return false;
      throw error;
    } finally {
      if (scope === lifecycle.current) {
        writing.current = false;
        setBusy(false);
        if (refreshAfterWrite.current) {
          refreshAfterWrite.current = false;
          void reload();
        }
      }
    }
  };

  const create = (input: CreateSynonBiomedArtifactAnnotationInput) =>
    mutate(
      () => createSynonBiomedArtifactAnnotation(artifactId, versionId, input),
      (created) => {
        publish([...items.current, created]);
        setChecksum((current) => created.contentChecksum ?? current);
      }
    );
  const update = (id: string, text: string) =>
    mutate(
      () => updateSynonBiomedArtifactAnnotation(id, { text }),
      (updated) => publish(items.current.map((item) => (item.id === updated.id ? updated : item)))
    );
  const remove = (id: string) =>
    mutate(
      () => deleteSynonBiomedArtifactAnnotation(id),
      () => publish(items.current.filter((item) => item.id !== id))
    );

  return { annotations, checksum, loading, failed, busy, reload, create, update, remove };
}
