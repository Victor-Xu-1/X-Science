/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useRef, useState } from 'react';
import {
  applySynonBiomedArtifactEdit,
  suggestSynonBiomedArtifactEdit,
  type SynonBiomedAppliedArtifactEdit,
  type SynonBiomedArtifactEditMode,
} from '@/renderer/services/synonBiomedAnnotations';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import { useArtifactRequestScope } from './useArtifactRequestScope';

export type SuggestionView = 'diff' | 'full';
type Status = 'idle' | 'suggesting' | 'ready' | 'applying' | 'refreshing' | 'applied';

export function useArtifactRefinementEditor(
  artifactId: string,
  versionId: string,
  selectedText: string,
  initialInstruction: string,
  onApplied: (result: SynonBiomedAppliedArtifactEdit) => void | Promise<void>
) {
  const scope = useArtifactRequestScope(artifactId, versionId, selectedText, initialInstruction);
  const appliedCallback = useRef(onApplied);
  useLayoutEffect(() => {
    appliedCallback.current = onApplied;
  }, [onApplied]);
  const [instruction, setInstruction] = useState(initialInstruction);
  const [mode, setMode] = useState<SynonBiomedArtifactEditMode>('edit');
  const [requestMode, setRequestMode] = useState<SynonBiomedArtifactEditMode>('edit');
  const [suggestion, setSuggestion] = useState<string | null>(null);
  const [editedSuggestion, setEditedSuggestion] = useState<string | null>(null);
  const [view, setView] = useState<SuggestionView>('diff');
  const [manualEditing, setManualEditing] = useState(false);
  const [status, setStatus] = useState<Status>('idle');
  const [error, setError] = useState<'generate' | 'apply' | 'refresh' | null>(null);
  const [saved, setSaved] = useState<SynonBiomedAppliedArtifactEdit | null>(null);
  const replacement = editedSuggestion ?? suggestion ?? '';

  useLayoutEffect(() => {
    setInstruction(initialInstruction);
    setMode('edit');
    setRequestMode('edit');
    setSuggestion(null);
    setEditedSuggestion(null);
    setView('diff');
    setManualEditing(false);
    setStatus('idle');
    setError(null);
    setSaved(null);
  }, [artifactId, versionId, selectedText, initialInstruction]);

  const generate = async (nextMode: SynonBiomedArtifactEditMode) => {
    const annotationText = instruction.trim();
    if (!annotationText || saved) return;
    const token = scope.begin();
    if (token === null) return;
    setRequestMode(nextMode);
    setStatus('suggesting');
    setError(null);
    try {
      const next = await suggestSynonBiomedArtifactEdit(artifactId, versionId, {
        selectedText,
        annotationText,
        ...(nextMode === 'edit' && mode === 'edit' && replacement && replacement !== selectedText
          ? { currentIteration: replacement }
          : {}),
        mode: nextMode,
      });
      if (!scope.isCurrent(token)) return;
      setMode(nextMode);
      setSuggestion(next);
      setEditedSuggestion(null);
      setManualEditing(false);
      setView(nextMode === 'edit' ? 'diff' : 'full');
      setStatus('ready');
    } catch (reason) {
      if (!scope.isCurrent(token)) return;
      console.error('[ArtifactEditRefinementPanel] Failed to generate suggestion:', diagnostic(reason));
      setError('generate');
      setStatus(suggestion ? 'ready' : 'idle');
    } finally {
      scope.finish(token);
    }
  };

  const showSaved = async (result: SynonBiomedAppliedArtifactEdit, token: number) => {
    if (!scope.isCurrent(token)) return;
    setStatus('refreshing');
    setError(null);
    try {
      await appliedCallback.current(result);
      if (scope.isCurrent(token)) setStatus('applied');
    } catch (reason) {
      if (!scope.isCurrent(token)) return;
      console.error('[ArtifactEditRefinementPanel] Failed to refresh saved version:', diagnostic(reason));
      setError('refresh');
      setStatus('applied');
    }
  };

  const apply = async () => {
    if (saved || status !== 'ready' || mode !== 'edit' || !replacement || replacement === selectedText) return;
    const token = scope.begin();
    if (token === null) return;
    setStatus('applying');
    setError(null);
    try {
      const result = await applySynonBiomedArtifactEdit(artifactId, versionId, {
        selectedText,
        replacementText: replacement,
      });
      if (!scope.isCurrent(token)) return;
      setSaved(result);
      await showSaved(result, token);
    } catch (reason) {
      if (!scope.isCurrent(token)) return;
      console.error('[ArtifactEditRefinementPanel] Failed to apply edit:', diagnostic(reason));
      setError('apply');
      setStatus('ready');
    } finally {
      scope.finish(token);
    }
  };

  const retryDisplay = async () => {
    if (!saved) return;
    const token = scope.begin();
    if (token === null) return;
    try {
      await showSaved(saved, token);
    } finally {
      scope.finish(token);
    }
  };

  return {
    instruction,
    setInstruction,
    mode,
    requestMode,
    suggestion,
    editedSuggestion,
    setEditedSuggestion,
    view,
    setView,
    manualEditing,
    setManualEditing,
    status,
    error,
    saved,
    replacement,
    generate,
    apply,
    retryDisplay,
    close: scope.close,
  };
}

const diagnostic = (reason: unknown) => redactErrorText(reason instanceof Error ? reason.message : String(reason));
