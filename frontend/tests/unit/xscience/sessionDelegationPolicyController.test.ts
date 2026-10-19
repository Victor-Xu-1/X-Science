import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { SessionDelegationPolicyController } from '@/renderer/services/runtime/sessionDelegationPolicyController';

const { httpRequest, events } = vi.hoisted(() => ({
  httpRequest: vi.fn(),
  events: new Map<string, (...args: unknown[]) => void>(),
}));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest,
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      listChanged: {
        on: (callback: (...args: unknown[]) => void) => {
          events.set('change', callback);
          return () => events.delete('change');
        },
      },
    },
    realtime: {
      reconnected: {
        on: (callback: (...args: unknown[]) => void) => {
          events.set('reconnect', callback);
          return () => events.delete('reconnect');
        },
      },
    },
  },
}));
const identity = { ownerId: 'owner', frameId: 'root-frame', frameIncarnationId: 'root-inc', rootFrameId: 'root-frame' };
const snapshot = (revision = 0, policy = 'allow', live = false) => ({
  schema: 'x-science.session-delegation-policy.v1',
  scope: { root_frame_id: 'root-frame', root_incarnation_id: 'root-inc' },
  policy,
  revision,
  has_live_delegated_attempts: live,
});
function mutation(command: Record<string, unknown>, current = snapshot(1, 'deny')) {
  return {
    schema: 'x-science.session-delegation-mutation.v1',
    receipt: {
      schema: 'x-science.session-delegation-receipt.v1',
      command,
      applied_revision: Number(command.expected_revision) + 1,
      applied_policy: command.desired_policy,
      applied_at: '2026-10-19T00:00:00Z',
    },
    snapshot: current,
    replayed: true,
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { resolve, promise };
}
const posts = () => httpRequest.mock.calls.filter(([method]) => method === 'POST');
let disposers: Array<() => void> = [];
async function ready() {
  const owner = new SessionDelegationPolicyController(identity);
  disposers.push(owner.attach());
  await owner.refresh();
  return owner;
}
describe('session delegation policy owner', () => {
  beforeEach(() => {
    httpRequest.mockReset();
    events.clear();
    setRendererAccountOwner(null);
    setRendererAccountOwner('owner');
    vi.stubGlobal('window', new EventTarget());
    vi.stubGlobal('document', Object.assign(new EventTarget(), { visibilityState: 'visible' }));
    httpRequest.mockResolvedValue(snapshot());
  });
  afterEach(() => {
    disposers.toReversed().forEach((dispose) => dispose());
    disposers = [];
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
  });
  it('loads default allow once and keeps bootstrap distinct from a policy command', async () => {
    const owner = await ready();
    expect(owner.getSnapshot().snapshot?.policy).toBe('allow');
    expect(httpRequest).toHaveBeenCalledTimes(1);
    await owner.change(true);
    expect(posts()).toHaveLength(0);
  });
  it('deduplicates synchronous clicks without optimistically flipping the policy', async () => {
    const owner = await ready(),
      pending = deferred<unknown>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const first = owner.change(false),
      second = owner.change(false);
    expect(first).toBe(second);
    expect(posts()).toHaveLength(1);
    expect(owner.getSnapshot()).toMatchObject({ writing: true, snapshot: { policy: 'allow' } });
    pending.resolve(mutation(posts()[0][2]));
    await first;
    expect(owner.getSnapshot()).toMatchObject({ writing: false, snapshot: { policy: 'deny', revision: 1 } });
  });
  it('deduplicates a refresh requested synchronously by a loading-state subscriber', async () => {
    const owner = await ready();
    httpRequest.mockClear();
    let entered = false;
    const unsubscribe = owner.subscribe(() => {
      if (owner.getSnapshot().loading && !entered) {
        entered = true;
        void owner.refresh();
      }
    });
    const reading = owner.refresh();
    expect(httpRequest).toHaveBeenCalledTimes(1);
    await reading;
    unsubscribe();
  });
  it('retries a lost acknowledgement with the identical command and accepts current snapshot over old receipt', async () => {
    const owner = await ready();
    httpRequest.mockRejectedValueOnce(new TypeError('connection lost'));
    await owner.change(false);
    const original = posts()[0][2];
    expect(owner.getSnapshot()).toMatchObject({ error: 'uncertain', retryable: true, snapshot: { policy: 'allow' } });
    httpRequest.mockResolvedValueOnce(mutation(original, snapshot(2, 'allow', true)));
    await owner.retry();
    expect(posts()[1][2]).toBe(original);
    expect(owner.getSnapshot()).toMatchObject({
      error: null,
      snapshot: { revision: 2, policy: 'allow', has_live_delegated_attempts: true },
    });
  });
  it('reads a known revision conflict without rebinding the rejected intent', async () => {
    const owner = await ready();
    httpRequest.mockRejectedValueOnce(
      new BackendHttpError({
        method: 'POST',
        path: '/',
        status: 409,
        body: { code: 'SESSION_DELEGATION_REVISION_CONFLICT', snapshot: snapshot(2) },
      })
    );
    await owner.change(false);
    expect(owner.getSnapshot()).toMatchObject({ error: 'conflict', retryable: false, snapshot: { revision: 2 } });
    httpRequest.mockResolvedValueOnce(snapshot(2));
    await owner.retry();
    expect(posts()).toHaveLength(1);
    expect(posts()[0][2].expected_revision).toBe(0);
    expect(owner.getSnapshot().error).toBeNull();
  });
  it('destroys account-switched late reads and their original request signal', async () => {
    const pending = deferred<unknown>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const owner = new SessionDelegationPolicyController(identity);
    disposers.push(owner.attach());
    const signal = httpRequest.mock.calls[0][3].signal as AbortSignal;
    setRendererAccountOwner('other-owner');
    expect(signal.aborted).toBe(true);
    pending.resolve(snapshot(4, 'deny'));
    await pending.promise;
    await Promise.resolve();
    expect(owner.getSnapshot()).toMatchObject({ snapshot: null, available: false });
    await owner.change(false);
    expect(posts()).toHaveLength(0);
  });
  it('treats stale root events as read hints and keeps revisions monotonic while retaining live updates', async () => {
    httpRequest.mockResolvedValueOnce(snapshot(3));
    const owner = await ready();
    httpRequest.mockResolvedValueOnce(snapshot(1, 'deny'));
    events.get('change')?.({
      action: 'updated',
      source: 'xscience',
      conversation_id: 'root-frame',
      root_frame_id: 'root-frame',
      delegation_policy_changed: true,
      root_incarnation_id: 'old-root',
      delegation_policy_revision: 1,
    });
    await vi.waitFor(() => expect(owner.getSnapshot().loading).toBe(false));
    expect(owner.getSnapshot().snapshot?.revision).toBe(3);
    httpRequest.mockResolvedValueOnce(snapshot(3, 'allow', true));
    window.dispatchEvent(new Event('focus'));
    await vi.waitFor(() => expect(owner.getSnapshot().snapshot?.has_live_delegated_attempts).toBe(true));
    httpRequest.mockResolvedValueOnce(snapshot(4, 'deny', true));
    events.get('reconnect')?.();
    await vi.waitFor(() => expect(owner.getSnapshot().snapshot?.revision).toBe(4));
    expect(owner.getSnapshot().snapshot?.policy).toBe('deny');
    expect(posts()).toHaveLength(0);
  });
  it('keeps an unacknowledged command across close and reattach without applying its late result', async () => {
    const owner = await ready(),
      pending = deferred<unknown>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const write = owner.change(false),
      original = posts()[0][2];
    disposers.pop()?.();
    const detach = owner.attach();
    disposers.push(detach);
    pending.resolve(mutation(original));
    await write;
    expect(owner.getSnapshot()).toMatchObject({ error: 'uncertain', retryable: true, snapshot: { policy: 'allow' } });
    httpRequest.mockResolvedValueOnce(mutation(original));
    await owner.retry();
    expect(posts()[1][2]).toBe(original);
    expect(owner.getSnapshot().snapshot?.policy).toBe('deny');
  });
});
