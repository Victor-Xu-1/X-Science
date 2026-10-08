/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  createSynonBiomedNote,
  deleteSynonBiomedNote,
  loadSynonBiomedNotes,
  updateSynonBiomedNote,
  type SynonBiomedNote,
  type SynonBiomedNoteTarget,
} from '@/renderer/services/synonBiomedNotes';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';

/** Editor state belongs to one target and one visible session, not a prop object's identity. */
export function useSynonBiomedNotesEditor(visible: boolean, target: SynonBiomedNoteTarget) {
  const { projectId, targetType, targetFrameId, targetMessageIndex, targetArtifactId } = target;
  const owner = useMemo<SynonBiomedNoteTarget>(
    () => ({
      projectId,
      targetType,
      targetFrameId,
      ...(targetMessageIndex === undefined ? {} : { targetMessageIndex }),
      ...(targetArtifactId === undefined ? {} : { targetArtifactId }),
    }),
    [projectId, targetType, targetFrameId, targetMessageIndex, targetArtifactId]
  );
  const [notes, setNotes] = useState<SynonBiomedNote[]>([]);
  const [draft, setDraft] = useState('');
  const [editingId, setEditingId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [mutationFailed, setMutationFailed] = useState<'save' | 'delete' | null>(null);
  const lifecycle = useRef(0);
  const readRevision = useRef(0);
  const active = useRef(false);
  const writing = useRef(false);

  const load = useCallback(async () => {
    if (!active.current || writing.current) return;
    const scope = lifecycle.current;
    const revision = ++readRevision.current;
    const isCurrent = () => active.current && lifecycle.current === scope && readRevision.current === revision;
    setLoading(true);
    setLoadFailed(false);
    try {
      const next = await loadSynonBiomedNotes(owner);
      if (isCurrent()) setNotes(next);
    } catch (reason) {
      if (!isCurrent()) return;
      console.warn('[SynonBiomedNotesModal] Failed to load notes:', diagnostic(reason));
      setNotes([]);
      setLoadFailed(true);
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [owner]);

  useLayoutEffect(() => {
    lifecycle.current += 1;
    active.current = visible;
    writing.current = false;
    setNotes([]);
    setDraft('');
    setEditingId(null);
    setSaving(false);
    setDeletingId(null);
    setMutationFailed(null);
    setLoadFailed(false);
    setLoading(false);
    if (visible) void load();
    return () => {
      active.current = false;
      lifecycle.current += 1;
      readRevision.current += 1;
    };
  }, [load, visible]);

  const save = async () => {
    if (!active.current || !draft.trim() || loading || writing.current) return;
    const scope = lifecycle.current;
    writing.current = true;
    setSaving(true);
    setMutationFailed(null);
    try {
      const saved = editingId
        ? await updateSynonBiomedNote(editingId, draft)
        : await createSynonBiomedNote(owner, draft);
      if (!active.current || lifecycle.current !== scope) return;
      setNotes((current) =>
        editingId ? current.map((note) => (note.id === saved.id ? saved : note)) : [saved, ...current]
      );
      setDraft('');
      setEditingId(null);
    } catch (reason) {
      if (!active.current || lifecycle.current !== scope) return;
      console.warn('[SynonBiomedNotesModal] Failed to save note:', diagnostic(reason));
      setMutationFailed('save');
    } finally {
      if (active.current && lifecycle.current === scope) {
        writing.current = false;
        setSaving(false);
      }
    }
  };

  const remove = async (noteId: string) => {
    if (!active.current || loading || writing.current || !notes.some((note) => note.id === noteId)) return;
    const scope = lifecycle.current;
    writing.current = true;
    setDeletingId(noteId);
    setMutationFailed(null);
    try {
      await deleteSynonBiomedNote(noteId);
      if (!active.current || lifecycle.current !== scope) return;
      setNotes((current) => current.filter((note) => note.id !== noteId));
      if (editingId === noteId) {
        setEditingId(null);
        setDraft('');
      }
    } catch (reason) {
      if (!active.current || lifecycle.current !== scope) return;
      console.warn('[SynonBiomedNotesModal] Failed to delete note:', diagnostic(reason));
      setMutationFailed('delete');
    } finally {
      if (active.current && lifecycle.current === scope) {
        writing.current = false;
        setDeletingId(null);
      }
    }
  };

  const edit = (note: SynonBiomedNote) => {
    if (writing.current) return;
    setEditingId(note.id);
    setDraft(note.content);
    setMutationFailed(null);
  };
  const cancelEditing = () => {
    if (writing.current) return;
    setEditingId(null);
    setDraft('');
    setMutationFailed(null);
  };

  return {
    notes,
    draft,
    setDraft,
    editingId,
    loading,
    loadFailed,
    saving,
    deletingId,
    mutationFailed,
    load,
    save,
    remove,
    edit,
    cancelEditing,
  };
}

const diagnostic = (error: unknown): string =>
  redactErrorText(error instanceof Error ? error.message : String(error || 'unknown error'));
