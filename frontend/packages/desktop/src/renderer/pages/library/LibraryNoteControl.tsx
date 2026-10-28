import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { libraryNoteCommand, libraryPersonalNote, type LibraryNoteCommand } from './libraryPersonalNote';
import './libraryNoteControl.css';

type Attempt = {
  command: LibraryNoteCommand;
  readKey: string;
  acknowledged?: LiteratureReceipt;
};
export type LibraryNoteControlProps = {
  item: LiteratureItem;
  scope: string;
  readKey: string;
  readStatus: 'loading' | 'ready' | 'error';
  refreshing?: boolean;
  refreshError?: boolean;
  disabled: boolean;
  retryDisabled: boolean;
  pendingId?: string;
  receipt?: LiteratureReceipt;
  commit: (command: LibraryNoteCommand, scope: string) => Promise<LiteratureReceipt>;
  reload: (scope: string) => boolean;
};

/** Ephemeral row interaction only; the Page reader and mutation controller retain all authority. */
export function LibraryNoteControl(props: LibraryNoteControlProps) {
  const { t } = useTranslation();
  const { item, scope, readKey, readStatus, refreshing, refreshError } = props;
  const [edit, setEdit] = useState<{ base: LiteratureItem; draft: string }>();
  const [attempt, setAttempt] = useState<Attempt>();
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<'write' | 'readback' | 'length'>();
  const savingRef = useRef(false);
  const cancelBlur = useRef(false);
  const live = useRef(true);
  const current = useRef(props);
  current.current = props;
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, [scope, item.id]);
  const dirty = edit !== undefined && edit.draft !== libraryPersonalNote(edit.base);
  const base = dirty ? edit.base : item;
  const draft = dirty ? edit.draft : libraryPersonalNote(item);
  const samePending = Boolean(attempt && props.pendingId === attempt.command.operation_id);
  const readOnly = props.disabled || saving || samePending;

  // A retry from the existing global notice may acknowledge this same original operation.
  useEffect(() => {
    if (!attempt || attempt.acknowledged || props.receipt?.operation_id !== attempt.command.operation_id) return;
    setAttempt({ ...attempt, acknowledged: props.receipt });
    setFailure(undefined);
    savingRef.current = true;
    setSaving(true);
  }, [attempt, props.receipt]);

  // An ACK is historical: finish only after the existing read owner observes a new outcome.
  useEffect(() => {
    if (!attempt?.acknowledged || readKey === attempt.readKey || refreshing) return;
    if (refreshError || readStatus === 'error') {
      setFailure('readback');
      savingRef.current = false;
      setSaving(false);
      return;
    }
    if (readStatus !== 'ready') return;
    if (item.metadata_revision < attempt.acknowledged.item.metadata_revision) {
      setFailure('readback');
      savingRef.current = false;
      setSaving(false);
      return;
    }
    setEdit(undefined);
    setAttempt(undefined);
    setFailure(undefined);
    savingRef.current = false;
    setSaving(false);
  }, [attempt, readKey, readStatus, refreshing, refreshError, item.metadata_revision]);

  const commit = async () => {
    if (cancelBlur.current) {
      cancelBlur.current = false;
      return;
    }
    if (savingRef.current || props.retryDisabled) return;
    if (attempt?.acknowledged) {
      setAttempt({ ...attempt, readKey });
      setFailure(undefined);
      savingRef.current = true;
      setSaving(true);
      if (!props.reload(scope)) {
        savingRef.current = false;
        setSaving(false);
        setFailure('readback');
      }
      return;
    }
    if (props.disabled && !samePending) return;
    let next = attempt;
    if (!next) {
      try {
        const command = libraryNoteCommand(base, draft, crypto.randomUUID());
        if (!command) {
          setEdit(undefined);
          setFailure(undefined);
          return;
        }
        next = { command, readKey };
      } catch {
        setFailure('length');
        return;
      }
    }
    const original = next;
    setAttempt(original);
    setFailure(undefined);
    savingRef.current = true;
    setSaving(true);
    try {
      const receipt = await props.commit(original.command, scope);
      if (!live.current || current.current.scope !== scope || current.current.item.id !== item.id) return;
      setAttempt({ ...original, acknowledged: receipt });
    } catch {
      if (!live.current || current.current.scope !== scope || current.current.item.id !== item.id) return;
      savingRef.current = false;
      setSaving(false);
      setFailure('write');
    }
  };
  const retryBlocked = props.retryDisabled || Boolean(props.pendingId && !samePending);
  return (
    <div className='library-note'>
      <input
        type='text'
        value={draft}
        readOnly={readOnly}
        aria-label={t('conversation.libraryNotes.forPaper', { title: item.title })}
        aria-busy={saving}
        aria-invalid={Boolean(failure) || undefined}
        placeholder={t('conversation.libraryNotes.placeholder')}
        onFocus={() => {
          if (!readOnly && !dirty) setEdit({ base: item, draft: libraryPersonalNote(item) });
        }}
        onChange={(event) => {
          if (readOnly) return;
          setEdit({ base, draft: event.currentTarget.value });
          if (!attempt?.acknowledged && !samePending) setAttempt(undefined);
          setFailure(undefined);
        }}
        onKeyDown={(event) => {
          if (saving || event.nativeEvent.isComposing || event.keyCode === 229) return;
          if (event.key === 'Enter') {
            event.preventDefault();
            event.currentTarget.blur();
          }
          if (event.key === 'Escape') {
            cancelBlur.current = true;
            setEdit(undefined);
            setFailure(undefined);
            if (!attempt?.acknowledged && !samePending) setAttempt(undefined);
            event.currentTarget.blur();
          }
        }}
        onBlur={() => {
          if (!props.disabled) void commit();
        }}
      />
      {failure && (
        <div className='library-note__failure'>
          <span role='alert'>
            {t(
              failure === 'length'
                ? 'conversation.libraryNotes.tooLong'
                : failure === 'readback'
                  ? 'conversation.libraryNotes.savedReloadFailed'
                  : 'conversation.libraryNotes.draftPreserved'
            )}
          </span>
          <button
            type='button'
            disabled={saving || retryBlocked}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => void commit()}
          >
            {t('common.retry')}
          </button>
        </div>
      )}
    </div>
  );
}
