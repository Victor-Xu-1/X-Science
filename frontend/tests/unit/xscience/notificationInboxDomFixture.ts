import { vi } from 'vitest';
import { inboxItem, inboxResponse, inboxSnapshot, type RawInboxItem } from './notificationInboxFixtures';

export function notificationInboxFixture() {
  const receipts = new Map<string, string[]>();
  const fixture = {
    items: [inboxItem()] as RawInboxItem[],
    revision: 1,
    extra: {} as Record<string, unknown>,
    read: null as null | ((cursor: string | null) => Promise<Response>),
    write: null as null | ((path: string, body: Record<string, unknown>) => Promise<Response>),
    target: null as null | ((conversationId: string) => Promise<Response>),
    fetch: vi.fn<typeof fetch>(),
    snapshot() {
      const data = inboxSnapshot(fixture.items, fixture.revision, fixture.extra);
      receipts.set(
        data.read_receipt,
        fixture.items.map((item) => item.id)
      );
      return data;
    },
    apply(path: string, body: Record<string, unknown>) {
      const ids = path.endsWith('/read-all') ? receipts.get(String(body.read_receipt)) : (body.ids as string[]);
      if (!ids) return inboxResponse({ code: 'NOTIFICATION_READ_CONFLICT' }, 409);
      fixture.items = fixture.items.map((item) =>
        ids.includes(item.id) ? { ...item, read_at: item.read_at ?? '2026-10-18T02:00:00Z' } : item
      );
      fixture.revision++;
      return inboxResponse(fixture.snapshot());
    },
  };
  fixture.fetch.mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/notifications/inbox')
      return fixture.read ? fixture.read(url.searchParams.get('cursor')) : inboxResponse(fixture.snapshot());
    if (url.pathname === '/api/notifications/inbox/read' || url.pathname === '/api/notifications/inbox/read-all') {
      if (init?.method !== 'POST') throw new Error('marking must be explicit POST');
      const body = JSON.parse(String(init.body));
      return fixture.write ? fixture.write(url.pathname, body) : fixture.apply(url.pathname, body);
    }
    if (url.pathname.startsWith('/api/conversations/')) {
      const id = decodeURIComponent(url.pathname.slice('/api/conversations/'.length));
      if (fixture.target) return fixture.target(id);
      const item = fixture.items.find((candidate) => candidate.conversation_id === id);
      return item
        ? inboxResponse({ id, extra: { frame_incarnation_id: item.frame_incarnation_id } })
        : inboxResponse({ code: 'NOT_FOUND' }, 404);
    }
    throw new Error('unexpected notification route: ' + url.pathname);
  });
  vi.stubGlobal('fetch', fixture.fetch);
  return fixture;
}
export const inboxWrites = (fixture: ReturnType<typeof notificationInboxFixture>) =>
  fixture.fetch.mock.calls.filter(([, init]) => init?.method === 'POST');
