import React, { useCallback, useEffect, useId, useRef, useSyncExternalStore } from 'react';
import { Bell } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { notificationInboxStore as store } from '@/renderer/services/notifications/NotificationInboxStore';
import { NotificationInboxOverlay } from './NotificationInboxOverlay';
import { NotificationInboxList } from './NotificationInboxList';
import styles from './notificationInbox.module.css';

export default function NotificationBell({
  mobile = false,
  side = 'bottom',
  onNavigate,
  onOpen,
}: {
  mobile?: boolean;
  side?: 'top' | 'bottom';
  onNavigate?: (path: string) => void | Promise<void>;
  onOpen?: () => void;
}) {
  const { t } = useTranslation();
  const id = useId(),
    trigger = useRef<HTMLButtonElement>(null);
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  const open = snapshot.openBellId === id,
    unread = snapshot.data?.unreadCount;
  const close = useCallback(() => store.close(id), [id]);
  useEffect(() => close, [close]);
  const openItem = async (itemId: string) => {
    if (!onNavigate) return;
    if (await store.openTarget(itemId, onNavigate)) close();
  };
  return (
    <>
      <button
        ref={trigger}
        type='button'
        className={styles.trigger}
        data-notification-bell-trigger='true'
        disabled={!snapshot.enabled}
        aria-haspopup='dialog'
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-label={
          unread === undefined
            ? t('conversation.notificationInbox.triggerUnknown')
            : snapshot.data?.incomplete
              ? t('conversation.notificationInbox.triggerPartial', { count: unread })
              : unread > 0
                ? t('conversation.notificationInbox.triggerUnread', { count: unread })
                : t('conversation.notificationInbox.triggerRead')
        }
        onClick={() => {
          if (open) close();
          else {
            store.open(id);
            onOpen?.();
          }
        }}
      >
        <Bell size={16} strokeWidth={2} aria-hidden='true' />
        {unread !== undefined && unread > 0 && <span className={styles.dot} aria-hidden='true' />}
      </button>
      {open && (
        <NotificationInboxOverlay
          key={snapshot.account}
          id={id}
          anchor={trigger}
          mobile={mobile}
          side={side}
          label={t('conversation.notificationInbox.center')}
          dismissLabel={t('conversation.notificationInbox.dismiss')}
          onClose={close}
        >
          <NotificationInboxList
            snapshot={snapshot}
            mobile={mobile}
            onClose={close}
            onOpen={(itemId) => {
              void openItem(itemId);
            }}
          />
        </NotificationInboxOverlay>
      )}
    </>
  );
}
