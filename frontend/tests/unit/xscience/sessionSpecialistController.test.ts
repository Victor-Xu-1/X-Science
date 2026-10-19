import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { SessionSpecialistController } from '@/renderer/services/runtime/sessionSpecialistController';
import {
  specialistIdentity as identity,
  specialistMutation,
  specialistScope,
  specialistSnapshot,
} from './sessionSpecialistFixtures';
const { httpRequest, events } = vi.hoisted(() => ({
  httpRequest: vi.fn(),
  events: new Map<string, (v?: unknown) => void>(),
}));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest,
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      listChanged: {
        on: (fn: (v: unknown) => void) => {
          events.set('event', fn);
          return () => events.delete('event');
        },
      },
    },
    realtime: {
      reconnected: {
        on: (fn: () => void) => {
          events.set('reconnect', fn);
          return () => events.delete('reconnect');
        },
      },
    },
  },
}));
let releases: Array<() => void> = [];
const posts = () => httpRequest.mock.calls.filter(([method]) => method === 'POST');
const deferred = () => {
  let resolve!: (v: unknown) => void;
  const promise = new Promise<unknown>((done) => {
    resolve = done;
  });
  return { resolve, promise };
};
async function ready() {
  const owner = new SessionSpecialistController(identity);
  releases.push(owner.attach());
  await owner.refresh();
  return owner;
}
beforeEach(() => {
  httpRequest.mockReset();
  events.clear();
  setRendererAccountOwner(null);
  setRendererAccountOwner('owner');
  vi.stubGlobal('window', new EventTarget());
  httpRequest.mockResolvedValue(specialistSnapshot());
});
afterEach(() => {
  releases.toReversed().forEach((release) => release());
  releases = [];
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
});
it('deduplicates synchronous writes and subscriber refresh reentry without an optimistic selection', async () => {
  const owner = await ready();
  let reentered = false;
  const unsubscribe = owner.subscribe(() => {
    if (owner.getSnapshot().loading && !reentered) {
      reentered = true;
      void owner.refresh();
    }
  });
  httpRequest.mockClear();
  await owner.refresh();
  expect(httpRequest).toHaveBeenCalledTimes(1);
  unsubscribe();
  const pending = deferred();
  httpRequest.mockReturnValueOnce(pending.promise);
  const first = owner.change('AGENT_B'),
    second = owner.change('AGENT_B');
  expect(first).toBe(second);
  await Promise.resolve();
  expect(posts()).toHaveLength(1);
  expect(owner.getSnapshot().snapshot?.applied.selection_name).toBe('AGENT_A');
  expect(() => owner.capture()).toThrow();
  pending.resolve(specialistMutation(posts()[0][2]));
  await first;
  expect(owner.capture()).toMatchObject({ reference: { revision: 1 }, targetAgent: 'AGENT_B' });
});
it('retries a lost ACK with the original command and takes current readback over old receipt', async () => {
  const owner = await ready();
  httpRequest.mockRejectedValueOnce(new TypeError('lost'));
  await owner.change('AGENT_B');
  const command = posts()[0][2];
  expect(owner.getSnapshot()).toMatchObject({ error: 'uncertain', retryable: true });
  httpRequest.mockResolvedValueOnce(specialistMutation(command, specialistSnapshot(2, 'AGENT_C')));
  await owner.retry();
  expect(posts()[1][2]).toBe(command);
  expect(owner.capture().targetAgent).toBe('AGENT_C');
});
it('reads a known conflict without automatically rebasing rejected intent', async () => {
  const owner = await ready();
  httpRequest.mockRejectedValueOnce(
    new BackendHttpError({
      method: 'POST',
      path: '/',
      status: 409,
      body: { code: 'SESSION_SPECIALIST_REVISION_CONFLICT' },
    })
  );
  httpRequest.mockResolvedValueOnce(specialistSnapshot(2, 'AGENT_C'));
  await owner.change('AGENT_B');
  await vi.waitFor(() => expect(owner.getSnapshot().loading).toBe(false));
  expect(posts()).toHaveLength(1);
  expect(() => owner.capture()).toThrow();
  const old = posts()[0][2];
  httpRequest.mockImplementationOnce(async (_method, _path, command) =>
    specialistMutation(command, specialistSnapshot(3, 'AGENT_B'))
  );
  await owner.change('AGENT_B');
  expect(posts()).toHaveLength(2);
  expect(posts()[1][2].command_id).not.toBe(old.command_id);
  expect(posts()[1][2].expected_revision).toBe(2);
  expect(owner.capture().targetAgent).toBe('AGENT_B');
});
it('rejects late account reads and late close results while retaining the same uncertain command', async () => {
  const owner = await ready(),
    pending = deferred();
  httpRequest.mockReturnValueOnce(pending.promise);
  const write = owner.change('AGENT_B');
  await Promise.resolve();
  const command = posts()[0][2];
  releases.pop()?.();
  releases.push(owner.attach());
  pending.resolve(specialistMutation(command));
  await write;
  expect(owner.getSnapshot().error).toBe('uncertain');
  httpRequest.mockImplementation(async (method, _path, body) =>
    method === 'POST' ? specialistMutation(body) : specialistSnapshot()
  );
  await owner.retry();
  expect(posts()[1][2]).toBe(command);
  const later = deferred();
  httpRequest.mockReturnValueOnce(later.promise);
  const read = owner.refresh();
  await Promise.resolve();
  setRendererAccountOwner('other');
  later.resolve(specialistSnapshot(8, 'AGENT_Z'));
  await read;
  expect(owner.getSnapshot()).toMatchObject({ snapshot: null, available: false });
});
it('treats matching events as invalidation and rejects stale frame, root and revision hints', async () => {
  httpRequest.mockResolvedValueOnce(specialistSnapshot(3, 'AGENT_B'));
  const owner = await ready();
  httpRequest.mockClear();
  events.get('event')?.({
    source: 'xscience',
    action: 'updated',
    specialist_changed: true,
    conversation_id: 'frame',
    frame_incarnation_id: 'old',
  });
  events.get('event')?.({
    source: 'xscience',
    action: 'updated',
    specialist_changed: true,
    conversation_id: 'frame',
    frame_incarnation_id: 'inc',
    root_incarnation_id: 'old',
  });
  events.get('event')?.({
    source: 'xscience',
    action: 'updated',
    specialist_changed: true,
    conversation_id: 'frame',
    frame_incarnation_id: 'inc',
    specialist_revision: 1,
  });
  expect(httpRequest).not.toHaveBeenCalled();
  httpRequest.mockResolvedValueOnce(specialistSnapshot(3, 'AGENT_B', { has_live_attempt: true }));
  window.dispatchEvent(new Event('focus'));
  await vi.waitFor(() => expect(owner.getSnapshot().snapshot?.has_live_attempt).toBe(true));
  httpRequest.mockResolvedValueOnce(specialistSnapshot(4, 'AGENT_C'));
  events.get('reconnect')?.();
  await vi.waitFor(() => expect(owner.getSnapshot().snapshot?.revision).toBe(4));
  expect(posts()).toHaveLength(0);
});
it('keeps ordinary legacy sends available but rejects a prepared reference for a different branch', async () => {
  const owner = await ready();
  expect(owner.capture()).toEqual({});
  httpRequest.mockResolvedValueOnce(specialistSnapshot(1, 'AGENT_B', { branch: { id: 'new', generation: 2 } }));
  await owner.refresh();
  expect(() => owner.capture()).toThrow();
  expect(owner.getSnapshot().snapshot?.prepared_branch.id).toBe('branch-a');
  expect(owner.getSnapshot().snapshot?.scope).toEqual(specialistScope);
});
