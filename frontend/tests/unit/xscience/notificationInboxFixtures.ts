export type RawInboxItem = {
  id: string;
  origin_id: string;
  kind: string;
  conversation_id: string;
  frame_incarnation_id: string;
  title: string;
  created_at: string;
  read_at?: string;
  action_state?: string;
  target_invalidated_at?: string;
};
export const inboxItem = (id = 'notice-1', patch: Partial<RawInboxItem> = {}): RawInboxItem => ({
  id,
  origin_id: 'canonical-' + id,
  kind: 'task.completed',
  conversation_id: 'frame-' + id,
  frame_incarnation_id: 'incarnation-' + id,
  title: 'Original task ' + id,
  created_at: '2026-10-18T01:00:00Z',
  ...patch,
});
export const inboxSnapshot = (
  items: RawInboxItem[] = [inboxItem()],
  revision = 1,
  patch: Record<string, unknown> = {}
) => ({
  schema: 'x-science.notification-inbox.v1',
  history_scope: 'active-transcript-sources',
  revision,
  unread_count: items.filter((item) => !item.read_at).length,
  items,
  read_receipt: 'receipt-' + revision,
  reconciliation_incomplete: false,
  history_truncated: false,
  ...patch,
});
export const inboxResponse = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'content-type': 'application/json' } });
export function inboxDeferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
