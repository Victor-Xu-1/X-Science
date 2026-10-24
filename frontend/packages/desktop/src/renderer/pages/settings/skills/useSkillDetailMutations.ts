import { useLayoutEffect, useRef, useState } from 'react';
import {
  deleteXScienceSkillDraft,
  duplicateXScienceSkill,
  publishXScienceSkillDraft,
  saveXScienceSkillDraftFile,
} from '@/renderer/services/skills/xScienceSkillLibrary';
import type { SkillDetailFiles } from './useSkillDetailFiles';
import type { SkillDetailSession, SkillDetailTicket } from './useSkillDetailSession';

type DetailAction = 'save' | 'publish' | 'delete' | 'duplicate';
type Receipt = { ticket: SkillDetailTicket; action: DetailAction };
const failureKeys = {
  save: 'draftSaveFailed',
  publish: 'publishFailed',
  delete: 'draftDeleteFailed',
  duplicate: 'copyFailed',
} as const;
export const detailSuccessKeys = {
  save: 'draftSaved',
  publish: 'published',
  delete: 'draftDeleted',
  duplicate: 'copyCreated',
} as const;

/** Serial writes keep their original service payloads and never replay a saved receipt. */
export function useSkillDetailMutations({
  session,
  files,
  visible,
  name,
  draft,
  editable,
  onChanged,
  onClose,
  onCommitted,
  onSuccess,
}: {
  session: SkillDetailSession;
  files: SkillDetailFiles;
  visible: boolean;
  name: string | undefined;
  draft: boolean;
  editable: boolean;
  onChanged: () => void | Promise<void>;
  onClose: () => void;
  onCommitted: (action: DetailAction) => void;
  onSuccess: (action: DetailAction) => void;
}) {
  const [pending, setPending] = useState<DetailAction | 'refresh' | 'reload' | null>(null);
  const [failure, setFailure] = useState<
    | (typeof failureKeys)[DetailAction]
    | 'invalidCopyName'
    | 'refreshFailed'
    | 'draftReviewRequired'
    | 'draftChanged'
    | null
  >(null);
  const lock = useRef(false);
  const receipt = useRef<Receipt | null>(null);
  const callbacks = useRef({ onChanged, onClose, onCommitted, onSuccess });
  callbacks.current = { onChanged, onClose, onCommitted, onSuccess };
  useLayoutEffect(() => {
    lock.current = false;
    receipt.current = null;
    setPending(null);
    setFailure(null);
  }, [visible, name, draft, editable]);

  const refresh = async (saved: Receipt) => {
    if (!session.isCurrent(saved.ticket)) return;
    try {
      await callbacks.current.onChanged();
      if (!session.isCurrent(saved.ticket)) return;
      receipt.current = null;
      setFailure(null);
      callbacks.current.onSuccess(saved.action);
      if (saved.action !== 'save') callbacks.current.onClose();
    } catch (error) {
      if (!session.isCurrent(saved.ticket)) return;
      console.error('Failed to refresh the skill library after a completed change:', error);
      setFailure('refreshFailed');
    }
  };

  const canPublish = draft && editable && files.ready && !!files.draftRevision && files.content === files.original;
  const run = async (action: DetailAction, copyName = '') => {
    const ticket = session.capture();
    if (!ticket || lock.current || receipt.current) return;
    if ((action === 'save' || action === 'publish') && (!editable || !files.ready)) return;
    if ((action === 'publish' || action === 'delete') && !draft) return;
    if (action === 'publish' && !canPublish) return;
    const normalized = copyName.trim();
    if (action === 'duplicate' && !/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(normalized)) {
      setFailure('invalidCopyName');
      return;
    }
    const { path, content, original, draftRevision } = files;
    lock.current = true;
    setPending(action);
    setFailure(null);
    try {
      if (action === 'save') await saveXScienceSkillDraftFile(ticket.name, path, original, content);
      else if (action === 'publish') {
        if (!draftRevision) return;
        await publishXScienceSkillDraft(ticket.name, false, { expectedDraftRevision: draftRevision });
      } else if (action === 'delete') await deleteXScienceSkillDraft(ticket.name);
      else await duplicateXScienceSkill(ticket.name, normalized);
      if (!session.isCurrent(ticket)) return;
      if (action === 'save') {
        files.markSaved(path, content);
        // Saving is not review: display the real stored snapshot through the
        // same read owner before a later explicit publication may use its hash.
        if (draft) await files.reload(path);
        if (!session.isCurrent(ticket)) return;
      }
      const saved = { ticket, action };
      receipt.current = saved;
      callbacks.current.onCommitted(action);
      await refresh(saved);
    } catch (error) {
      if (!session.isCurrent(ticket)) return;
      console.error('Failed to change the skill:', error);
      const code = typeof error === 'object' && error !== null && 'code' in error ? error.code : null;
      if (action === 'publish' && (code === 'draft_revision_required' || code === 'draft_revision_conflict')) {
        files.invalidateDraftRevision();
        setFailure(code === 'draft_revision_conflict' ? 'draftChanged' : 'draftReviewRequired');
      } else setFailure(failureKeys[action]);
    } finally {
      if (session.isCurrent(ticket)) {
        lock.current = false;
        setPending(null);
      }
    }
  };

  const reloadDraft = async () => {
    const ticket = session.capture();
    if (!ticket || !draft || lock.current || receipt.current) return;
    lock.current = true;
    setPending('reload');
    setFailure(null);
    try {
      await files.reload();
    } finally {
      if (session.isCurrent(ticket)) {
        lock.current = false;
        setPending(null);
      }
    }
  };

  const retryRefresh = async () => {
    const saved = receipt.current;
    if (!saved || !session.isCurrent(saved.ticket) || lock.current) return;
    lock.current = true;
    setPending('refresh');
    try {
      await refresh(saved);
    } finally {
      if (session.isCurrent(saved.ticket)) {
        lock.current = false;
        setPending(null);
      }
    }
  };
  return {
    run,
    reloadDraft,
    retryRefresh,
    canPublish,
    failure,
    busy: pending !== null,
    pending,
    needsRefresh: failure === 'refreshFailed',
  };
}
