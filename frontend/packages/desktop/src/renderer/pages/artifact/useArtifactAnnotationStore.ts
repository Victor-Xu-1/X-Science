/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  createXScienceAnnotationTarget,
  deleteXScienceArtifactAnnotation,
  loadXScienceAnnotationTarget,
  updateXScienceArtifactAnnotation,
  type CreateXScienceArtifactAnnotationInput,
  type XScienceArtifactAnnotation,
  type XScienceAnnotationTarget,
} from '@/renderer/services/xScienceAnnotations';

/** One version owns the list, checksum, requests and notifications. */
export function useArtifactAnnotationStore(
  artifactId: string,
  versionId: string,
  refreshToken: number,
  onChange?: (annotations: XScienceArtifactAnnotation[]) => void
) {
  return useAnnotationStore({ kind: 'artifact', artifactId, versionId }, refreshToken, onChange);
}

/** The original request lifecycle owns either canonical target, never two stores. */
export function useAnnotationStore(
  target: XScienceAnnotationTarget,
  refreshToken: number,
  onChange?: (annotations: XScienceArtifactAnnotation[]) => void
) {
  const kind = target.kind;
  const artifactId = target.kind === 'artifact' ? target.artifactId : '';
  const versionId = target.versionId;
  const [annotations, setAnnotations] = useState<XScienceArtifactAnnotation[]>([]);
  const [checksum, setChecksum] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const items = useRef<XScienceArtifactAnnotation[]>([]);
  const notify = useRef(onChange);
  const lifecycle = useRef(0);
  const revision = useRef(0);
  const owner = useRef<{ artifactId: string; versionId: string; kind: string } | null>(null);
  const writing = useRef(false);
  const refreshAfterWrite = useRef(false);

  useLayoutEffect(() => {
    notify.current = onChange;
  }, [onChange]);
  const publish = useCallback((next: XScienceArtifactAnnotation[]) => {
    items.current = next;
    setAnnotations(next);
    notify.current?.(next);
  }, []);
  const ownsCurrentVersion = () =>
    owner.current?.kind === kind && owner.current.artifactId === artifactId && owner.current.versionId === versionId;

  const reload = useCallback(async () => {
    if (
      owner.current?.kind !== kind ||
      owner.current.artifactId !== artifactId ||
      owner.current.versionId !== versionId
    )
      return;
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
      const result = await loadXScienceAnnotationTarget(
        kind === 'artifact' ? { kind, artifactId, versionId } : { kind, versionId }
      );
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
  }, [kind, artifactId, versionId, publish]);

  useLayoutEffect(() => {
    lifecycle.current += 1;
    owner.current = { kind, artifactId, versionId };
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
  }, [kind, artifactId, versionId, publish]);

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

  const create = (input: CreateXScienceArtifactAnnotationInput) =>
    mutate(
      () =>
        createXScienceAnnotationTarget(
          kind === 'artifact' ? { kind, artifactId, versionId } : { kind, versionId },
          input,
          checksum
        ),
      (created) => {
        publish([...items.current, created]);
        setChecksum((current) => created.contentChecksum ?? current);
      }
    );
  const update = (id: string, text: string) =>
    mutate(
      () => updateXScienceArtifactAnnotation(id, { text }),
      (updated) => publish(items.current.map((item) => (item.id === updated.id ? updated : item)))
    );
  const remove = (id: string) =>
    mutate(
      () => deleteXScienceArtifactAnnotation(id),
      () => publish(items.current.filter((item) => item.id !== id))
    );

  return { annotations, checksum, loading, failed, busy, reload, create, update, remove };
}
