import React, { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { Archive, RotateCcw, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  getArchiveOperationsSnapshot,
  isArchiveOperationPending,
  runConversationArchive,
  subscribeArchiveOperations,
} from '@/renderer/services/runtime/conversationArchiveOperations';
import SettingsPageWrapper from './components/SettingsPageWrapper';
import SettingsPageHeader from './components/SettingsPageHeader';
import { useArchivedConversations } from './archived/useArchivedConversations';
import './archived/archivedSettings.css';

export default function ArchivedSettings() {
  const { t, i18n } = useTranslation();
  const page = useArchivedConversations();
  const operations = useSyncExternalStore(subscribeArchiveOperations, getArchiveOperationsSnapshot);
  const [failed, setFailed] = useState(false);
  const scope = getRendererAccountScopeToken();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    setFailed(false);
  }, [scope]);
  const restore = async (conversation: TChatConversation) => {
    if (isArchiveOperationPending(conversation)) return;
    setFailed(false);
    try {
      await runConversationArchive(conversation, false);
    } catch {
      if (mounted.current && scope === getRendererAccountScopeToken()) setFailed(true);
    }
  };
  const formatTime = (timestamp: number) =>
    new Intl.DateTimeFormat(i18n.language, { dateStyle: 'medium', timeStyle: 'short' }).format(timestamp);
  return (
    <SettingsPageWrapper>
      <div className='archived-conversations' data-testid='x-science-archived-settings'>
        <SettingsPageHeader
          title={t('conversation.history.archivedSection')}
          description={t('conversation.history.archivedDescription')}
          actions={
            <button
              type='button'
              className='archived-conversations__action'
              disabled={page.loading}
              onClick={() => {
                void page.refresh();
              }}
            >
              <RefreshCw size={14} aria-hidden='true' />
              {t('common.refresh')}
            </button>
          }
        />
        {failed && (
          <p role='alert' className='archived-conversations__note'>
            {t('conversation.history.archiveRestoreFailed')}
          </p>
        )}
        {page.error && (
          <p role='alert' className='archived-conversations__note'>
            {t('conversation.history.archivedLoadFailed')}{' '}
            <button
              type='button'
              className='archived-conversations__action'
              onClick={() => {
                void page.retry();
              }}
            >
              {t('common.retry')}
            </button>
          </p>
        )}
        {page.items.length > 0 ? (
          <ul className='archived-conversations__list'>
            {page.items.map((conversation) => (
              <li key={conversation.id} className='archived-conversations__row'>
                <Archive size={16} strokeWidth={1.8} className='archived-conversations__icon' aria-hidden='true' />
                <div className='archived-conversations__copy'>
                  <p className='archived-conversations__title'>{conversation.name}</p>
                  <p className='archived-conversations__meta'>
                    {t('conversation.history.archivedWhen', { when: formatTime(conversation.extra.archived_at!) })}
                  </p>
                </div>
                <button
                  type='button'
                  className='archived-conversations__action'
                  disabled={isArchiveOperationPending(conversation, operations)}
                  aria-label={t('conversation.history.restoreNamed', { title: conversation.name })}
                  onClick={() => {
                    void restore(conversation);
                  }}
                >
                  <RotateCcw size={14} aria-hidden='true' />
                  {t('conversation.history.archiveRestore')}
                </button>
              </li>
            ))}
          </ul>
        ) : !page.loading && !page.error ? (
          <div className='archived-conversations__empty'>
            <Archive size={22} strokeWidth={1.5} aria-hidden='true' />
            <p>{t('conversation.history.archivedEmpty')}</p>
          </div>
        ) : null}
        {page.loading && (
          <p role='status' className='archived-conversations__note'>
            {t('common.loading')}
          </p>
        )}
        {page.hasMore && !page.error && (
          <button
            type='button'
            className='archived-conversations__action archived-conversations__more'
            disabled={page.loading}
            onClick={() => {
              void page.loadMore();
            }}
          >
            {t('conversation.history.loadMore')}
          </button>
        )}
      </div>
    </SettingsPageWrapper>
  );
}
