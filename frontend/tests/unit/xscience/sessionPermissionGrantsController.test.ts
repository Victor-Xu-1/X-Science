import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { SessionPermissionGrantsController } from '@/renderer/services/runtime/sessionPermissionGrantsController';
import {
  sessionGrantA,
  sessionGrantB,
  sessionGrantC,
  sessionGrantIdentity,
  sessionGrantInvalidation,
  sessionGrantRevokeBody,
  sessionGrantRevokeResponse,
  sessionGrantsSnapshot,
} from './sessionPermissionGrantsFixtures';

const { httpRequest } = vi.hoisted(() => ({ httpRequest: vi.fn() }));
const { subscribeInvalidation } = vi.hoisted(() => ({
  subscribeInvalidation: vi.fn((_source: string, _callback: (event?: unknown) => void) => vi.fn()),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      listChanged: {
        on: (listener: (event: unknown) => void) =>
          subscribeInvalidation('listChanged', (event) => listener(event ?? sessionGrantInvalidation)),
      },
    },
    realtime: {
      reconnected: {
        on: (listener: () => void) => subscribeInvalidation('reconnected', () => listener()),
      },
    },
  },
}));
vi.mock('@/common/adapter/httpBridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest,
}));
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
let cleanups: Array<() => void> = [];
function createController() {
  const controller = new SessionPermissionGrantsController(sessionGrantIdentity);
  cleanups.push(controller.attach());
  return controller;
}
async function opened() {
  const controller = createController();
  const close = controller.open();
  cleanups.push(close);
  await vi.waitFor(() => expect(controller.getSnapshot().snapshot).not.toBeNull());
  return { controller, close };
}
const posts = () => httpRequest.mock.calls.filter(([method]) => method === 'POST');

describe('SessionPermissionGrantsController', () => {
  beforeEach(() => {
    httpRequest.mockReset();
    subscribeInvalidation.mockClear();
    setRendererAccountOwner(null);
    setRendererAccountOwner(sessionGrantIdentity.ownerId);
    httpRequest.mockImplementation(async (method: string) =>
      method === 'GET' ? sessionGrantsSnapshot() : sessionGrantRevokeResponse()
    );
  });
  afterEach(() => {
    cleanups.toReversed().forEach((cleanup) => cleanup());
    cleanups = [];
    setRendererAccountOwner(null);
  });
  it('observes an unopened composer without granting any mutation presentation authority', async () => {
    const controller = createController();
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot?.grants).toEqual([sessionGrantA]));
    expect(httpRequest).toHaveBeenCalledTimes(1);
    expect(subscribeInvalidation).toHaveBeenCalledTimes(2);
    await controller.revoke(controller.getSnapshot().snapshot!);
    await controller.retry();
    expect(posts()).toHaveLength(0);
  });
  it('keeps one observer across closed presentations and refreshes canonical changes without posting', async () => {
    const { controller, close } = await opened();
    close();
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantA, sessionGrantC]));
    subscribeInvalidation.mock.calls[0][1]();
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot?.grants).toHaveLength(2));
    expect(httpRequest).toHaveBeenCalledTimes(2);
    expect(httpRequest.mock.calls[1][1]).toContain('expected_root_frame_incarnation_id=grant-root-incarnation');
    expect(subscribeInvalidation).toHaveBeenCalledTimes(2);
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 0)).toBe(true);
    await controller.revoke(controller.getSnapshot().snapshot!);
    expect(posts()).toHaveLength(0);
  });
  it('refcounts composer observation without duplicate reads and destroys late reads at final unmount', async () => {
    const pending = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const controller = new SessionPermissionGrantsController(sessionGrantIdentity);
    const first = controller.attach();
    const second = controller.attach();
    expect(httpRequest).toHaveBeenCalledTimes(1);
    expect(subscribeInvalidation).toHaveBeenCalledTimes(2);
    first();
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 0)).toBe(true);
    const signal = httpRequest.mock.calls[0][3].signal as AbortSignal;
    second();
    expect(signal.aborted).toBe(true);
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 1)).toBe(true);
    pending.resolve(sessionGrantsSnapshot());
    await pending.promise;
    await Promise.resolve();
    expect(controller.getSnapshot().snapshot).toBeNull();
    expect(posts()).toHaveLength(0);
  });
  it('fences a closed composer read when the account changes and never accepts its late count', async () => {
    const controller = createController();
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot?.grants).toHaveLength(1));
    const pending = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    subscribeInvalidation.mock.calls[0][1]();
    setRendererAccountOwner('new-account');
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 1)).toBe(true);
    pending.resolve(sessionGrantsSnapshot([sessionGrantA, sessionGrantB]));
    await pending.promise;
    await Promise.resolve();
    expect(controller.getSnapshot().snapshot).toBeNull();
    expect(controller.getSnapshot().available).toBe(false);
    expect(posts()).toHaveLength(0);
  });
  it('shares one read between two views and refreshes the frozen scope only after the last view closes', async () => {
    const { controller, close } = await opened();
    const secondClose = controller.open();
    expect(httpRequest).toHaveBeenCalledTimes(1);
    close();
    expect(controller.getSnapshot().available).toBe(true);
    secondClose();
    cleanups.push(controller.open());
    await vi.waitFor(() => expect(httpRequest).toHaveBeenCalledTimes(2));
    expect(httpRequest.mock.calls[1][1]).toContain('expected_root_frame_incarnation_id=grant-root-incarnation');
  });
  it('remembers a marker during bootstrap and rereads under only the real returned scope', async () => {
    const bootstrap = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(bootstrap.promise);
    const controller = createController();
    subscribeInvalidation.mock.calls[0][1]({
      ...sessionGrantInvalidation,
      root_frame_incarnation_id: 'untrusted-event-incarnation',
    });
    expect(httpRequest).toHaveBeenCalledTimes(1);
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantA, sessionGrantC]));
    bootstrap.resolve(sessionGrantsSnapshot());
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot?.grants).toHaveLength(2));
    expect(httpRequest).toHaveBeenCalledTimes(2);
    expect(httpRequest.mock.calls[1][1]).toContain('expected_root_frame_incarnation_id=grant-root-incarnation');
    expect(httpRequest.mock.calls[1][1]).not.toContain('untrusted-event-incarnation');
    expect(posts()).toHaveLength(0);
  });
  it('treats a late unlike-incarnation marker only as a scoped read hint, never new scope or grant data', async () => {
    const controller = createController();
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot).not.toBeNull());
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot());
    subscribeInvalidation.mock.calls[0][1]({
      ...sessionGrantInvalidation,
      root_frame_incarnation_id: 'old-incarnation',
      grants: [sessionGrantC],
    });
    await vi.waitFor(() => expect(controller.getSnapshot().loading).toBe(false));
    expect(httpRequest).toHaveBeenCalledTimes(2);
    expect(httpRequest.mock.calls[1][1]).toContain('expected_root_frame_incarnation_id=grant-root-incarnation');
    expect(controller.getSnapshot().snapshot?.scope.root_frame_incarnation_id).toBe('grant-root-incarnation');
    expect(controller.getSnapshot().snapshot?.grants).toEqual([sessionGrantA]);
    expect(posts()).toHaveLength(0);
  });
  it('lets the old-scope GET detect real root recreation and blocks writes without event-driven rebinding', async () => {
    const { controller } = await opened();
    httpRequest.mockRejectedValueOnce(
      new BackendHttpError({
        method: 'GET',
        path: '/scope',
        status: 409,
        body: { code: 'SESSION_GRANT_SCOPE_STALE' },
      })
    );
    subscribeInvalidation.mock.calls[0][1]({
      ...sessionGrantInvalidation,
      root_frame_incarnation_id: 'real-new-incarnation',
    });
    await vi.waitFor(() => expect(controller.getSnapshot().available).toBe(false));
    expect(httpRequest.mock.calls[1][1]).toContain('expected_root_frame_incarnation_id=grant-root-incarnation');
    await controller.revoke(controller.getSnapshot().snapshot!);
    expect(posts()).toHaveLength(0);
    expect(httpRequest).toHaveBeenCalledTimes(2);
  });
  it('ignores unmarked, wrong-root, malformed, and old-account event hints', async () => {
    const controller = createController();
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot).not.toBeNull());
    const marker = subscribeInvalidation.mock.calls[0][1];
    marker({ ...sessionGrantInvalidation, permission_grants_changed: false });
    marker({ ...sessionGrantInvalidation, root_frame_id: 'foreign-root' });
    marker({ ...sessionGrantInvalidation, conversation_id: 'foreign-root' });
    marker({ ...sessionGrantInvalidation, root_frame_incarnation_id: '' });
    marker({ ...sessionGrantInvalidation, source: 'other' });
    expect(httpRequest).toHaveBeenCalledTimes(1);
    setRendererAccountOwner('other-account');
    marker(sessionGrantInvalidation);
    await controller.refresh();
    expect(httpRequest).toHaveBeenCalledTimes(1);
    expect(posts()).toHaveLength(0);
  });
  it('reserves a mutation synchronously against repeated revoke and retry clicks', async () => {
    const { controller } = await opened();
    const pending = deferred<ReturnType<typeof sessionGrantRevokeResponse>>();
    httpRequest.mockReturnValue(pending.promise);
    const snapshot = controller.getSnapshot().snapshot!;
    const first = controller.revoke(snapshot);
    await controller.revoke(snapshot);
    await controller.retry();
    expect(posts()).toHaveLength(1);
    pending.resolve(sessionGrantRevokeResponse());
    await first;
    expect(controller.getSnapshot().snapshot?.grants).toEqual([]);
  });
  it('keeps the exact original body for explicit retry after lost ACK, including after read-only refresh', async () => {
    const { controller } = await opened();
    const seen = controller.getSnapshot().snapshot!;
    httpRequest.mockRejectedValueOnce(new Error('network'));
    await controller.revoke(seen);
    expect(controller.getSnapshot().retryable).toBe(true);
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantC]));
    await controller.refresh();
    expect(posts()).toHaveLength(1);
    const response = sessionGrantRevokeResponse([sessionGrantA], [sessionGrantC]);
    response.results[0].status = 'already_absent';
    httpRequest.mockResolvedValueOnce(response);
    await controller.retry();
    expect(posts()).toHaveLength(2);
    expect(posts()[1][2]).toBe(posts()[0][2]);
    expect(posts()[1][2]).toEqual(sessionGrantRevokeBody());
    expect(controller.getSnapshot().results[0].status).toBe('already_absent');
  });
  it('honors the rendered older snapshot rather than replacing its IDs with a refreshed list', async () => {
    const { controller } = await opened();
    const rendered = controller.getSnapshot().snapshot!;
    const renewed = { ...sessionGrantA, grant_id: 'new-generation' };
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([renewed, sessionGrantC]));
    await controller.refresh();
    const response = sessionGrantRevokeResponse([sessionGrantA], [renewed, sessionGrantC]);
    response.results[0].status = 'changed';
    httpRequest.mockResolvedValueOnce(response);
    await controller.revoke(rendered);
    expect(posts()[0][2]).toEqual(sessionGrantRevokeBody());
    expect(controller.getSnapshot().results[0].status).toBe('changed');
    expect(controller.getSnapshot().snapshot?.grants).toHaveLength(2);
    expect(posts()).toHaveLength(1);
  });
  it('retains successful removals and changed rows without automatic replacement mutations', async () => {
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantA, sessionGrantB]));
    const { controller } = await opened();
    const renewed = { ...sessionGrantA, grant_id: 'renewed' };
    const result = sessionGrantRevokeResponse([sessionGrantA, sessionGrantB], [renewed]);
    result.results[0].status = 'changed';
    httpRequest.mockResolvedValueOnce(result);
    await controller.revoke(controller.getSnapshot().snapshot!);
    expect(controller.getSnapshot().snapshot?.grants).toEqual([renewed]);
    expect(controller.getSnapshot().retryable).toBe(false);
    expect(posts()).toHaveLength(1);
  });
  it('ignores a late mutation ACK after close/reopen and retains the original command for explicit retry', async () => {
    const { controller, close } = await opened();
    const pending = deferred<ReturnType<typeof sessionGrantRevokeResponse>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const operation = controller.revoke(controller.getSnapshot().snapshot!);
    close();
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantC]));
    cleanups.push(controller.open());
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot?.grants).toEqual([sessionGrantC]));
    pending.resolve(sessionGrantRevokeResponse());
    await operation;
    expect(controller.getSnapshot().snapshot?.grants).toEqual([sessionGrantC]);
    expect(controller.getSnapshot().retryable).toBe(true);
    expect(posts()).toHaveLength(1);
  });
  it('clears account data and rejects late bootstrap data and writes from the old owner', async () => {
    const pending = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const controller = createController();
    cleanups.push(controller.open());
    setRendererAccountOwner('another-owner');
    pending.resolve(sessionGrantsSnapshot());
    await pending.promise;
    await Promise.resolve();
    expect(controller.getSnapshot().snapshot).toBeNull();
    expect(controller.getSnapshot().available).toBe(false);
    await controller.revoke(sessionGrantsSnapshot());
    expect(posts()).toHaveLength(0);
  });
  it('blocks stale scope responses without attempting a fresh bootstrap or mutation retry', async () => {
    const { controller, close } = await opened();
    httpRequest.mockRejectedValueOnce(
      new BackendHttpError({
        method: 'POST',
        path: '/scope',
        status: 409,
        body: { code: 'SESSION_GRANT_SCOPE_STALE' },
      })
    );
    await controller.revoke(controller.getSnapshot().snapshot!);
    expect(controller.getSnapshot().error).toBe('scope');
    close();
    cleanups.push(controller.open());
    await controller.refresh();
    await controller.retry();
    expect(posts()).toHaveLength(1);
    expect(httpRequest).toHaveBeenCalledTimes(2);
  });
  it('retains the prior list on read failure and never substitutes a successful empty list', async () => {
    const { controller } = await opened();
    const old = controller.getSnapshot().snapshot;
    httpRequest.mockRejectedValueOnce(new Error('storage unavailable'));
    await controller.refresh();
    expect(controller.getSnapshot().snapshot).toBe(old);
    expect(controller.getSnapshot().error).toBe('read');
  });
  it('aborts and fences a delayed refresh before submitting a visible revoke', async () => {
    const { controller } = await opened();
    const pending = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const refresh = controller.refresh();
    const signal = httpRequest.mock.calls.at(-1)![3].signal as AbortSignal;
    httpRequest.mockResolvedValueOnce(sessionGrantRevokeResponse());
    await controller.revoke(controller.getSnapshot().snapshot!);
    expect(signal.aborted).toBe(true);
    pending.resolve(sessionGrantsSnapshot());
    await refresh;
    expect(controller.getSnapshot().snapshot?.grants).toEqual([]);
  });
  it('fences late responses after the old frame presentation unmounts', async () => {
    const { controller } = await opened();
    const pending = deferred<ReturnType<typeof sessionGrantRevokeResponse>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const write = controller.revoke(controller.getSnapshot().snapshot!);
    cleanups.forEach((cleanup) => cleanup());
    const before = controller.getSnapshot().snapshot;
    pending.resolve(sessionGrantRevokeResponse());
    await write;
    expect(controller.getSnapshot().snapshot).toBe(before);
  });
  it('treats canonical root/frame invalidation as a coalesced scoped read, never an invented grant or mutation', async () => {
    const { controller, close } = await opened();
    expect(subscribeInvalidation.mock.calls.map(([source]) => source)).toEqual(['listChanged', 'reconnected']);
    const callbacks = subscribeInvalidation.mock.calls.map((call) => call[1] as unknown as () => void);
    const read = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(read.promise);
    callbacks[0]();
    callbacks[1]();
    callbacks[0]();
    expect(httpRequest).toHaveBeenCalledTimes(2);
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantA, sessionGrantC]));
    read.resolve(sessionGrantsSnapshot());
    await vi.waitFor(() => expect(controller.getSnapshot().snapshot?.grants).toHaveLength(2));
    expect(httpRequest).toHaveBeenCalledTimes(3);
    expect(posts()).toHaveLength(0);
    close();
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 0)).toBe(true);
    cleanups.forEach((cleanup) => cleanup());
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 1)).toBe(true);
  });
});
