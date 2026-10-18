import React, { useEffect, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { Archive, X } from 'lucide-react';
import {
  dismissArchiveUndo,
  expireArchiveUndo,
  getArchiveOperationsSnapshot,
  isArchiveOperationPending,
  subscribeArchiveOperations,
  undoConversationArchive,
} from '@/renderer/services/runtime/conversationArchiveOperations';
import './archiveUndo.css';

/** One authenticated-layout owner, so navigation never duplicates the notice. */
export default function ArchiveUndoNotice() {
  const { t } = useTranslation();
  const state = useSyncExternalStore(subscribeArchiveOperations, getArchiveOperationsSnapshot);
  useEffect(() => {
    const expiring = state.notices.filter((notice) => !state.undoing.has(notice.key));
    if (!expiring.length) return;
    const deadline = Math.min(...expiring.map((notice) => notice.expiresAt));
    const timer = setTimeout(expireArchiveUndo, Math.max(0, deadline - Date.now()));
    return () => clearTimeout(timer);
  }, [state.notices, state.undoing]);
  if (!state.notices.length) return null;
  return (
    <aside className='conversation-archive-notices' aria-label={t('conversation.history.archiveNotifications')}>
      {state.notices.map((notice) => (
        <div key={notice.key} className='conversation-archive-notice' role='status'>
          <Archive size={15} aria-hidden='true' />
          <span className='conversation-archive-notice__text'>
            {t(notice.failed ? 'conversation.history.archiveUndoFailed' : 'conversation.history.archivedNotice', {
              title: notice.conversation.name,
            })}
          </span>
          <button
            type='button'
            disabled={state.undoing.has(notice.key) || isArchiveOperationPending(notice.conversation, state)}
            onClick={() => {
              void undoConversationArchive(notice.key).catch((): undefined => undefined);
            }}
          >
            {t('conversation.history.archiveUndo')}
          </button>
          <button type='button' aria-label={t('common.close')} onClick={() => dismissArchiveUndo(notice.key)}>
            <X size={14} aria-hidden='true' />
          </button>
        </div>
      ))}
    </aside>
  );
}
