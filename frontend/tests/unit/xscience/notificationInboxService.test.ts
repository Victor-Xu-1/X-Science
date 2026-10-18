import { describe, expect, it, vi } from 'vitest';
import {
  decodeNotificationInbox,
  readNotificationInbox,
  verifyNotificationTarget,
  writeNotificationRead,
} from '@/renderer/services/notifications/notificationInboxService';
import { inboxItem, inboxResponse, inboxSnapshot } from './notificationInboxFixtures';

describe('human notification inbox typed transport', () => {
  it('keeps cursor and original read receipt opaque and never supplies an owner or approval operation', async () => {
    const fetchImpl = vi.fn().mockImplementation(async () => inboxResponse(inboxSnapshot()));
    await readNotificationInbox('opaque +/cursor', { fetchImpl });
    await writeNotificationRead({ kind: 'read', ids: ['notice-1'] }, { fetchImpl });
    await writeNotificationRead({ kind: 'read-all', receipt: 'original-receipt' }, { fetchImpl });
    expect(fetchImpl.mock.calls.map(([url]) => url)).toEqual([
      '/api/notifications/inbox?cursor=opaque%20%2B%2Fcursor',
      '/api/notifications/inbox/read',
      '/api/notifications/inbox/read-all',
    ]);
    expect(fetchImpl.mock.calls[1][1].body).toBe('{"ids":["notice-1"]}');
    expect(fetchImpl.mock.calls[2][1].body).toBe('{"read_receipt":"original-receipt"}');
    expect(fetchImpl.mock.calls.every(([, init]) => init.credentials === 'include')).toBe(true);
  });

  it('retains incomplete empty snapshots and explicit historical truncation without inventing a total', () => {
    expect(
      decodeNotificationInbox(
        inboxSnapshot([], 3, {
          reconciliation_incomplete: true,
          next_cursor: 'source-next',
          incomplete_reason: 'more_sources',
          history_truncated: true,
        })
      )
    ).toMatchObject({ items: [], unreadCount: 0, incomplete: true, nextCursor: 'source-next', historyTruncated: true });
    const stale = inboxItem('gone', { target_invalidated_at: '2026-10-18T02:00:00Z' });
    expect(decodeNotificationInbox(inboxSnapshot([stale])).unreadCount).toBe(1);
  });

  it.each([
    { unread_count: 99 },
    { revision: 1.2 },
    { read_receipt: '' },
    { history_scope: 'all-history' },
    { items: [inboxItem(), inboxItem()] },
    { items: [inboxItem('bad', { kind: 'agent_message' })] },
    { items: [inboxItem('bad', { frame_incarnation_id: '' })] },
    { next_cursor: 'unannounced-incomplete-scan' },
  ])('rejects invalid wire instead of displaying an authoritative-looking empty result: %j', (patch) => {
    expect(() => decodeNotificationInbox(inboxSnapshot(undefined, 1, patch))).toThrow();
  });

  it('preflights the exact frame incarnation before navigation and does not execute or mark during verification', async () => {
    const item = decodeNotificationInbox(inboxSnapshot()).items[0];
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(
        inboxResponse({ id: item.conversationId, extra: { frame_incarnation_id: item.frameIncarnationId } })
      )
      .mockResolvedValueOnce(
        inboxResponse({ id: item.conversationId, extra: { frame_incarnation_id: 'replacement' } })
      );
    expect(await verifyNotificationTarget(item, { fetchImpl })).toBe('/conversation/frame-notice-1');
    await expect(verifyNotificationTarget(item, { fetchImpl })).rejects.toThrow();
    expect(fetchImpl.mock.calls.every(([, init]) => !init.method || init.method === 'GET')).toBe(true);
    await expect(
      verifyNotificationTarget({ ...item, targetInvalidatedAt: '2026-10-18T02:00:00Z' }, { fetchImpl })
    ).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });
});
