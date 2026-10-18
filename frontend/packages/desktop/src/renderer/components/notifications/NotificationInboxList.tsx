import React from 'react';
import { AlertCircle, CheckCircle2, CheckCheck, Shield, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type {
  InboxNotification,
  InboxView,
  NotificationKind,
} from '@/renderer/services/notifications/notificationInboxTypes';
import { notificationInboxStore as store } from '@/renderer/services/notifications/NotificationInboxStore';
import styles from './notificationInbox.module.css';

const kindKeys = {
  'task.completed': 'conversation.notificationInbox.completed',
  'task.failed': 'conversation.notificationInbox.failed',
  'task.needs-attention': 'conversation.notificationInbox.attention',
  'authorization.required': 'conversation.notificationInbox.authorization',
} as const;
function EventIcon({ kind }: { kind: NotificationKind }) {
  if (kind === 'task.completed') return <CheckCircle2 size={16} className={styles.success} />;
  if (kind === 'task.failed') return <AlertCircle size={16} className={styles.failure} />;
  if (kind === 'authorization.required') return <Shield size={16} className={styles.attention} />;
  return <AlertCircle size={16} className={styles.attention} />;
}
function NotificationRow({
  item,
  disabled,
  onOpen,
}: {
  item: InboxNotification;
  disabled: boolean;
  onOpen: (id: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const age = Math.max(0, Date.now() - Date.parse(item.createdAt));
  const unit = age < 3600000 ? 'minute' : age < 86400000 ? 'hour' : 'day';
  const divisor = unit === 'minute' ? 60000 : unit === 'hour' ? 3600000 : 86400000;
  const when =
    age < 60000
      ? t('conversation.notificationInbox.justNow')
      : new Intl.RelativeTimeFormat(i18n.resolvedLanguage || i18n.language, { numeric: 'auto' }).format(
          -Math.floor(age / divisor),
          unit
        );
  const label = item.targetInvalidatedAt
    ? t('conversation.notificationInbox.targetUnavailable')
    : item.actionState === 'resolved'
      ? t('conversation.notificationInbox.resolved')
      : t(kindKeys[item.kind]);
  return (
    <button
      type='button'
      className={`${styles.row} ${item.readAt === null ? styles.unread : ''}`}
      disabled={disabled || item.targetInvalidatedAt !== null}
      onClick={() => onOpen(item.id)}
      data-notification-id={item.id}
    >
      <span className={styles.rowIcon} aria-hidden='true'>
        <EventIcon kind={item.kind} />
      </span>
      <span className={styles.rowBody}>
        <span className={styles.context}>
          <span>{label}</span>
          <time dateTime={item.createdAt}>{when}</time>
        </span>
        <span className={styles.rowTitle} title={item.title || t(kindKeys[item.kind])}>
          {item.title || t(kindKeys[item.kind])}
        </span>
        {item.actionState === 'pending' && (
          <span className={styles.badge}>{t('conversation.notificationInbox.pending')}</span>
        )}
      </span>
      {item.readAt === null && <span className={styles.rowDot} aria-hidden='true' />}
    </button>
  );
}
export function NotificationInboxList({
  snapshot,
  mobile,
  onClose,
  onOpen,
}: {
  snapshot: InboxView;
  mobile: boolean;
  onClose: () => void;
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const data = snapshot.data;
  const items = data?.items ?? [];
  const groups = [
    { key: 'unread' as const, items: items.filter((item) => item.readAt === null) },
    { key: 'earlier' as const, items: items.filter((item) => item.readAt !== null) },
  ];
  return (
    <>
      <header className={styles.header}>
        {mobile && (
          <button
            type='button'
            className={styles.action}
            aria-label={t('conversation.notificationInbox.close')}
            onClick={onClose}
          >
            <X size={18} />
          </button>
        )}
        <div className={styles.heading}>
          {t('conversation.notificationInbox.title')}
          <div className={styles.meta}>
            {data
              ? data.incomplete
                ? t('conversation.notificationInbox.recoveredUnreadCount', { count: data.unreadCount })
                : data.unreadCount > 0
                  ? t('conversation.notificationInbox.unreadCount', { count: data.unreadCount })
                  : t('conversation.notificationInbox.caughtUp')
              : t('conversation.notificationInbox.loading')}
          </div>
        </div>
        <button
          type='button'
          className={styles.action}
          disabled={!data || data.unreadCount === 0 || snapshot.writeStatus !== 'idle'}
          onClick={() => {
            if (data) void store.markAllRead(data.readReceipt);
          }}
        >
          <CheckCheck size={14} aria-hidden='true' />
          {t('conversation.notificationInbox.markAll')}
        </button>
      </header>
      <div className={styles.list}>
        {snapshot.readError && (
          <div role='alert' className={`${styles.notice} ${styles.error}`}>
            {t('conversation.notificationInbox.readFailed')}
            {data && <p>{t('conversation.notificationInbox.previous')}</p>}
            <button
              type='button'
              className={styles.action}
              onClick={() => {
                void store.refresh();
              }}
            >
              {t('common.retry')}
            </button>
          </div>
        )}
        {snapshot.writeError && (
          <div role='alert' className={`${styles.notice} ${styles.error}`}>
            {t('conversation.notificationInbox.writeFailed')}
            <button
              type='button'
              className={styles.action}
              onClick={() => {
                void store.retryWrite();
              }}
            >
              {t('conversation.notificationInbox.retryWrite')}
            </button>
          </div>
        )}
        {snapshot.targetError && (
          <p role='alert' className={`${styles.notice} ${styles.error}`}>
            {t('conversation.notificationInbox.openFailed')}
          </p>
        )}
        {!data && !snapshot.readError && (
          <p role='status' className={`${styles.notice} ${styles.empty}`}>
            {t('conversation.notificationInbox.loading')}
          </p>
        )}
        {data && items.length === 0 && (
          <p className={`${styles.notice} ${styles.empty}`}>{t('conversation.notificationInbox.empty')}</p>
        )}
        {groups
          .filter((group) => group.items.length > 0)
          .map((group) => (
            <section key={group.key}>
              <h3 className={styles.group}>{t(`conversation.notificationInbox.${group.key}`)}</h3>
              {group.items.map((item) => (
                <NotificationRow
                  key={item.id}
                  item={item}
                  disabled={snapshot.openingTarget !== null || snapshot.writeStatus === 'pending'}
                  onOpen={onOpen}
                />
              ))}
            </section>
          ))}
        {data?.incomplete && (
          <div className={styles.paging}>
            <p role='status' className={styles.notice}>
              {t('conversation.notificationInbox.incomplete')}
            </p>
            {data.incompleteReason === 'capacity' && (
              <p className={styles.notice}>{t('conversation.notificationInbox.capacity')}</p>
            )}
            {data.incompleteReason === 'source_unavailable' && (
              <p className={styles.notice}>{t('conversation.notificationInbox.sourceUnavailable')}</p>
            )}
            <button
              type='button'
              className={styles.action}
              disabled={snapshot.status === 'loading'}
              onClick={() => {
                void (data.nextCursor ? store.loadMore() : store.refresh());
              }}
            >
              {t(data.nextCursor ? 'conversation.notificationInbox.continueScan' : 'common.retry')}
            </button>
          </div>
        )}
        {data?.historyTruncated && (
          <p className={styles.notice}>{t('conversation.notificationInbox.historyTruncated')}</p>
        )}
        {data && <p className={styles.notice}>{t('conversation.notificationInbox.historyScope')}</p>}
      </div>
    </>
  );
}
