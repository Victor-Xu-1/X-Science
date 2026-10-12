/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useRef, useState } from 'react';
import {
  createXScienceArtifactAnnotation,
  type XScienceArtifactAnnotation,
} from '@/renderer/services/xScienceAnnotations';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import { artifactCanvasSelectionIdentity, type XScienceArtifactCanvasSelection } from './artifactCanvasSelection';
import { useArtifactRequestScope } from './useArtifactRequestScope';

export function useArtifactSelectionEditor(
  artifactId: string,
  versionId: string,
  selection: XScienceArtifactCanvasSelection,
  onCreated: (annotation: XScienceArtifactAnnotation) => void | Promise<void>
) {
  const anchorKey = artifactCanvasSelectionIdentity(selection);
  const scope = useArtifactRequestScope(artifactId, versionId, anchorKey);
  const createdCallback = useRef(onCreated);
  useLayoutEffect(() => {
    createdCallback.current = onCreated;
  }, [onCreated]);
  const [note, setNote] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<'create' | 'display' | null>(null);
  const [created, setCreated] = useState<XScienceArtifactAnnotation | null>(null);
  useLayoutEffect(() => {
    setNote('');
    setSaving(false);
    setError(null);
    setCreated(null);
  }, [artifactId, versionId, anchorKey]);

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

  const save = async () => {
    if (!note.trim() || created) return;
    const token = scope.begin();
    if (token === null) return;
    setSaving(true);
    setError(null);
    try {
      const annotation = await createXScienceArtifactAnnotation(artifactId, versionId, {
        type: selection.type,
        text: note.trim(),
        selectionText: selection.type === 'point' ? null : selection.text,
        selectionPrefix: selection.type === 'text_selection' ? selection.selectionPrefix : null,
        startLine: selection.type === 'text_selection' ? selection.startLine : null,
        startColumn: selection.type === 'text_selection' ? selection.startColumn : null,
        endLine: selection.type === 'text_selection' ? selection.endLine : null,
        endColumn: selection.type === 'text_selection' ? selection.endColumn : null,
        xPercent: selection.type === 'text_selection' ? null : selection.xPercent,
        yPercent: selection.type === 'text_selection' ? null : selection.yPercent,
        pageNumber: selection.type === 'html_element' ? null : selection.pageNumber,
        elementSelector: selection.type === 'html_element' ? selection.elementSelector : null,
        elementDescriptor: selection.type === 'html_element' ? selection.elementDescriptor : null,
      });
      if (!scope.isCurrent(token)) return;
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
  return { note, setNote, saving, error, created, save, retryDisplay, close: scope.close };
}

const diagnostic = (reason: unknown) => redactErrorText(reason instanceof Error ? reason.message : String(reason));
