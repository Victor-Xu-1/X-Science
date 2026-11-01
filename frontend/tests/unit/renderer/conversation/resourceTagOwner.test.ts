import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeResourceTagReceipt,
  type ResourceTagCommand,
  type ResourceTagReceipt,
  type ResourceTagSnapshot,
} from '@/common/adapter/resourceTags';
import {
  ResourceTagOwner,
  getResourceTagOwner,
  type ResourceTagOwnerOptions,
} from '@/renderer/services/runtime/resourceTagOwner';
import {
  createResourceTagMutationQueue,
  decodeResourceTagMutationQueue,
  decodeResourceTagQueueEntry,
  projectResourceTagTarget,
} from '@/renderer/services/runtime/resourceTagMutationQueue';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const op = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const target = { resource_type: 'literature.item', resource_id: 'item-1' } as const;
const favorite = { id: 'tag-favorite', system_key: 'favorite', created_at: 0, updated_at: 0 } as const;
const tag = {
  id: 'tag-a',
  name: 'Research',
  icon_key: 'tag',
  color_key: 'blue',
  created_at: 1,
  updated_at: 2,
} as const;
const snapshot = (revision = 2): ResourceTagSnapshot => ({
  schema: 'x-science.resource-tags.v1',
  revision,
  resource_types: ['literature.item'],
  tags: [favorite, tag],
  assignments: [],
});
const command = (n = 1, assigned = true) =>
  ({ kind: 'assignment', operation_id: op(n), ...target, tag_id: tag.id, assigned }) as const;
const entry = (n = 1, assigned = true) =>
  decodeResourceTagQueueEntry({
    group_id: op(n),
    command: command(n, assigned),
    continuation: null,
    phase: 'ready',
    minimum_revision: 0,
    rejection: null,
  });
const receipt = (input: ResourceTagCommand): ResourceTagReceipt => {
  const state = snapshot();
  const changed =
    input.kind === 'create'
      ? {
          ...state,
          tags: [favorite, { ...tag, name: input.name.trim(), icon_key: input.icon_key, color_key: input.color_key }],
        }
      : input.kind === 'assignment' && input.assigned
        ? {
            ...state,
            assignments: [
              {
                tag_id: input.tag_id,
                resource_type: input.resource_type,
                resource_id: input.resource_id,
                created_at: 2,
              },
            ],
          }
        : state;
  return decodeResourceTagReceipt(
    {
      schema: 'x-science.resource-tag-receipt.v1',
      operation_id: input.operation_id,
      action: input.kind,
      tag_id: input.kind === 'create' ? tag.id : 'tag_id' in input ? input.tag_id : undefined,
      snapshot: changed,
    },
    input
  );
};
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
let values: Map<string, string>;
let sequence: number;
const storage = () => ({
  getItem: (key: string) => values.get(key) ?? null,
  setItem: (key: string, value: string) => {
    values.set(key, value);
  },
  removeItem: (key: string) => {
    values.delete(key);
  },
});
const owners: ResourceTagOwner[] = [];
const make = (options: ResourceTagOwnerOptions = {}, ownerId = 'owner-a') => {
  const owner = new ResourceTagOwner(ownerId, {
    storage,
    events: null,
    operationId: () => op(++sequence),
    load: async () => snapshot(),
    run: async (input) => receipt(input),
    ...options,
  });
  owners.push(owner);
  return owner;
};
function locks() {
  const tails = new Map<string, Promise<unknown>>();
  return {
    request: (key: string, options: { signal: AbortSignal }, callback: () => unknown) => {
      const previous = tails.get(key) ?? Promise.resolve();
      const task = previous
        .catch((): void => undefined)
        .then(() => {
          options.signal.throwIfAborted();
          return callback();
        });
      tails.set(
        key,
        task.catch((): void => undefined)
      );
      return new Promise<unknown>((resolve, reject) => {
        const abort = () => reject(options.signal.reason);
        options.signal.addEventListener('abort', abort, { once: true });
        task.then(resolve, reject).finally(() => options.signal.removeEventListener('abort', abort));
      });
    },
  };
}
beforeEach(() => {
  values = new Map();
  sequence = 0;
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('owner-a');
  vi.stubGlobal('navigator', { locks: locks() });
});
afterEach(() => {
  owners.splice(0).forEach((owner) => owner.stop());
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('resource tag stable account queue', () => {
  it('strictly restores original immutable payloads, owner and phase without catalog snapshots', async () => {
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    const original = entry();
    await queue.append(original);
    const raw = JSON.parse(values.get(queue.key)!);
    expect(queue.read()?.entries[0]).toEqual(original);
    expect(() => decodeResourceTagMutationQueue(raw, 'owner-b')).toThrow();
    expect(() => decodeResourceTagMutationQueue({ ...raw, snapshot: snapshot() }, 'owner-a')).toThrow();
    expect(() => decodeResourceTagMutationQueue({ ...raw, entries: [original, original] }, 'owner-a')).toThrow();
    expect(() =>
      decodeResourceTagQueueEntry({
        ...original,
        phase: 'readback',
        rejection: { status: 503, code: 'RESOURCE_TAG_UNAVAILABLE' },
      })
    ).toThrow();
    expect(Object.isFrozen(queue.read()?.entries[0].command)).toBe(true);
  });
  it('serializes concurrent append and settles only the exact head while preserving tails', async () => {
    const a = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    const b = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    await Promise.all([a.append(entry(1)), b.append(entry(2))]);
    expect(a.read()?.entries.map((item) => item.group_id)).toEqual([op(1), op(2)]);
    expect(await a.replaceHead(entry(1), null)).toBe(true);
    expect(await b.replaceHead(entry(1), null)).toBe(false);
    expect(a.read()?.entries.map((item) => item.group_id)).toEqual([op(2)]);
  });
  it('projects desired booleans in FIFO order, excludes rejected writes and stops recovery spinners', () => {
    const create = decodeResourceTagQueueEntry({
      ...entry(3),
      command: { kind: 'create', operation_id: op(3), name: 'New', icon_key: 'tag', color_key: 'blue' },
      continuation: { target, operation_id: op(4) },
    });
    const rejected = decodeResourceTagQueueEntry({
      ...entry(5),
      phase: 'readback',
      rejection: { status: 409, code: 'RESOURCE_TAG_CONFLICT' },
    });
    expect(projectResourceTagTarget(snapshot(), [entry(1), entry(2, false), rejected, create], target, false)).toEqual({
      assignedIds: [],
      pendingTagIds: [],
      creating: true,
    });
    expect(projectResourceTagTarget(snapshot(), [entry(1)], target, true)).toEqual({
      assignedIds: [tag.id],
      pendingTagIds: [tag.id],
      creating: false,
    });
    expect(
      projectResourceTagTarget(snapshot(), [entry(1)], { ...target, resource_id: 'other' }, true).pendingTagIds
    ).toEqual([]);
  });
});

describe('resource tag single account owner', () => {
  it('accepts concurrent checkboxes durably before FIFO send and waits for canonical GET', async () => {
    const first = deferred<ResourceTagReceipt>(),
      reads = deferred<ResourceTagSnapshot>();
    const run = vi.fn(async (input) => receipt(input as ResourceTagCommand));
    run.mockImplementationOnce(() => first.promise);
    const load = vi.fn(async () => snapshot());
    load.mockImplementationOnce(() => reads.promise);
    const owner = make({ run, load });
    const one = owner.assign(target, tag.id, true),
      two = owner.assign(target, tag.id, false);
    await vi.waitFor(() => expect(owner.getSnapshot().pending).toHaveLength(2));
    expect(run).toHaveBeenCalledTimes(1);
    expect(values.size).toBe(1);
    first.resolve(receipt(command(1)));
    await vi.waitFor(() => expect(load).toHaveBeenCalledOnce());
    expect(owner.getSnapshot().snapshot).toBeNull();
    expect(owner.getSnapshot().pending).toHaveLength(2);
    reads.resolve(snapshot());
    await Promise.all([one, two]);
    expect(run.mock.calls.map(([input]) => (input as ResourceTagCommand).operation_id)).toEqual([op(1), op(2)]);
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: 'idle', pending: [] });
    expect(values.size).toBe(0);
  });
  it('preserves a lost ACK across owner refresh and explicitly replays the original key once', async () => {
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    run.mockRejectedValueOnce(new Error('lost response'));
    const owner = make({ run });
    await expect(owner.assign(target, tag.id, true)).rejects.toThrow('lost response');
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: 'recovery' });
    const frozen = owner.getSnapshot().pending[0];
    owner.stop();
    const restored = make({ run });
    await restored.retry();
    expect(run.mock.calls.map(([input]) => input.operation_id)).toEqual([
      frozen.command.operation_id,
      frozen.command.operation_id,
    ]);
    expect(values.size).toBe(0);
    expect(restored.getSnapshot().pending).toEqual([]);
  });
  it('never lets a late ACK settle or rewrite a different original head', async () => {
    const waiting = deferred<ResourceTagReceipt>();
    const run = vi.fn(async (_input: ResourceTagCommand) => waiting.promise);
    run.mockImplementationOnce(() => waiting.promise).mockRejectedValueOnce(new Error('verification lost'));
    const owner = make({ run });
    const result = owner.assign(target, tag.id, true).catch((error) => error);
    await vi.waitFor(() => expect(owner.getSnapshot().pending[0]?.phase).toBe('unknown'));
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    await queue.replaceHead(queue.read()!.entries[0], entry(99, false));
    waiting.resolve(receipt(command(1)));
    expect(await result).toMatchObject({ message: expect.stringContaining('verification lost') });
    expect(queue.read()?.entries).toEqual([entry(99, false)]);
    expect(owner.getSnapshot().snapshot).toBeNull();
  });
  it('atomically continues a created real tag through assignment failure and refresh without another create', async () => {
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    run.mockImplementationOnce(async (input) => receipt(input)).mockRejectedValueOnce(new Error('assignment ACK lost'));
    const owner = make({ run });
    await expect(owner.createAndAssign(target, 'Research')).rejects.toThrow('assignment ACK lost');
    expect(owner.getSnapshot().pending[0]).toMatchObject({
      group_id: op(1),
      command: { kind: 'assignment', operation_id: op(2), tag_id: tag.id },
      phase: 'unknown',
      continuation: { target, operation_id: op(2) },
    });
    owner.stop();
    const restored = make({ run });
    await restored.retry();
    expect(run.mock.calls.map(([input]) => [input.kind, input.operation_id])).toEqual([
      ['create', op(1)],
      ['assignment', op(2)],
      ['assignment', op(2)],
    ]);
    expect(restored.getSnapshot().pending).toEqual([]);
  });
  it('keeps confirmed view and readback intent on GET failure, then retries only GET with finite busy', async () => {
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    const load = vi.fn(async () => snapshot());
    load.mockResolvedValueOnce(snapshot(1)).mockRejectedValueOnce(new Error('GET offline'));
    const owner = make({ run, load });
    await owner.reload();
    await expect(owner.assign(target, tag.id, true)).rejects.toThrow('GET offline');
    expect(owner.getSnapshot()).toMatchObject({
      snapshot: { revision: 1 },
      busy: false,
      status: 'recovery',
      pending: [{ phase: 'readback' }],
    });
    await owner.retry();
    expect(run).toHaveBeenCalledOnce();
    expect(owner.getSnapshot().snapshot?.revision).toBe(2);
  });
  it('ends known coded rejection after canonical read and does not strand unrelated queued work', async () => {
    const first = deferred<ResourceTagReceipt>();
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    run.mockImplementationOnce(() => first.promise);
    const owner = make({ run });
    const one = owner.assign(target, tag.id, true).catch((error) => error),
      two = owner.assign(target, tag.id, false);
    await vi.waitFor(() => expect(owner.getSnapshot().pending).toHaveLength(2));
    first.reject(
      new BackendHttpError({
        method: 'PUT',
        path: '/api/tags/assignments',
        status: 409,
        body: { code: 'RESOURCE_TAG_CONFLICT' },
      })
    );
    expect(await one).toMatchObject({ code: 'RESOURCE_TAG_CONFLICT' });
    await two;
    expect(owner.getSnapshot()).toMatchObject({ busy: false, pending: [], error: { code: 'RESOURCE_TAG_CONFLICT' } });
    expect(run).toHaveBeenCalledTimes(2);
  });
  it('keeps untyped4xx, invalid ACK and5xx uncertain without loops or replacement keys', async () => {
    for (const error of [
      new BackendHttpError({ method: 'PUT', path: '/api/tags', status: 409, body: {} }),
      new BackendHttpError({
        method: 'PUT',
        path: '/api/tags',
        status: 503,
        body: { code: 'RESOURCE_TAG_UNAVAILABLE' },
      }),
      new Error('Invalid resource tag contract'),
    ]) {
      values.clear();
      const run = vi.fn(async () => {
        throw error;
      });
      const owner = make({ run });
      await expect(owner.assign(target, tag.id, true)).rejects.toBe(error);
      expect(run).toHaveBeenCalledOnce();
      expect(owner.getSnapshot()).toMatchObject({ busy: false, pending: [{ phase: 'unknown' }] });
      owner.stop();
    }
  });
  it('sends nothing when durable append or browser lock fails', async () => {
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    const owner = make({
      run,
      storage: () => ({
        ...storage(),
        setItem: () => {
          throw new Error('storage denied');
        },
      }),
    });
    await expect(owner.assign(target, tag.id, true)).rejects.toThrow('storage denied');
    expect(run).not.toHaveBeenCalled();
    expect(owner.getSnapshot().pending).toEqual([]);
    vi.stubGlobal('navigator', {});
    await expect(make({ run }).assign(target, tag.id, false)).rejects.toThrow('lock unavailable');
    expect(run).not.toHaveBeenCalled();
  });
  it('fences an account switch and preserves its durable original queue', async () => {
    const waiting = deferred<ResourceTagReceipt>();
    const owner = make({ run: () => waiting.promise });
    const result = owner.assign(target, tag.id, true).catch((error) => error);
    await vi.waitFor(() => expect(owner.getSnapshot().pending[0]?.phase).toBe('unknown'));
    setRendererAccountOwner('owner-b');
    owner.stop();
    waiting.resolve(receipt(command(1)));
    expect(await result).toMatchObject({ message: expect.stringContaining('account changed') });
    const next = make({}, 'owner-b');
    await next.reload();
    expect(next.getSnapshot().pending).toEqual([]);
    expect(values.size).toBe(1);
  });
  it('rejects stale reads but accepts equal-revision canonical permission changes', async () => {
    const old = deferred<ResourceTagSnapshot>();
    const load = vi.fn(async () => snapshot(4));
    load.mockImplementationOnce(() => old.promise);
    const owner = make({ load });
    const stale = owner.reload().catch((error) => error);
    await owner.reload();
    old.resolve(snapshot(3));
    expect(await stale).toMatchObject({ message: expect.stringContaining('superseded') });
    expect(owner.getSnapshot().snapshot?.revision).toBe(4);
    expect(owner.getSnapshot().error).toBeNull();
    load.mockResolvedValueOnce({ ...snapshot(4), tags: [favorite] });
    await owner.reload();
    expect(owner.getSnapshot().snapshot?.tags).toEqual([favorite]);
  });
  it('uses one sender across owners and verifies elsewhere completion with one bounded original-key replay', async () => {
    const first = deferred<ResourceTagReceipt>();
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    run.mockImplementationOnce(() => first.promise);
    const a = make({ run }),
      b = make({ run });
    const one = a.assign(target, tag.id, true),
      two = b.assign(target, tag.id, false);
    await vi.waitFor(() => expect(a.getSnapshot().pending).toHaveLength(2));
    await vi.waitFor(() => expect(values.values().next().value).toContain(op(2)));
    expect(run).toHaveBeenCalledOnce();
    first.resolve(receipt(command(1)));
    await Promise.all([one, two]);
    expect(run.mock.calls.map(([input]) => input.operation_id)).toEqual([op(1), op(2), op(2)]);
    expect(a.getSnapshot().pending).toEqual([]);
    expect(b.getSnapshot().pending).toEqual([]);
  });
  it('leaves accepted work durable after bounded sender-lock acquisition timeout', async () => {
    vi.useFakeTimers();
    const first = deferred<ResourceTagReceipt>();
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    run.mockImplementationOnce(() => first.promise);
    const a = make({ run }),
      b = make({ run });
    const one = a.assign(target, tag.id, true),
      two = b.assign(target, tag.id, false).catch((error) => error);
    await vi.advanceTimersByTimeAsync(1);
    expect(run).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(5_001);
    expect(await two).toMatchObject({ message: expect.stringContaining('lock timed out') });
    expect(b.getSnapshot()).toMatchObject({ busy: false, status: 'recovery', pending: [{}, { group_id: op(2) }] });
    expect(run).toHaveBeenCalledOnce();
    first.resolve(receipt(command(1)));
    await one;
    expect(run).toHaveBeenCalledTimes(2);
    expect(values.size).toBe(0);
  });
  it('restores before today metadata reads and does not restart accepted work on repeated start', async () => {
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    await queue.append(decodeResourceTagQueueEntry({ ...entry(), phase: 'unknown' }));
    const order: string[] = [];
    const run = vi.fn(async (input: ResourceTagCommand) => {
      order.push(`send:${input.operation_id}`);
      return receipt(input);
    });
    const owner = make({
      run,
      load: async () => {
        order.push('GET');
        return snapshot();
      },
    });
    owner.start();
    owner.start();
    await owner.retry();
    expect(order).toEqual([`send:${op(1)}`, 'GET']);
    expect(run).toHaveBeenCalledOnce();
  });
  it('uses storage events only for pending intent and canonical readback, never server confirmation', async () => {
    const events = new EventTarget(),
      load = vi.fn(async () => snapshot());
    const owner = make({ events, load });
    owner.start();
    await vi.waitFor(() => expect(owner.getSnapshot().snapshot).not.toBeNull());
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    await queue.append(decodeResourceTagQueueEntry({ ...entry(), phase: 'unknown' }));
    const event = new Event('storage');
    Object.defineProperty(event, 'key', { value: queue.key });
    events.dispatchEvent(event);
    await vi.waitFor(() => expect(owner.getSnapshot().pending).toHaveLength(1));
    expect(owner.getSnapshot().snapshot?.assignments).toEqual([]);
    await queue.replaceHead(queue.read()!.entries[0], null);
    const later = deferred<ResourceTagSnapshot>();
    load.mockImplementationOnce(() => later.promise);
    events.dispatchEvent(event);
    expect(owner.getSnapshot().snapshot?.revision).toBe(2);
    later.resolve(snapshot(3));
    await vi.waitFor(() => expect(owner.getSnapshot().snapshot?.revision).toBe(3));
  });
  it('shares the scope singleton across consumers and account reset aborts actual transport', async () => {
    vi.stubGlobal('localStorage', storage());
    let signal: AbortSignal | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_url: RequestInfo | URL, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            signal = init?.signal ?? undefined;
            signal?.addEventListener('abort', () => reject(signal?.reason), { once: true });
          })
      )
    );
    const owner = getResourceTagOwner();
    expect(getResourceTagOwner()).toBe(owner);
    owner.start();
    expect(signal?.aborted).toBe(false);
    setRendererAccountOwner('owner-b');
    expect(signal?.aborted).toBe(true);
    expect(getResourceTagOwner()).not.toBe(owner);
  });
  it('replays a fresh-owner persisted readback before treating it as an accepted operation', async () => {
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    await queue.append(decodeResourceTagQueueEntry({ ...entry(), phase: 'readback' }));
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    const owner = make({ run });
    await owner.retry();
    expect(run).toHaveBeenCalledOnce();
    expect(run.mock.calls[0][0]).toEqual(command(1));
    expect(owner.getSnapshot().pending).toEqual([]);
  });
  it('does not trust a fresh-owner persisted claimed rejection without the original-key response', async () => {
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    await queue.append(
      decodeResourceTagQueueEntry({
        ...entry(),
        phase: 'readback',
        rejection: { status: 409, code: 'RESOURCE_TAG_CONFLICT' },
      })
    );
    const run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    const owner = make({ run });
    await expect(owner.retry()).resolves.toBeUndefined();
    expect(run).toHaveBeenCalledOnce();
    expect(owner.getSnapshot()).toMatchObject({ pending: [], error: null });
  });
  it('retries a failed canonical read with an empty durable queue instead of only clearing its error', async () => {
    const load = vi.fn(async () => snapshot());
    load.mockRejectedValueOnce(new Error('initial GET failed'));
    const owner = make({ load });
    await expect(owner.reload()).rejects.toThrow('initial GET failed');
    expect(owner.getSnapshot().snapshot).toBeNull();
    await owner.retry();
    expect(load).toHaveBeenCalledTimes(2);
    expect(owner.getSnapshot()).toMatchObject({ snapshot: { revision: 2 }, error: null, busy: false });
  });
  it('does not reinterpret an invalid durable queue as an empty read retry', async () => {
    const queue = createResourceTagMutationQueue({ ownerId: 'owner-a', current: () => true, storage });
    values.set(queue.key, JSON.stringify({ schema: 'wrong', owner_id: 'owner-a', entries: [] }));
    const load = vi.fn(async () => snapshot()),
      run = vi.fn(async (input: ResourceTagCommand) => receipt(input));
    const owner = make({ load, run });
    await expect(owner.retry()).rejects.toThrow('Invalid resource tag mutation queue');
    expect(owner.getSnapshot().error?.message).toContain('Invalid resource tag mutation queue');
    expect(load).not.toHaveBeenCalled();
    expect(run).not.toHaveBeenCalled();
    expect(values.has(queue.key)).toBe(true);
  });
});
