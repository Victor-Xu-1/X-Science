import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
const events = vi.hoisted(() => ({
  wakes: new Set<(event: { frame_id: string; root_frame_id: string }) => void>(),
  reconnects: new Set<() => void>(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    kernel: {
      executionCellUpdate: {
        on: (listener: (event: { frame_id: string; root_frame_id: string }) => void) => {
          events.wakes.add(listener);
          return () => events.wakes.delete(listener);
        },
      },
    },
    realtime: {
      reconnected: {
        on: (listener: () => void) => {
          events.reconnects.add(listener);
          return () => events.reconnects.delete(listener);
        },
      },
    },
  },
}));
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { getNotebookSessionController } from '@/renderer/services/notebook/NotebookSessionController';
import { owner, pendingPage, receipt, request, response } from './manualNotebookFixtures';
import { deferred, notebookFixture, submissions } from './manualNotebookControllerFixture';

const controller = () => getNotebookSessionController('frame-1', 'root-1');
const ready = async (session: ReturnType<typeof controller>) => {
  await vi.waitFor(() => expect(session.getSnapshot()).toMatchObject({ status: 'ready', scanComplete: true }));
};
beforeEach(() => {
  setRendererAccountOwner(null);
  setRendererAccountOwner(owner);
});
afterEach(() => {
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
  expect(events.wakes.size).toBe(0);
  expect(events.reconnects.size).toBe(0);
});

describe('shared Notebook presentation controller', () => {
  it('reserves cancellation once when acceptance arrives during the original cancel lookup', async () => {
    const fixture = notebookFixture(),
      submission = deferred<Response>(),
      lookup = deferred<Response>(),
      cancellation = deferred<void>();
    fixture.submit = (body) => {
      fixture.accept(body);
      return submission.promise;
    };
    fixture.lookup = () => lookup.promise;
    fixture.cancel = async (body) => {
      await cancellation.promise;
      return response(receipt(body, 'cancel_requested'));
    };
    const session = controller(),
      close = session.subscribe(() => {});
    await ready(session);
    const sending = session.run(session.getSnapshot().selectedKey!, 'print("cancel once")');
    await vi.waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body));
    const cancelling = session.cancel(body.request_id);
    const cancelCalls = () => fixture.fetch.mock.calls.filter(([input]) => String(input).endsWith('/cancel'));
    try {
      await vi.waitFor(() => expect(session.readRequests.has(body.request_id)).toBe(true));
      submission.resolve(response(receipt(body)));
      await vi.waitFor(() => expect(cancelCalls()).toHaveLength(1));
      lookup.resolve(response(receipt(body)));
      await vi.waitFor(() => expect(session.readRequests.has(body.request_id)).toBe(false));
      expect(cancelCalls()).toHaveLength(1);
    } finally {
      cancellation.resolve();
      await Promise.all([sending, cancelling]);
      close();
    }
  });

  it('shares two consumers, one observer and one synchronously reserved submission', async () => {
    const fixture = notebookFixture(),
      pending = deferred<Response>();
    fixture.submit = () => pending.promise;
    const first = controller(),
      second = controller();
    expect(second).toBe(first);
    const releaseFirst = first.subscribe(() => {}),
      releaseSecond = second.subscribe(() => {});
    await ready(first);
    expect(events.wakes.size).toBe(1);
    expect(events.reconnects.size).toBe(1);
    expect(fixture.fetch.mock.calls.filter(([input]) => String(input).endsWith('/kernels'))).toHaveLength(1);
    const key = first.getSnapshot().selectedKey!;
    const sending = first.run(key, 'print("single")\n');
    const duplicate = second.run(key, 'print("duplicate")\n');
    await vi.waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body));
    fixture.accept(body);
    pending.resolve(response(receipt(body)));
    await Promise.all([sending, duplicate]);
    expect(first.getSnapshot().requests).toHaveLength(1);
    expect(first.getSnapshot().requests[0].request?.code).toBe('print("single")\n');
    releaseFirst();
    expect(events.wakes.size).toBe(1);
    releaseSecond();
    expect(events.wakes.size).toBe(0);
  });

  it('retains the original uncertain UUID over view closure and only reads on reopening', async () => {
    const fixture = notebookFixture();
    fixture.submit = async () => {
      throw new TypeError('lost response');
    };
    const session = controller(),
      release = session.subscribe(() => {});
    await ready(session);
    await session.run(session.getSnapshot().selectedKey!, 'print("retained")');
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body));
    expect(session.getSnapshot().requests[0]).toMatchObject({
      id: body.request_id,
      phase: 'uncertain',
      error: 'notAccepted',
    });
    release();
    fixture.accept(body);
    const reopened = controller(),
      close = reopened.subscribe(() => {});
    expect(reopened).toBe(session);
    await vi.waitFor(() =>
      expect(reopened.getSnapshot().requests[0]).toMatchObject({ id: body.request_id, phase: 'accepted' })
    );
    expect(submissions(fixture)).toHaveLength(1);
    expect(reopened.getSnapshot().requests[0].request).toEqual(body);
    close();
  });

  it('preserves a confirmed receipt when an overlapping POST response later fails', async () => {
    const fixture = notebookFixture(),
      pending = deferred<Response>();
    fixture.submit = (body) => {
      fixture.accept(body);
      return pending.promise;
    };
    const session = controller(),
      close = session.subscribe(() => {});
    await ready(session);
    const sending = session.run(session.getSnapshot().selectedKey!, 'print("accepted")');
    await vi.waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body));
    await session.lookup(body.request_id);
    expect(session.getSnapshot().requests[0].phase).toBe('accepted');
    pending.reject(new TypeError('lost response'));
    await sending;
    expect(session.getSnapshot().requests[0]).toMatchObject({
      phase: 'accepted',
      receipt: { requestId: body.request_id },
    });
    expect(submissions(fixture)).toHaveLength(1);
    close();
  });

  it('keeps an empty incomplete discovery page blocked until explicit bounded continuation', async () => {
    const fixture = notebookFixture();
    fixture.pending = (cursor) => response(pendingPage([], cursor ? '' : 'cursor-1'));
    const session = controller(),
      close = session.subscribe(() => {});
    await vi.waitFor(() =>
      expect(session.getSnapshot()).toMatchObject({
        status: 'ready',
        scanComplete: false,
        nextCursor: 'cursor-1',
        scanning: false,
      })
    );
    await session.run(session.getSnapshot().selectedKey!, 'print("not permitted yet")');
    expect(submissions(fixture)).toHaveLength(0);
    expect(fixture.fetch.mock.calls.filter(([input]) => String(input).includes('/kernel-executions'))).toHaveLength(1);
    await session.loadMorePending();
    expect(session.getSnapshot()).toMatchObject({ scanComplete: true, nextCursor: null });
    expect(fixture.fetch.mock.calls.filter(([input]) => String(input).includes('/kernel-executions'))).toHaveLength(2);
    close();
  });

  it('disposes account-bound ownership and ignores a late result from the old account', async () => {
    const fixture = notebookFixture(),
      pending = deferred<Response>();
    fixture.submit = () => pending.promise;
    const old = controller();
    old.subscribe(() => {});
    await ready(old);
    const sending = old.run(old.getSnapshot().selectedKey!, request.code);
    await vi.waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body));
    setRendererAccountOwner('another-owner');
    const next = controller();
    expect(next).not.toBe(old);
    expect(old.disposed).toBe(true);
    expect(events.wakes.size).toBe(0);
    pending.resolve(response(receipt(body)));
    await sending;
    expect(next.getSnapshot().requests).toEqual([]);
    expect(old.getSnapshot().requests[0].receipt).toBeNull();
  });
});
