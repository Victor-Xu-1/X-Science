import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  ContextCompactionController,
  compactionDependencies,
} from '@/renderer/services/compaction/ContextCompactionController';
import {
  decodeCompactionReceipt,
  decodeCompactionSnapshot,
} from '@/renderer/services/compaction/contextCompactionContract';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import type { CompactionReceipt, CompactionRequest } from '@/renderer/services/compaction/contextCompactionTypes';
import {
  manualCompactionIdentity as identity,
  manualCompactionRequest,
  manualCompactionReceipt,
  manualCompactionSnapshot,
} from './manualContextCompactionFixtures';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
const receipt = (patch: Partial<CompactionReceipt> = {}) =>
  decodeCompactionReceipt({ ...manualCompactionReceipt(), ...patch }, identity);
const snapshot = (operation: CompactionReceipt | null = null) =>
  decodeCompactionSnapshot(
    manualCompactionSnapshot(operation as ReturnType<typeof manualCompactionReceipt> | null),
    identity
  );
const releases: Array<() => void> = [];
function setup(operation: CompactionReceipt | null = null) {
  let invalidate = () => {};
  const releaseObservation = vi.fn();
  const dependencies = {
    ...compactionDependencies,
    uuid: vi.fn(() => manualCompactionRequest.request_id),
    read: vi.fn(async () => snapshot(operation)),
    lookup: vi.fn(async (_identity: typeof identity, _request: CompactionRequest) => operation ?? receipt()),
    submit: vi.fn(async (_identity: typeof identity, request: CompactionRequest) => receipt({ request })),
    cancel: vi.fn(async (_identity: typeof identity, _request: CompactionRequest) =>
      receipt({
        state: 'cancelled',
        cancellation_status: 'cancelled',
        execution_observation: 'settled',
        can_submit_next_input: true,
      })
    ),
    observe: vi.fn((_identity: typeof identity, listener: () => void) => {
      invalidate = listener;
      return releaseObservation;
    }),
  };
  const controller = new ContextCompactionController(identity, dependencies);
  const release = controller.subscribe(() => {});
  releases.push(release);
  return { controller, dependencies, release, releaseObservation, invalidate: () => invalidate() };
}
beforeEach(() => {
  setRendererAccountOwner(identity.ownerId);
});
afterEach(() => {
  releases.splice(0).forEach((release) => release());
  setRendererAccountOwner(null);
});
describe('C11 composer-owned maintenance lifecycle', () => {
  it('does not wait for an optional bootstrap before handing an ordinary Stop back to its owner', async () => {
    const { controller, dependencies } = setup();
    const bootstrap = deferred<ReturnType<typeof snapshot>>();
    dependencies.read.mockReturnValueOnce(bootstrap.promise);
    await expect(controller.stop()).resolves.toBe(false);
    expect(controller.getSnapshot().request).toBeNull();
    expect(dependencies.cancel).not.toHaveBeenCalled();
    bootstrap.resolve(snapshot());
    await controller.refresh();
  });
  it('does not wait for a pending optional bootstrap before an ordinary prompt', async () => {
    const { controller, dependencies } = setup();
    const bootstrap = deferred<ReturnType<typeof snapshot>>();
    dependencies.read.mockReturnValueOnce(bootstrap.promise);
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    await expect(controller.prepareForPrompt()).resolves.toBe(true);
    expect(controller.getSnapshot().loading).toBe(true);
    expect(controller.getSnapshot().request).toBeNull();
    expect(controller.getSnapshot().waitingForPrompt).toBe(false);
    expect(dependencies.cancel).not.toHaveBeenCalled();
    expect(dependencies.submit).not.toHaveBeenCalled();
    bootstrap.resolve(snapshot());
    await controller.refresh();
  });
  it('keeps a failed optional bootstrap visible without gating an ordinary prompt', async () => {
    const { controller, dependencies } = setup();
    dependencies.read.mockRejectedValueOnce(
      new BackendHttpError({
        method: 'GET',
        path: '/api/conversations/compaction-child/context-compactions',
        status: 500,
        body: { code: 'CONTEXT_COMPACTION_STORAGE_UNAVAILABLE' },
      })
    );
    await controller.refresh();
    expect(controller.getSnapshot().error).toBe('read');
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    await expect(controller.prepareForPrompt()).resolves.toBe(true);
    expect(controller.getSnapshot().error).toBe('read');
    expect(controller.getSnapshot().waitingForPrompt).toBe(false);
    expect(dependencies.cancel).not.toHaveBeenCalled();
    expect(dependencies.submit).not.toHaveBeenCalled();
  });
  it('does not bypass a known uncertain request when its readback failed', async () => {
    const { controller, dependencies } = setup();
    await controller.refresh();
    dependencies.submit.mockRejectedValueOnce(new Error('lost ACK'));
    dependencies.lookup.mockRejectedValueOnce(new Error('readback failed'));
    await controller.start();
    const original = controller.getSnapshot().request;
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: false, preempted: false });
    expect(controller.getSnapshot().request).toBe(original);
    expect(controller.getSnapshot().uncertain).toBe('submit');
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it('returns terminal maintenance superseded by an ordinary input to the original send queue decision', async () => {
    const terminal = receipt({
      state: 'committed',
      execution_observation: 'unavailable',
      can_submit_next_input: false,
    });
    const { controller, dependencies } = setup(terminal);
    await controller.refresh();
    const ordinary = {
      ...snapshot(),
      target: { ...snapshot().target, input_revision: 9, through_publication_seq: 30 },
      availability: { available: false, reason: 'active' as const },
      operation: null,
    };
    dependencies.read.mockResolvedValue(ordinary);
    dependencies.lookup.mockResolvedValue(terminal);
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    expect(controller.retained).toBe(false);
    expect(controller.getSnapshot().receipt?.execution_observation).toBe('unavailable');
    expect(controller.getSnapshot().receipt?.can_submit_next_input).toBe(false);
    expect(dependencies.cancel).not.toHaveBeenCalled();
    expect(dependencies.submit).not.toHaveBeenCalled();
  });
  it('does not claim preemption for an already admission-safe historical receipt', async () => {
    const { controller, dependencies } = setup(
      receipt({ state: 'committed', execution_observation: 'unavailable', can_submit_next_input: true })
    );
    await controller.refresh();
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it('presents a new canonical maintenance instead of repeatedly looking up the retired old request', async () => {
    const terminal = receipt({
      state: 'cancelled',
      execution_observation: 'unavailable',
      can_submit_next_input: false,
    });
    const { controller, dependencies } = setup(terminal);
    await controller.refresh();
    const next = receipt({
      request: {
        ...terminal.request,
        request_id: '03030303-0303-4303-8303-030303030303',
        expected: { ...terminal.request.expected, input_revision: 9, through_publication_seq: 30 },
      },
      input_revision: 10,
      input_message_id: 'new-maintenance-input',
      state: 'compacting',
      execution_observation: 'active',
    });
    dependencies.read.mockResolvedValue({
      ...snapshot(next),
      target: { ...snapshot(next).target, input_revision: 10, through_publication_seq: 34 },
    });
    dependencies.lookup.mockResolvedValue(terminal);
    await controller.refresh();
    expect(controller.getSnapshot().request).toEqual(next.request);
    expect(controller.getSnapshot().receipt).toEqual(next);
    expect(controller.retained).toBe(true);
    dependencies.lookup.mockResolvedValue(next);
    await controller.refresh();
    expect(dependencies.lookup.mock.lastCall?.[1]).toEqual(next.request);
    expect(
      dependencies.lookup.mock.calls.filter(([, request]) => request.request_id === terminal.request.request_id)
    ).toHaveLength(1);
    expect(dependencies.cancel).not.toHaveBeenCalled();
    expect(dependencies.submit).not.toHaveBeenCalled();
  });
  it.each([
    { input_revision: 8 },
    { input_revision: 9, stream_uid: 'replacement-stream' },
    { input_revision: 9, stream_epoch: 3 },
  ])('does not retire an old request without same-scope newer admitted input: %j', async (target) => {
    const terminal = receipt({
      state: 'committed',
      execution_observation: 'unavailable',
      can_submit_next_input: false,
    });
    const { controller, dependencies } = setup(terminal);
    await controller.refresh();
    dependencies.read.mockResolvedValue({
      ...snapshot(),
      target: { ...snapshot().target, ...target },
      operation: null,
    });
    dependencies.lookup.mockResolvedValue(terminal);
    await controller.refresh();
    expect(controller.retained).toBe(true);
    expect(controller.getSnapshot().request).toEqual(terminal.request);
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it.each(['draining', 'active', 'unavailable'] as const)(
    'keeps unresolved %s maintenance protected without a terminal replacement proof',
    async (observation) => {
      const old = receipt({
        state: observation === 'unavailable' ? 'recovery_needed' : 'cancelled',
        execution_observation: observation,
      });
      const { controller, dependencies } = setup(old);
      await controller.refresh();
      dependencies.read.mockResolvedValue({
        ...snapshot(),
        target: { ...snapshot().target, input_revision: 9 },
        operation: null,
      });
      dependencies.lookup.mockResolvedValue(old);
      await controller.refresh();
      expect(controller.retained).toBe(true);
      expect(controller.getSnapshot().request).toEqual(old.request);
      expect(controller.getSnapshot().receipt?.execution_observation).toBe(observation);
      expect(dependencies.cancel).not.toHaveBeenCalled();
    }
  );
  it('keeps an uncertain original body when a later snapshot cannot prove its acceptance or retirement', async () => {
    const { controller, dependencies } = setup();
    await controller.refresh();
    dependencies.submit.mockRejectedValue(new Error('lost ACK'));
    dependencies.lookup.mockRejectedValue(new Error('no exact receipt'));
    await controller.start();
    const original = controller.getSnapshot().request;
    dependencies.read.mockResolvedValue({
      ...snapshot(),
      target: { ...snapshot().target, input_revision: 9 },
      operation: null,
    });
    await controller.refresh();
    expect(controller.getSnapshot().request).toBe(original);
    expect(controller.getSnapshot().uncertain).toBe('submit');
    expect(controller.retained).toBe(true);
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it('retires an exact cancelled-before-acceptance tombstone without inventing an admitted revision', async () => {
    const { controller, dependencies } = setup();
    await controller.refresh();
    const cancelled = decodeCompactionReceipt(
      {
        schema: 'x-science.context-compaction-receipt.v1',
        request: manualCompactionRequest,
        frame_id: identity.frameId,
        accepted: false,
        state: 'cancelled',
        cancellation_status: 'cancelled_before_acceptance',
        execution_observation: 'not_started',
        can_submit_next_input: false,
        replayed: true,
      },
      identity
    );
    dependencies.submit.mockRejectedValueOnce(new Error('cancelled before acceptance'));
    dependencies.lookup.mockResolvedValue(cancelled);
    await controller.start();
    dependencies.read.mockResolvedValue({
      ...snapshot(),
      target: { ...snapshot().target, input_revision: 8 },
      operation: null,
    });
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    expect(controller.getSnapshot().receipt?.accepted).toBe(false);
    expect(controller.getSnapshot().receipt?.input_revision).toBeUndefined();
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it('hands an exact preaccept tombstone back to the ordinary queue without any input revision advance', async () => {
    const { controller, dependencies } = setup();
    await controller.refresh();
    const cancelled = decodeCompactionReceipt(
      {
        schema: 'x-science.context-compaction-receipt.v1',
        request: manualCompactionRequest,
        frame_id: identity.frameId,
        accepted: false,
        state: 'cancelled',
        cancellation_status: 'cancelled_before_acceptance',
        execution_observation: 'not_started',
        can_submit_next_input: false,
        replayed: true,
      },
      identity
    );
    dependencies.submit.mockRejectedValueOnce(new Error('cancelled before acceptance'));
    dependencies.lookup.mockResolvedValue(cancelled);
    await controller.start();
    dependencies.read.mockResolvedValue({
      ...snapshot(),
      availability: { available: false, reason: 'queued' },
      operation: null,
    });
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    expect(controller.getSnapshot().snapshot?.target.input_revision).toBe(7);
    expect(controller.getSnapshot().receipt).toEqual(cancelled);
    expect(controller.retained).toBe(false);
    expect(dependencies.cancel).not.toHaveBeenCalled();
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
  });
  it('hands a canonical terminal receipt back to the ordinary queue without a newer admitted revision', async () => {
    const terminal = receipt({
      state: 'committed',
      boundary_event_id: 41,
      terminal_event_id: 42,
      execution_observation: 'unavailable',
      can_submit_next_input: false,
    });
    const { controller, dependencies } = setup(terminal);
    await controller.refresh();
    dependencies.read.mockResolvedValue({
      ...snapshot(terminal),
      availability: { available: false, reason: 'queued' },
    });
    dependencies.lookup.mockResolvedValue(terminal);
    await expect(controller.preparePromptIntent()).resolves.toEqual({ safe: true, preempted: false });
    expect(controller.getSnapshot().snapshot?.target.input_revision).toBe(terminal.input_revision);
    expect(controller.getSnapshot().receipt).toEqual(terminal);
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it('releases an already waiting prompt to the ordinary queue when exact cleanup leaves only queued work', async () => {
    const { controller, dependencies, invalidate } = setup(receipt());
    await controller.refresh();
    dependencies.cancel.mockResolvedValueOnce(
      receipt({
        state: 'cancelled',
        cancellation_status: 'requested',
        execution_observation: 'draining',
        terminal_event_id: 42,
      })
    );
    const prompt = controller.preparePromptIntent();
    await vi.waitFor(() => expect(controller.getSnapshot().waitingForPrompt).toBe(true));
    await vi.waitFor(() => expect(dependencies.cancel).toHaveBeenCalledTimes(1));
    const terminal = receipt({
      state: 'cancelled',
      cancellation_status: 'cancelled',
      terminal_event_id: 42,
      execution_observation: 'unavailable',
      can_submit_next_input: false,
    });
    dependencies.read.mockResolvedValue({
      ...snapshot(terminal),
      availability: { available: false, reason: 'queued' },
    });
    dependencies.lookup.mockResolvedValue(terminal);
    invalidate();
    await controller.refresh();
    await expect(prompt).resolves.toEqual({ safe: true, preempted: false });
    expect(controller.getSnapshot().receipt?.can_submit_next_input).toBe(false);
    expect(controller.getSnapshot().receipt?.execution_observation).toBe('unavailable');
  });
  it.each(['draining', 'active', 'unavailable'] as const)(
    'does not borrow queued availability to retire %s without the terminal proof',
    async (observation) => {
      const old = receipt({
        state: 'cancelled',
        execution_observation: observation,
        can_submit_next_input: false,
        ...(observation !== 'unavailable' ? { terminal_event_id: 42 } : {}),
      });
      const { controller, dependencies } = setup(old);
      await controller.refresh();
      dependencies.read.mockResolvedValue({ ...snapshot(old), availability: { available: false, reason: 'queued' } });
      dependencies.lookup.mockResolvedValue(old);
      await controller.refresh();
      expect(controller.retained).toBe(true);
      expect(controller.getSnapshot().receipt).toEqual(old);
    }
  );
  it('shares one observer/read between consumers and closing one presentation does not cancel work', async () => {
    const { controller, dependencies, release, releaseObservation } = setup(receipt());
    const second = controller.subscribe(() => {});
    releases.push(second);
    await controller.refresh();
    release();
    expect(releaseObservation).not.toHaveBeenCalled();
    expect(dependencies.read).toHaveBeenCalledTimes(1);
    expect(dependencies.observe).toHaveBeenCalledTimes(1);
    expect(dependencies.cancel).not.toHaveBeenCalled();
    expect(controller.getSnapshot().request).toEqual(manualCompactionRequest);
  });
  it('coalesces first reads and repeated explicit clicks into one frozen UUID/body', async () => {
    const { controller, dependencies } = setup();
    await Promise.all([controller.refresh(), controller.refresh()]);
    expect(dependencies.read).toHaveBeenCalledTimes(1);
    await Promise.all([controller.start(), controller.start()]);
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
    expect(dependencies.uuid).toHaveBeenCalledTimes(1);
    expect(dependencies.submit.mock.calls[0][1]).toEqual(manualCompactionRequest);
    expect(Object.isFrozen(controller.getSnapshot().request?.expected)).toBe(true);
  });
  it('restores the original request rather than borrowing the advanced snapshot target', async () => {
    const original = receipt();
    const { controller, dependencies } = setup(original);
    await controller.refresh();
    expect(controller.getSnapshot().snapshot?.target.input_revision).toBe(8);
    expect(controller.getSnapshot().request?.expected.input_revision).toBe(7);
    await controller.stop();
    expect(dependencies.cancel.mock.calls[0][1]).toEqual(original.request);
    expect(dependencies.submit).not.toHaveBeenCalled();
  });
  it('retains the original uncertain submission for explicit identical retry, never a fresh UUID', async () => {
    const { controller, dependencies } = setup();
    await controller.refresh();
    dependencies.submit.mockRejectedValueOnce(new Error('lost ACK'));
    dependencies.lookup.mockRejectedValueOnce(new Error('not found'));
    await controller.start();
    expect(controller.getSnapshot().uncertain).toBe('submit');
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
    await controller.retry();
    expect(dependencies.submit.mock.calls[1][1]).toBe(dependencies.submit.mock.calls[0][1]);
    expect(dependencies.uuid).toHaveBeenCalledTimes(1);
  });
  it('does not re-submit a lost ACK when exact lookup proves acceptance', async () => {
    const { controller, dependencies } = setup();
    await controller.refresh();
    dependencies.submit.mockRejectedValueOnce(new Error('lost ACK'));
    await controller.start();
    expect(controller.getSnapshot().uncertain).toBeNull();
    expect(controller.getSnapshot().receipt?.accepted).toBe(true);
    expect(dependencies.submit).toHaveBeenCalledTimes(1);
  });
  it('holds a new prompt through draining and resumes only after authoritative safe admission', async () => {
    const { controller, dependencies, invalidate } = setup(receipt());
    await controller.refresh();
    dependencies.cancel.mockResolvedValueOnce(
      receipt({ state: 'cancelled', cancellation_status: 'requested', execution_observation: 'draining' })
    );
    const admitted = vi.fn();
    const prompt = controller.prepareForPrompt().then(admitted);
    await vi.waitFor(() => expect(controller.getSnapshot().waitingForPrompt).toBe(true));
    expect(admitted).not.toHaveBeenCalled();
    dependencies.lookup.mockResolvedValueOnce(
      receipt({
        state: 'cancelled',
        cancellation_status: 'cancelled',
        execution_observation: 'unavailable',
        can_submit_next_input: true,
      })
    );
    invalidate();
    await controller.refresh();
    await prompt;
    expect(admitted).toHaveBeenCalledWith(true);
    expect(controller.getSnapshot().receipt?.execution_observation).toBe('unavailable');
    expect(dependencies.cancel).toHaveBeenCalledTimes(1);
  });
  it('does not duplicate a cancellation when Stop and a new prompt race', async () => {
    const { controller, dependencies } = setup(receipt());
    await controller.refresh();
    const cancel = deferred<CompactionReceipt>();
    dependencies.cancel.mockReturnValueOnce(cancel.promise);
    const stop = controller.stop(),
      prompt = controller.prepareForPrompt();
    await vi.waitFor(() => expect(dependencies.cancel).toHaveBeenCalledTimes(1));
    cancel.resolve(
      receipt({
        state: 'cancelled',
        cancellation_status: 'cancelled',
        execution_observation: 'unavailable',
        can_submit_next_input: true,
      })
    );
    await expect(prompt).resolves.toBe(true);
    await stop;
    expect(dependencies.cancel).toHaveBeenCalledTimes(1);
  });
  it('keeps uncertain cancel intent when a read only observes the old active receipt', async () => {
    const { controller, dependencies } = setup(receipt());
    await controller.refresh();
    dependencies.cancel.mockRejectedValueOnce(new Error('lost cancel ACK'));
    await controller.stop();
    expect(controller.getSnapshot().uncertain).toBe('cancel');
    await controller.refresh();
    expect(controller.getSnapshot().uncertain).toBe('cancel');
    expect(dependencies.cancel).toHaveBeenCalledTimes(1);
    await controller.retry();
    expect(dependencies.cancel).toHaveBeenCalledTimes(2);
  });
  it('retains an invalidation during bootstrap and performs one follow-up authoritative read', async () => {
    const { controller, dependencies, invalidate } = setup();
    const bootstrap = deferred<ReturnType<typeof snapshot>>();
    dependencies.read.mockReturnValueOnce(bootstrap.promise);
    invalidate();
    invalidate();
    bootstrap.resolve(snapshot());
    await controller.refresh();
    await vi.waitFor(() => expect(dependencies.read).toHaveBeenCalledTimes(2));
    expect(dependencies.submit).not.toHaveBeenCalled();
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
  it('discards late account results and never sends a waiting old-account prompt', async () => {
    const { controller, dependencies, invalidate } = setup(receipt());
    await controller.refresh();
    const cancel = deferred<CompactionReceipt>();
    dependencies.cancel.mockReturnValueOnce(cancel.promise);
    const prompt = controller.prepareForPrompt();
    await vi.waitFor(() => expect(dependencies.cancel).toHaveBeenCalledTimes(1));
    setRendererAccountOwner('different-owner');
    cancel.resolve(receipt({ execution_observation: 'settled', can_submit_next_input: true }));
    await expect(prompt).resolves.toBe(false);
    invalidate();
    expect(controller.getSnapshot().receipt?.can_submit_next_input).toBe(false);
    expect(dependencies.read).toHaveBeenCalledTimes(1);
  });
  it('rejects a changed root incarnation rather than rebinding the old request', async () => {
    const { controller, dependencies, invalidate } = setup();
    await controller.refresh();
    dependencies.read.mockResolvedValueOnce({
      ...snapshot(),
      target: { ...snapshot().target, root_frame_incarnation_id: 'replacement-root' },
    });
    invalidate();
    await controller.refresh();
    expect(controller.getSnapshot().error).toBe('scope');
    await controller.start();
    expect(dependencies.submit).not.toHaveBeenCalled();
    expect(dependencies.uuid).not.toHaveBeenCalled();
    expect(controller.getSnapshot().snapshot?.target.root_frame_incarnation_id).toBe(
      manualCompactionRequest.expected.root_frame_incarnation_id
    );
  });
  it('unmount releases the observer and preserves the uncertain original command without cancelling work', async () => {
    const { controller, dependencies, release, releaseObservation } = setup();
    await controller.refresh();
    const submission = deferred<CompactionReceipt>();
    dependencies.submit.mockReturnValueOnce(submission.promise);
    const pending = controller.start();
    await vi.waitFor(() => expect(dependencies.submit).toHaveBeenCalledTimes(1));
    release();
    submission.resolve(receipt());
    await pending;
    expect(releaseObservation).toHaveBeenCalledTimes(1);
    expect(controller.getSnapshot().request).toEqual(manualCompactionRequest);
    expect(controller.getSnapshot().uncertain).toBe('submit');
    expect(controller.getSnapshot().receipt).toBeNull();
    expect(dependencies.cancel).not.toHaveBeenCalled();
  });
});
