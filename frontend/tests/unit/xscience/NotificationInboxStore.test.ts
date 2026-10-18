import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.mock('@/common', () => ({ ipcBridge: {} }));
import { NotificationInboxStore } from '@/renderer/services/notifications/NotificationInboxStore';
import { decodeNotificationInbox } from '@/renderer/services/notifications/notificationInboxService';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { inboxDeferred, inboxItem, inboxResponse, inboxSnapshot } from './notificationInboxFixtures';

const stores: NotificationInboxStore[] = [];
const makeStore = (observe = vi.fn(() => () => {})) => {
  const store = new NotificationInboxStore(observe);
  stores.push(store);
  return store;
};
beforeEach(() => {
  setRendererAccountOwner('inbox-owner');
});
afterEach(() => {
  setRendererAccountOwner(null);
  stores.splice(0).forEach((store) => store.reset());
  vi.unstubAllGlobals();
});
const decoded = (revision = 1) => decodeNotificationInbox(inboxSnapshot(undefined, revision));

describe('account-scoped notification inbox store', () => {
  it('uses one observation and one initial GET for two consumers', async () => {
    const stop = vi.fn(),
      observe = vi.fn(() => stop),
      store = makeStore(observe);
    const fetchImpl = vi.fn(async () => inboxResponse(inboxSnapshot()));
    vi.stubGlobal('fetch', fetchImpl);
    const first = store.subscribe(() => {}),
      second = store.subscribe(() => {});
    await store.refresh();
    expect(observe).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    first();
    expect(stop).not.toHaveBeenCalled();
    second();
    expect(stop).toHaveBeenCalledTimes(1);
  });

  it('retains the original explicit read-all receipt after a lost reply and a newer refresh', async () => {
    const store = makeStore(),
      old = inboxItem(),
      newer = inboxItem('new');
    store.receive(decoded());
    const fetchImpl = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('reply lost'))
      .mockResolvedValueOnce(inboxResponse(inboxSnapshot([old, newer], 2)))
      .mockResolvedValueOnce(inboxResponse(inboxSnapshot([{ ...old, read_at: '2026-10-18T02:00:00Z' }, newer], 3)));
    vi.stubGlobal('fetch', fetchImpl);
    expect(await store.markAllRead('receipt-1')).toBe(false);
    expect(store.snapshot).toMatchObject({ writeStatus: 'uncertain', writeError: true, data: { unreadCount: 1 } });
    await store.refresh();
    expect(store.snapshot.data?.readReceipt).toBe('receipt-2');
    expect(await store.markAllRead('receipt-2')).toBe(false);
    expect(await store.retryWrite()).toBe(true);
    expect(fetchImpl.mock.calls[0][1].body).toBe('{"read_receipt":"receipt-1"}');
    expect(fetchImpl.mock.calls[2][1].body).toBe(fetchImpl.mock.calls[0][1].body);
    expect(store.snapshot.data?.unreadCount).toBe(1);
  });

  it('reserves duplicate marking synchronously and does not overwrite a newer ack with a late GET', async () => {
    const store = makeStore(),
      read = inboxDeferred<Response>(),
      write = inboxDeferred<Response>();
    store.receive(decoded());
    const fetchImpl = vi.fn().mockReturnValueOnce(read.promise).mockReturnValueOnce(write.promise);
    vi.stubGlobal('fetch', fetchImpl);
    const reading = store.refresh(),
      marking = store.markRead(['notice-1']);
    expect(await store.markRead(['notice-1'])).toBe(false);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
    write.resolve(inboxResponse(inboxSnapshot([inboxItem('notice-1', { read_at: '2026-10-18T02:00:00Z' })], 2)));
    expect(await marking).toBe(true);
    read.resolve(inboxResponse(inboxSnapshot()));
    await reading;
    expect(store.snapshot).toMatchObject({ status: 'ready', data: { revision: 2, unreadCount: 0 } });
  });

  it('keeps old rows after a failed read and only retries on another explicit read', async () => {
    const store = makeStore();
    store.receive(decoded());
    const fetchImpl = vi
      .fn()
      .mockRejectedValueOnce(new Error('storage unavailable'))
      .mockResolvedValueOnce(inboxResponse(inboxSnapshot()));
    vi.stubGlobal('fetch', fetchImpl);
    await store.refresh();
    expect(store.snapshot).toMatchObject({
      status: 'error',
      readError: true,
      data: { items: [expect.objectContaining({ id: 'notice-1' })] },
    });
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    await store.refresh();
    expect(store.snapshot.readError).toBe(false);
  });

  it('continues an incomplete scan only on demand and replaces with the full accumulated snapshot', async () => {
    const store = makeStore();
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(
        inboxResponse(inboxSnapshot([], 1, { reconciliation_incomplete: true, next_cursor: 'cursor-one' }))
      )
      .mockResolvedValueOnce(inboxResponse(inboxSnapshot([inboxItem(), inboxItem('two')], 2)));
    vi.stubGlobal('fetch', fetchImpl);
    await store.refresh();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    await store.loadMore();
    expect(fetchImpl.mock.calls[1][0]).toBe('/api/notifications/inbox?cursor=cursor-one');
    expect(store.snapshot.data?.items).toHaveLength(2);
  });

  it('drops an old-account late write result and its retained marking intent', async () => {
    const store = makeStore(),
      pending = inboxDeferred<Response>();
    store.receive(decoded());
    vi.stubGlobal(
      'fetch',
      vi.fn(() => pending.promise)
    );
    const marking = store.markAllRead('receipt-1');
    setRendererAccountOwner('next-owner');
    store.reset();
    pending.resolve(inboxResponse(inboxSnapshot([], 2)));
    expect(await marking).toBe(false);
    expect(store.snapshot.data).toBeNull();
    expect(store.command).toBeNull();
    expect(store.snapshot.writeStatus).toBe('idle');
  });

  it('does not navigate or mark after the view closes during exact-target preflight', async () => {
    const store = makeStore(),
      pending = inboxDeferred<Response>(),
      navigate = vi.fn();
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(inboxResponse(inboxSnapshot()))
      .mockReturnValueOnce(pending.promise);
    vi.stubGlobal('fetch', fetchImpl);
    store.open('bell');
    await store.refresh();
    const opening = store.openTarget('notice-1', navigate);
    store.close('bell');
    pending.resolve(inboxResponse({ id: 'frame-notice-1', extra: { frame_incarnation_id: 'incarnation-notice-1' } }));
    expect(await opening).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });
});
