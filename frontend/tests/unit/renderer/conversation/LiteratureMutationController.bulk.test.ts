import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt,
} from '@/common/adapter/literatureReadingStatus';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { BulkPausedError } from '@/renderer/services/runtime/literatureReadingBulkFlow';
import {
  isLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import { decodePendingRecord } from '@/renderer/services/runtime/literatureUploadIntent';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';
import { deferredPdf } from './pdfReadingFixtures';

const OWNER = 'bulk-owner';
const KEY = `x-science.literature-mutation.v1:${OWNER}`;
const SCOPE = 'library:all:unread';
const operation = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const uuidSequence = (first = 0) => {
  let sequence = first;
  return vi.fn(() => operation(++sequence));
};
const targets = () => Array.from({ length: 205 }, (_, index) => `paper-${index}`);
const ack = (command: LiteratureReadingStatusCommand): LiteratureReadingStatusReceipt => ({
  schema: 'x-science.literature-reading-status-receipt.v1',
  operation_id: command.operation_id,
  item_ids: command.item_ids,
  reading_status: command.reading_status,
});
type MemoryStorage = ReturnType<typeof literaturePendingStorage>;
function pendingBulk(storage: MemoryStorage): LiteratureReadingBulkIntent {
  const raw = storage.getItem(KEY);
  expect(raw).not.toBeNull();
  const pending = decodePendingRecord(JSON.parse(raw!), OWNER);
  if (!isLiteratureReadingBulk(pending)) throw new Error('Expected the original bulk journal');
  return pending;
}

describe('Library bulk uses the original mutation controller and durable slot', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner(OWNER);
    installLiteratureLocks();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => {
        throw new Error('Unexpected real network');
      })
    );
  });
  afterEach(() => {
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
  });

  it('persists the complete 205-target two-batch plan before wire and clears only after both real ACKs', async () => {
    const storage = literaturePendingStorage();
    const uuid = uuidSequence();
    const sentPlans: LiteratureReadingBulkIntent[] = [];
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => {
      const saved = pendingBulk(storage);
      sentPlans.push(saved);
      expect(saved.phase).toBe('sending');
      expect(saved.batches.map((batch) => batch.item_ids.length)).toEqual([200, 5]);
      expect(saved.batches.flatMap((batch) => batch.item_ids)).toEqual(targets());
      expect(saved.batches[saved.confirmed_batches]).toEqual(command);
      return ack(command);
    });
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid });
    const selected = targets();
    const result = owner.startReadingStatusBulk(selected, 'read', SCOPE);
    selected.push('late-selection');
    const received = await result;
    expect(received).toEqual(ack(sentPlans[0].batches[1]));
    expect(run.mock.calls.map(([command]) => command.operation_id)).toEqual([operation(2), operation(3)]);
    expect(sentPlans.map((plan) => plan.confirmed_batches)).toEqual([0, 1]);
    expect(uuid).toHaveBeenCalledTimes(3);
    expect(storage.values.size).toBe(0);
    expect(owner.getSnapshot()).toMatchObject({ pending: null, mutating: false, error: null });
  });

  it('restores an unknown first ACK with the original child UUID and then sends the remaining batch', async () => {
    const storage = literaturePendingStorage();
    const lost = new TypeError('ACK response lost');
    const firstRun = vi.fn(
      async (_command: LiteratureReadingStatusCommand): Promise<LiteratureReadingStatusReceipt> => {
        throw lost;
      }
    );
    const first = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: firstRun,
      uuid: uuidSequence(),
    });
    await expect(first.startReadingStatusBulk(targets(), 'read', SCOPE)).rejects.toBe(lost);
    const saved = pendingBulk(storage);
    expect(saved).toMatchObject({ phase: 'sending', confirmed_batches: 0, paused: true });
    expect(saved.batches).toHaveLength(2);
    const replay = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const resumed = new LiteratureMutationController(OWNER, { storage, runReadingStatus: replay });
    await expect(resumed.resumeReadingStatusBulk()).resolves.toEqual(ack(saved.batches[1]));
    expect(replay.mock.calls.map(([command]) => command)).toEqual(saved.batches);
    expect(replay.mock.calls[0][0]).toEqual(firstRun.mock.calls[0][0]);
    expect(storage.values.size).toBe(0);
  });

  it('makes a pause before the initial slot write durable without sending HTTP', async () => {
    const gate = deferredPdf<void>();
    const locks = installLiteratureLocks(gate.promise);
    const storage = literaturePendingStorage();
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    const started = owner.startReadingStatusBulk(targets(), 'read', SCOPE);
    const stopped = expect(started).rejects.toBeInstanceOf(BulkPausedError);
    const root = owner.getSnapshot().action!.operation_id;
    const pause = owner.pauseReadingStatusBulk(root);
    await vi.waitFor(() => expect(locks).toHaveBeenCalled());
    expect(storage.values.size).toBe(0);
    expect(run).not.toHaveBeenCalled();
    gate.resolve();
    await pause;
    await stopped;
    expect(pendingBulk(storage)).toMatchObject({
      operation_id: root,
      phase: 'ready',
      confirmed_batches: 0,
      paused: true,
    });
    const restored = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run });
    expect(restored.getSnapshot().pending).toEqual(pendingBulk(storage));
    expect(run).not.toHaveBeenCalled();
  });

  it('confirms the current child and durably stops before the next batch when the visit callback becomes false', async () => {
    const storage = literaturePendingStorage();
    const reply = deferredPdf<LiteratureReadingStatusReceipt>();
    let visiting = true;
    const run = vi.fn((_command: LiteratureReadingStatusCommand) => reply.promise);
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    const started = owner.startReadingStatusBulk(targets(), 'read', SCOPE, () => visiting);
    await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    const command = run.mock.calls[0][0];
    visiting = false;
    reply.resolve(ack(command));
    await expect(started).resolves.toEqual(ack(command));
    expect(pendingBulk(storage)).toMatchObject({ phase: 'ready', confirmed_batches: 1, paused: true });
    expect(run).toHaveBeenCalledTimes(1);
    expect(owner.getSnapshot()).toMatchObject({ receipt: ack(command), mutating: false, error: null });
  });

  it('preserves a pause written by another renderer while accepting the in-flight child ACK', async () => {
    const storage = literaturePendingStorage();
    const reply = deferredPdf<LiteratureReadingStatusReceipt>();
    const run = vi.fn((_command: LiteratureReadingStatusCommand) => reply.promise);
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    const started = owner.startReadingStatusBulk(targets(), 'read', SCOPE);
    await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    const original = pendingBulk(storage);
    const other = new LiteratureMutationController(OWNER, { storage });
    await other.pauseReadingStatusBulk(original.operation_id);
    expect(pendingBulk(storage)).toMatchObject({ phase: 'sending', confirmed_batches: 0, paused: true });
    reply.resolve(ack(original.batches[0]));
    await expect(started).resolves.toEqual(ack(original.batches[0]));
    expect(pendingBulk(storage)).toMatchObject({ phase: 'ready', confirmed_batches: 1, paused: true });
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('does not overwrite a newer durable operation after a duplicate old child ACK arrives late', async () => {
    const storage = literaturePendingStorage();
    const firstReply = deferredPdf<LiteratureReadingStatusReceipt>();
    const lateReply = deferredPdf<LiteratureReadingStatusReceipt>();
    const nextReply = deferredPdf<LiteratureReadingStatusReceipt>();
    const runFirst = vi.fn((command: LiteratureReadingStatusCommand) =>
      command.operation_id === operation(2) ? firstReply.promise : Promise.resolve(ack(command))
    );
    const runLate = vi.fn((_command: LiteratureReadingStatusCommand) => lateReply.promise);
    const runNext = vi.fn((_command: LiteratureReadingStatusCommand) => nextReply.promise);
    const first = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: runFirst,
      uuid: uuidSequence(),
    });
    const firstTask = first.startReadingStatusBulk(targets(), 'read', SCOPE);
    await vi.waitFor(() => expect(runFirst).toHaveBeenCalledTimes(1));
    const original = pendingBulk(storage);
    const duplicate = new LiteratureMutationController(OWNER, { storage, runReadingStatus: runLate });
    const duplicateTask = duplicate.resumeReadingStatusBulk();
    const refused = expect(duplicateTask).rejects.toThrow('original');
    await vi.waitFor(() => expect(runLate).toHaveBeenCalledTimes(1));
    firstReply.resolve(ack(original.batches[0]));
    await firstTask;
    const next = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: runNext,
      uuid: uuidSequence(100),
    });
    const nextTask = next.startReadingStatusBulk(['new-paper'], 'reading', 'library:next');
    await vi.waitFor(() => expect(runNext).toHaveBeenCalledTimes(1));
    const nextRaw = storage.getItem(KEY);
    lateReply.resolve(ack(original.batches[0]));
    await refused;
    expect(storage.getItem(KEY)).toBe(nextRaw);
    expect(duplicate.getSnapshot()).toMatchObject({
      pending: pendingBulk(storage),
      mutating: false,
      error: 'uncertain',
    });
    expect(runLate).toHaveBeenCalledTimes(1);
    nextReply.resolve(ack(runNext.mock.calls[0][0]));
    await nextTask;
    expect(storage.values.size).toBe(0);
  });

  it('keeps sending after an ACK checkpoint storage failure and replays the same child before advancing', async () => {
    const storage = literaturePendingStorage();
    const write = storage.setItem;
    let blockCheckpoint = true;
    storage.setItem = (key, raw) => {
      const candidate = decodePendingRecord(JSON.parse(raw), OWNER);
      if (blockCheckpoint && isLiteratureReadingBulk(candidate) && candidate.confirmed_batches === 1) {
        blockCheckpoint = false;
        throw new Error('checkpoint storage blocked');
      }
      write(key, raw);
    };
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const first = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    await expect(first.startReadingStatusBulk(targets(), 'read', SCOPE)).rejects.toThrow('storage unavailable');
    const saved = pendingBulk(storage);
    expect(saved).toMatchObject({ phase: 'sending', confirmed_batches: 0 });
    expect(first.getSnapshot()).toMatchObject({ error: 'storage', mutating: false, pending: saved });
    const resumed = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run });
    await expect(resumed.resumeReadingStatusBulk()).resolves.toEqual(ack(saved.batches[1]));
    expect(run.mock.calls.map(([command]) => command)).toEqual([saved.batches[0], ...saved.batches]);
    expect(storage.values.size).toBe(0);
  });

  it('uses generic retry only to confirm the unknown current child and leaves the remaining plan paused', async () => {
    const storage = literaturePendingStorage();
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    run.mockRejectedValueOnce(new TypeError('response lost'));
    const first = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    await expect(first.startReadingStatusBulk(targets(), 'read', SCOPE)).rejects.toThrow('response lost');
    const original = pendingBulk(storage);
    const restored = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run });
    await expect(restored.retry()).resolves.toEqual(ack(original.batches[0]));
    expect(run.mock.calls.map(([command]) => command)).toEqual([original.batches[0], original.batches[0]]);
    expect(pendingBulk(storage)).toMatchObject({ phase: 'ready', confirmed_batches: 1, paused: true });
    expect(restored.getSnapshot()).toMatchObject({ mutating: false, receipt: ack(original.batches[0]) });
  });

  it('refuses discard for sending or a different operation and discards only its exact ready remainder', async () => {
    const storage = literaturePendingStorage();
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    run.mockRejectedValueOnce(new TypeError('response lost'));
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    await expect(owner.startReadingStatusBulk(targets(), 'read', SCOPE)).rejects.toThrow('response lost');
    const original = pendingBulk(storage);
    const sendingRaw = storage.getItem(KEY);
    await expect(owner.discardReadingStatusBulk(original.operation_id)).rejects.toThrow('Confirm the original');
    expect(storage.getItem(KEY)).toBe(sendingRaw);
    await owner.retry();
    const readyRaw = storage.getItem(KEY);
    expect(pendingBulk(storage)).toMatchObject({ phase: 'ready', confirmed_batches: 1, paused: true });
    await expect(owner.discardReadingStatusBulk(operation(999))).rejects.toThrow('original');
    expect(storage.getItem(KEY)).toBe(readyRaw);
    await owner.discardReadingStatusBulk(original.operation_id);
    expect(storage.values.size).toBe(0);
    expect(owner.getSnapshot()).toMatchObject({ pending: null, mutating: false, error: null });
    expect(run).toHaveBeenCalledTimes(2);
  });

  it('honours an immediate pause while the ready-to-sending claim waits for its slot lock', async () => {
    const storage = literaturePendingStorage();
    const claimGate = deferredPdf<void>();
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    let queuedClaim: ReturnType<typeof installLiteratureLocks> | undefined;
    const unsubscribe = owner.subscribe(() => {
      const pending = owner.getSnapshot().pending;
      if (!queuedClaim && isLiteratureReadingBulk(pending) && pending.phase === 'ready') {
        // The initial journal write is already held; only subsequent acquisitions are gated.
        queuedClaim = installLiteratureLocks(claimGate.promise);
        unsubscribe();
      }
    });
    const started = owner.startReadingStatusBulk(targets(), 'read', SCOPE);
    const outcome = started.then(
      (receipt) => ({ receipt, error: undefined as undefined }),
      (error: unknown) => ({ receipt: undefined as undefined, error })
    );
    await vi.waitFor(() => expect(queuedClaim).toHaveBeenCalledTimes(1));
    const original = pendingBulk(storage);
    expect(original).toMatchObject({ phase: 'ready', confirmed_batches: 0, paused: false });
    const stopped = owner.pauseReadingStatusBulk(original.operation_id);
    claimGate.resolve();
    await stopped;
    const settled = await outcome;
    expect(run).not.toHaveBeenCalled();
    expect(settled.error).toBeInstanceOf(BulkPausedError);
    expect(pendingBulk(storage)).toMatchObject({ phase: 'ready', confirmed_batches: 0, paused: true });
  });

  it('clears the exact final slot after an in-flight durable pause and its real final ACK', async () => {
    const storage = literaturePendingStorage();
    const reply = deferredPdf<LiteratureReadingStatusReceipt>();
    const run = vi.fn((_command: LiteratureReadingStatusCommand) => reply.promise);
    const owner = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    const started = owner.startReadingStatusBulk(['last-paper'], 'read', SCOPE);
    await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    const original = pendingBulk(storage);
    const other = new LiteratureMutationController(OWNER, { storage });
    await other.pauseReadingStatusBulk(original.operation_id);
    expect(pendingBulk(storage)).toMatchObject({ phase: 'sending', confirmed_batches: 0, paused: true });
    const receipt = ack(original.batches[0]);
    reply.resolve(receipt);
    await expect(started).resolves.toEqual(receipt);
    expect(run).toHaveBeenCalledTimes(1);
    expect(storage.getItem(KEY)).toBeNull();
    expect(owner.getSnapshot()).toMatchObject({ pending: null, receipt, error: null, mutating: false });
  });

  it('projects a cleared durable slot before a restored renderer rejects resume and accepts a new explicit plan', async () => {
    const storage = literaturePendingStorage();
    const run = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const first = new LiteratureMutationController(OWNER, { storage, runReadingStatus: run, uuid: uuidSequence() });
    await expect(first.startReadingStatusBulk(targets(), 'read', SCOPE, () => false)).rejects.toBeInstanceOf(
      BulkPausedError
    );
    const original = pendingBulk(storage);
    const restored = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: run,
      uuid: uuidSequence(100),
    });
    expect(restored.getSnapshot().pending).toEqual(original);
    await first.discardReadingStatusBulk(original.operation_id);
    expect(storage.getItem(KEY)).toBeNull();
    await expect(restored.resumeReadingStatusBulk()).rejects.toBeInstanceOf(Error);
    expect(restored.getSnapshot().pending).toBeNull();
    expect(run).not.toHaveBeenCalled();
    await restored.startReadingStatusBulk(['new-paper'], 'reading', 'library:next');
    expect(run.mock.calls.map(([command]) => command.item_ids)).toEqual([['new-paper']]);
    expect(storage.getItem(KEY)).toBeNull();
  });

  it('refuses to resume a newer bulk operation when its renderer still owns the cached previous plan', async () => {
    const storage = literaturePendingStorage();
    const initialRun = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const first = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: initialRun,
      uuid: uuidSequence(),
    });
    await expect(first.startReadingStatusBulk(targets(), 'read', SCOPE, () => false)).rejects.toBeInstanceOf(
      BulkPausedError
    );
    const previous = pendingBulk(storage);
    const restoredRun = vi.fn(async (command: LiteratureReadingStatusCommand) => ack(command));
    const restored = new LiteratureMutationController(OWNER, { storage, runReadingStatus: restoredRun });
    await first.discardReadingStatusBulk(previous.operation_id);
    const nextReply = deferredPdf<LiteratureReadingStatusReceipt>();
    const nextRun = vi.fn((_command: LiteratureReadingStatusCommand) => nextReply.promise);
    const next = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: nextRun,
      uuid: uuidSequence(100),
    });
    const nextTask = next.startReadingStatusBulk(['new-paper'], 'reading', 'library:next').then(
      (receipt) => ({ receipt, error: undefined as undefined }),
      (error: unknown) => ({ receipt: undefined as undefined, error })
    );
    await vi.waitFor(() => expect(nextRun).toHaveBeenCalledTimes(1));
    const nextPlan = pendingBulk(storage);
    const nextRaw = storage.getItem(KEY);
    const resumed = await restored.resumeReadingStatusBulk().then(
      (receipt) => ({ receipt, error: undefined as undefined }),
      (error: unknown) => ({ receipt: undefined as undefined, error })
    );
    const retainedRaw = storage.getItem(KEY);
    const published = restored.getSnapshot().pending;
    nextReply.resolve(ack(nextRun.mock.calls[0][0]));
    await nextTask;
    expect(restoredRun).not.toHaveBeenCalled();
    expect(resumed.error).toBeInstanceOf(Error);
    expect((resumed.error as Error).message).toContain('original');
    expect(retainedRaw).toBe(nextRaw);
    expect(published).toEqual(nextPlan);
  });
});
