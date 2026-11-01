/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import { isAnnotationNoteSaveable } from '@/renderer/utils/annotationNote';
import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import type { XScienceArtifactAnnotation } from '@/renderer/services/xScienceAnnotations';
import type { AnnotationCreateSource } from '@/renderer/services/runtime/annotationCreateIntent';
import { useAnnotationCreateIntent } from '@/renderer/hooks/useAnnotationCreateIntent';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import {
  annotationInputFromCanvasSelection,
  artifactCanvasSelectionIdentity,
  type XScienceArtifactCanvasSelection,
} from './artifactCanvasSelection';
import { useArtifactRequestScope } from './useArtifactRequestScope';

export function useArtifactSelectionEditor(
  artifactId: string,
  versionId: string,
  selection: XScienceArtifactCanvasSelection,
  onCreated: (annotation: XScienceArtifactAnnotation) => void | Promise<void>,
  sourceHint?: AnnotationCreateSource | null,
  onRecovered?: (annotation: XScienceArtifactAnnotation) => void | Promise<void>,
  onMissing?: () => void
) {
  const creator = useAnnotationCreateIntent({ kind: 'artifact', artifactId, versionId });
  const anchorKey = artifactCanvasSelectionIdentity(selection);
  const scope = useArtifactRequestScope(artifactId, versionId, anchorKey, creator.scopeKey);
  const createdCallback = useRef(onCreated);
  const recoveredCallback = useRef(onRecovered);
  const missingCallback = useRef(onMissing);
  useLayoutEffect(() => {
    createdCallback.current = onCreated;
    recoveredCallback.current = onRecovered;
    missingCallback.current = onMissing;
  }, [onCreated, onRecovered, onMissing]);
  const [note, updateNote] = useState('');
  const noteRef = useRef('');
  const setNote = useCallback((value: string) => {
    noteRef.current = value;
    updateNote(value);
  }, []);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<'create' | 'display' | null>(null);
  const [created, setCreated] = useState<XScienceArtifactAnnotation | null>(null);
  useLayoutEffect(() => {
    setNote('');
    setSaving(false);
    setError(null);
    setCreated(null);
  }, [artifactId, versionId, anchorKey, creator.owner, setNote]);
  const deliver = async (annotation: XScienceArtifactAnnotation, token: number) => {
    if (!scope.isCurrent(token)) return;
    try {
      await createdCallback.current(annotation);
    } catch (reason) {
      if (!scope.isCurrent(token)) return;
      console.error('[ArtifactSelectionAnnotationModal] Failed to display saved annotation:', diagnostic(reason));
      setError('display');
    }
  };
  const recovered = async (annotation: XScienceArtifactAnnotation) => {
    // Recover the old mutation without clearing or closing a newly edited draft.
    const token = scope.begin();
    if (token === null) return;
    try {
      if (scope.isCurrent(token)) {
        setError(null);
        await recoveredCallback.current?.(annotation);
      }
    } finally {
      scope.finish(token);
    }
  };
  const missing = useCallback(() => {
    if (!creator.owner.isCurrentAccount()) return;
    setError(null);
    missingCallback.current?.();
  }, [creator.owner]);
  const save = async () => {
    if (!isAnnotationNoteSaveable(noteRef.current) || created) return;
    const token = scope.begin();
    if (token === null) return;
    const originalNote = noteRef.current;
    setSaving(true);
    setError(null);
    try {
      const annotation = await creator.owner.start(
        annotationInputFromCanvasSelection(selection, originalNote.trim()),
        sourceHint
      );
      if (!annotation || !creator.owner.isCurrentAccount()) return;
      if (!scope.isCurrent(token)) return;
      if (noteRef.current !== originalNote) {
        await recoveredCallback.current?.(annotation);
        return;
      }
      setCreated(annotation);
      await deliver(annotation, token);
    } catch (reason) {
      if (!scope.isCurrent(token)) return;
      console.error('[ArtifactSelectionAnnotationModal] Failed to add selection annotation:', diagnostic(reason));
      setError('create');
    } finally {
      if (scope.isCurrent(token)) setSaving(false);
      scope.finish(token);
    }
  };
  const retryDisplay = async () => {
    if (!created) return;
    const token = scope.begin();
    if (token === null) return;
    setSaving(true);
    setError(null);
    try {
      await deliver(created, token);
    } finally {
      if (scope.isCurrent(token)) setSaving(false);
      scope.finish(token);
    }
  };
  return {
    note,
    setNote,
    saving: saving || creator.view.busy,
    error,
    created,
    save,
    retryDisplay,
    close: scope.close,
    creator,
    recovered,
    missing,
  };
}
const diagnostic = (reason: unknown) => redactErrorText(reason instanceof Error ? reason.message : String(reason));
