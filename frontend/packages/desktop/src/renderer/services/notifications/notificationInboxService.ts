import { requestXScienceJson, type XScienceGatewayOptions } from '../xScienceHttp';
import type { InboxMutation, InboxNotification, InboxSnapshot, NotificationKind } from './notificationInboxTypes';

const base = '/api/notifications/inbox';
const kinds = new Set(['task.completed', 'task.failed', 'task.needs-attention', 'authorization.required']);
const record = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);
const text = (value: unknown, max: number): value is string =>
  typeof value === 'string' && value.length > 0 && new TextEncoder().encode(value).length <= max;
const timestamp = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const optionalTimestamp = (value: unknown): string | null => {
  if (value === undefined || value === null) return null;
  if (!timestamp(value)) throw new Error('invalid_notification_timestamp');
  return value;
};
const invalid = (): never => {
  throw new Error('invalid_notification_inbox');
};

export function decodeNotificationInbox(value: unknown): InboxSnapshot {
  if (
    !record(value) ||
    value.schema !== 'x-science.notification-inbox.v1' ||
    value.history_scope !== 'active-transcript-sources' ||
    !Number.isSafeInteger(value.revision) ||
    Number(value.revision) < 0 ||
    !Number.isSafeInteger(value.unread_count) ||
    Number(value.unread_count) < 0 ||
    !Array.isArray(value.items) ||
    value.items.length > 1200 ||
    !text(value.read_receipt, 512) ||
    typeof value.reconciliation_incomplete !== 'boolean'
  )
    return invalid();
  const nextCursor =
    value.next_cursor === undefined || value.next_cursor === null || value.next_cursor === ''
      ? null
      : value.next_cursor;
  if (nextCursor !== null && !text(nextCursor, 512)) return invalid();
  if (!value.reconciliation_incomplete && nextCursor !== null) return invalid();
  const reason = value.incomplete_reason ?? null;
  if (reason !== null && !['source_unavailable', 'capacity', 'more_sources'].includes(String(reason))) return invalid();
  if (value.history_truncated !== undefined && typeof value.history_truncated !== 'boolean') return invalid();
  const seen = new Set<string>();
  const items: InboxNotification[] = value.items.map((item) => {
    if (
      !record(item) ||
      !text(item.id, 128) ||
      seen.has(item.id) ||
      !text(item.origin_id, 2048) ||
      !kinds.has(String(item.kind)) ||
      !text(item.conversation_id, 512) ||
      !text(item.frame_incarnation_id, 512) ||
      typeof item.title !== 'string' ||
      item.title.length > 8192 ||
      !timestamp(item.created_at) ||
      (item.action_state !== undefined &&
        item.action_state !== null &&
        item.action_state !== 'pending' &&
        item.action_state !== 'resolved')
    )
      return invalid();
    seen.add(item.id);
    return Object.freeze({
      id: item.id,
      originId: item.origin_id,
      kind: item.kind as NotificationKind,
      conversationId: item.conversation_id,
      frameIncarnationId: item.frame_incarnation_id,
      title: item.title,
      createdAt: item.created_at,
      readAt: optionalTimestamp(item.read_at),
      actionState: (item.action_state as InboxNotification['actionState']) ?? null,
      targetInvalidatedAt: optionalTimestamp(item.target_invalidated_at),
    });
  });
  if (Number(value.unread_count) !== items.filter((item) => item.readAt === null).length) return invalid();
  return Object.freeze({
    historyScope: 'active-transcript-sources' as const,
    revision: Number(value.revision),
    unreadCount: Number(value.unread_count),
    items: Object.freeze(items),
    readReceipt: value.read_receipt,
    incomplete: value.reconciliation_incomplete,
    nextCursor: nextCursor as string | null,
    incompleteReason: reason as InboxSnapshot['incompleteReason'],
    historyTruncated: value.history_truncated === true,
  });
}
const optionsWithTimeout = (options: XScienceGatewayOptions) => ({ timeoutMs: 15000, ...options });
export async function readNotificationInbox(
  cursor: string | null = null,
  options: XScienceGatewayOptions = {}
): Promise<InboxSnapshot> {
  if (cursor !== null && !text(cursor, 512)) return invalid();
  const url = cursor === null ? base : `${base}?cursor=${encodeURIComponent(cursor)}`;
  return decodeNotificationInbox(await requestXScienceJson(url, { cache: 'no-store' }, optionsWithTimeout(options)));
}
export async function writeNotificationRead(
  command: InboxMutation,
  options: XScienceGatewayOptions = {}
): Promise<InboxSnapshot> {
  const body = command.kind === 'read' ? { ids: command.ids } : { read_receipt: command.receipt };
  if (
    command.kind === 'read'
      ? command.ids.length === 0 ||
        command.ids.length > 1200 ||
        command.ids.some((id) => !text(id, 128)) ||
        new Set(command.ids).size !== command.ids.length
      : !text(command.receipt, 512)
  )
    return invalid();
  return decodeNotificationInbox(
    await requestXScienceJson(
      `${base}/${command.kind}`,
      { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) },
      optionsWithTimeout(options)
    )
  );
}
export async function verifyNotificationTarget(
  item: InboxNotification,
  options: XScienceGatewayOptions = {}
): Promise<string> {
  if (item.targetInvalidatedAt !== null) throw new Error('notification_target_unavailable');
  const detail = await requestXScienceJson<unknown>(
    `/api/conversations/${encodeURIComponent(item.conversationId)}`,
    { cache: 'no-store' },
    optionsWithTimeout(options)
  );
  if (
    !record(detail) ||
    detail.id !== item.conversationId ||
    !record(detail.extra) ||
    detail.extra.frame_incarnation_id !== item.frameIncarnationId
  )
    throw new Error('notification_target_changed');
  return `/conversation/${encodeURIComponent(item.conversationId)}`;
}
