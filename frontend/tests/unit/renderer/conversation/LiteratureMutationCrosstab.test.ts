import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { deferredPdf } from './pdfReadingFixtures';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({
  httpRequest: mocks.http,
  isBackendHttpError: (value: unknown) => Boolean(value && typeof value === 'object' && 'status' in value),
}));
const operation = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const command = (n = 1): Extract<LiteratureCommand, { kind: 'edit' }> => ({
  kind: 'edit',
  operation_id: operation(n),
  item_id: 'paper',
  expected_revision: 1,
  title: 'Title',
});
const receipt = (n = 1): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1',
  operation_id: operation(n),
  item: {
    id: 'paper',
    title: 'Title',
    metadata_revision: 2,
    metadata: {},
    deleted: false,
    project_ids: [],
    attachments: [],
  },
});
const pendingKey = 'x-science.literature-mutation.v1:owner';

describe('Library cross-renderer original request ownership', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    mocks.http.mockReset();
    installLiteratureLocks();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('rejects a second renderer before it can overwrite the original durable command', async () => {
    const storage = literaturePendingStorage();
    const firstReply = deferredPdf<LiteratureReceipt>();
    const runA = vi.fn(() => firstReply.promise);
    const runB = vi.fn().mockResolvedValue(receipt(2));
    const a = new LiteratureMutationController('owner', { storage, run: runA });
    const b = new LiteratureMutationController('owner', { storage, run: runB });
    const first = a.submit(command());
    await vi.waitFor(() => expect(runA).toHaveBeenCalledTimes(1));
    const outcome = b.submit(command(2)).then(
      () => 'unexpected success',
      () => 'refused'
    );
    await expect(outcome).resolves.toBe('refused');
    expect(runB).not.toHaveBeenCalled();
    expect(JSON.parse(storage.getItem(pendingKey)!)).toEqual({ ownerId: 'owner', command: command() });
    expect(b.getSnapshot()).toMatchObject({ pending: command(), error: 'uncertain' });
    firstReply.resolve(receipt());
    await first;
  });

  it('retains a new operation when a duplicate original ACK arrives late', async () => {
    const storage = literaturePendingStorage();
    const firstReply = deferredPdf<LiteratureReceipt>();
    const replayReply = deferredPdf<LiteratureReceipt>();
    const nextReply = deferredPdf<LiteratureReceipt>();
    const runA = vi.fn(() => firstReply.promise);
    const runReplay = vi.fn(() => replayReply.promise);
    const runNext = vi.fn(() => nextReply.promise);
    const a = new LiteratureMutationController('owner', { storage, run: runA });
    const replay = new LiteratureMutationController('owner', { storage, run: runReplay });
    const next = new LiteratureMutationController('owner', { storage, run: runNext });
    const first = a.submit(command());
    const duplicate = replay.submit(command());
    await vi.waitFor(() => expect(runReplay).toHaveBeenCalledTimes(1));
    firstReply.resolve(receipt());
    await first;
    const second = next.submit(command(2));
    await vi.waitFor(() => expect(runNext).toHaveBeenCalledTimes(1));
    replayReply.resolve(receipt());
    await duplicate;
    expect(JSON.parse(storage.getItem(pendingKey)!)).toEqual({ ownerId: 'owner', command: command(2) });
    expect(replay.getSnapshot()).toMatchObject({ pending: command(2), error: 'uncertain', receipt: receipt() });
    nextReply.resolve(receipt(2));
    await second;
  });

  it('fails closed without native Web Locks and never calls the transport', async () => {
    vi.stubGlobal('navigator', {});
    const storage = literaturePendingStorage();
    const run = vi.fn().mockResolvedValue(receipt());
    const controller = new LiteratureMutationController('owner', { storage, run });
    await expect(controller.submit(command())).rejects.toThrow(/storage|lock/i);
    expect(run).not.toHaveBeenCalled();
    expect(storage.values.size).toBe(0);
    expect(controller.getSnapshot()).toMatchObject({ error: 'storage', mutating: false });
  });

  it('keeps a synchronously throwing transport recoverable without a stuck mutation', async () => {
    const storage = literaturePendingStorage();
    const run = vi
      .fn()
      .mockImplementationOnce(() => {
        throw new TypeError('transport failed');
      })
      .mockResolvedValueOnce(receipt());
    const controller = new LiteratureMutationController('owner', { storage, run });
    await expect(controller.submit(command())).rejects.toThrow('transport failed');
    expect(controller.getSnapshot()).toMatchObject({ pending: command(), error: 'uncertain', mutating: false });
    await controller.retry();
    expect(run.mock.calls.map(([value]) => value)).toEqual([command(), command()]);
    expect(storage.values.size).toBe(0);
  });

  it('uses the native lock API on the default HTTP command path', async () => {
    const request = installLiteratureLocks();
    const storage = literaturePendingStorage();
    mocks.http.mockResolvedValue(receipt());
    await new LiteratureMutationController('owner', { storage }).submit(command());
    expect(request).toHaveBeenCalledTimes(2);
    const [name, options] = request.mock.calls[0];
    expect(name).toBe(pendingKey);
    expect(options.mode).toBe('exclusive');
    expect(options.signal).toBeInstanceOf(AbortSignal);
    expect(request.mock.calls[1][0]).toBe(name);
    expect(mocks.http).toHaveBeenCalledTimes(1);
    expect(mocks.http.mock.calls[0].slice(0, 3)).toEqual([
      'PATCH',
      '/api/literature/items/paper',
      {
        operation_id: operation(1),
        expected_revision: 1,
        title: 'Title',
      },
    ]);
    setRendererAccountOwner('other');
    // Completed acquisitions unregister their account-reset listeners.
    expect(request.mock.calls.every(([, value]) => !value.signal?.aborted)).toBe(true);
  });

  it('coalesces reentrant original submissions before notifying and refuses a different command', async () => {
    const storage = literaturePendingStorage();
    const reply = deferredPdf<LiteratureReceipt>();
    const run = vi.fn(() => reply.promise);
    const controller = new LiteratureMutationController('owner', { storage, run });
    let duplicate: Promise<LiteratureReceipt> | undefined;
    let changed: Promise<string> | undefined;
    const unsubscribe = controller.subscribe(() => {
      if (!controller.getSnapshot().mutating) return;
      unsubscribe();
      duplicate = controller.submit(command());
      changed = controller.submit({ ...command(), title: 'Changed payload' }).then(
        () => 'wrong ACK',
        () => 'refused'
      );
    });
    const original = controller.submit(command());
    expect(duplicate).toBe(original);
    await expect(changed).resolves.toBe('refused');
    await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    reply.resolve(receipt());
    await original;
  });

  it('retries the exact original command after receipt storage becomes available again', async () => {
    const storage = literaturePendingStorage();
    const remove = storage.removeItem;
    storage.removeItem = vi
      .fn()
      .mockImplementationOnce(() => {
        throw new Error('storage blocked');
      })
      .mockImplementation(remove);
    const run = vi.fn().mockResolvedValue(receipt());
    const controller = new LiteratureMutationController('owner', { storage, run });
    await expect(controller.submit(command())).rejects.toThrow('storage unavailable');
    expect(controller.getSnapshot()).toMatchObject({ pending: command(), error: 'storage', mutating: false });
    await controller.retry();
    expect(run.mock.calls.map(([value]) => value)).toEqual([command(), command()]);
    expect(storage.values.size).toBe(0);
    expect(controller.getSnapshot()).toMatchObject({ pending: null, error: null, receipt: receipt() });
  });

  it.each(['rejected', 'uncertain'] as const)(
    'does not replace the next durable operation after a late %s response',
    async (outcome) => {
      const storage = literaturePendingStorage();
      const firstReply = deferredPdf<LiteratureReceipt>();
      const duplicateReply = deferredPdf<LiteratureReceipt>();
      const nextReply = deferredPdf<LiteratureReceipt>();
      const runDuplicate = vi.fn(() => duplicateReply.promise);
      const runNext = vi.fn(() => nextReply.promise);
      const first = new LiteratureMutationController('owner', { storage, run: () => firstReply.promise });
      const duplicate = new LiteratureMutationController('owner', { storage, run: runDuplicate });
      const next = new LiteratureMutationController('owner', { storage, run: runNext });
      const firstRun = first.submit(command());
      const duplicateRun = duplicate.submit(command()).then(
        () => 'wrong ACK',
        () => 'failed'
      );
      await vi.waitFor(() => expect(runDuplicate).toHaveBeenCalledTimes(1));
      firstReply.resolve(receipt());
      await firstRun;
      const nextRun = next.submit(command(2));
      await vi.waitFor(() => expect(runNext).toHaveBeenCalledTimes(1));
      duplicateReply.reject(outcome === 'rejected' ? { status: 409 } : new TypeError('lost receipt'));
      await expect(duplicateRun).resolves.toBe('failed');
      expect(JSON.parse(storage.getItem(pendingKey)!)).toEqual({ ownerId: 'owner', command: command(2) });
      expect(duplicate.getSnapshot()).toMatchObject({ pending: command(2), error: 'uncertain', mutating: false });
      nextReply.resolve(receipt(2));
      await nextRun;
    }
  );

  it.each(['account change', 'timeout'] as const)(
    'cancels a queued acquisition on %s without a late storage write',
    async (cause) => {
      vi.useFakeTimers();
      const gate = deferredPdf<void>();
      const request = installLiteratureLocks(gate.promise);
      const storage = literaturePendingStorage();
      const run = vi.fn().mockResolvedValue(receipt());
      const controller = new LiteratureMutationController('owner', { storage, run });
      const pending = controller.submit(command()).then(
        () => 'wrong ACK',
        () => 'refused'
      );
      await vi.advanceTimersByTimeAsync(0);
      expect(request).toHaveBeenCalledTimes(1);
      if (cause === 'account change') setRendererAccountOwner('other');
      else await vi.advanceTimersByTimeAsync(5_000);
      await expect(pending).resolves.toBe('refused');
      expect(request.mock.calls[0][1].signal?.aborted).toBe(true);
      gate.resolve();
      await vi.advanceTimersByTimeAsync(0);
      expect(run).not.toHaveBeenCalled();
      expect(storage.values.size).toBe(0);
      if (cause === 'timeout') expect(controller.getSnapshot()).toMatchObject({ error: 'storage', mutating: false });
    }
  );

  it('refuses a cancelled native callback even if it arrives after the acquisition rejected', async () => {
    vi.useFakeTimers();
    let late: (() => unknown) | undefined;
    vi.stubGlobal('navigator', {
      locks: {
        request: (_name: string, options: LockOptions, callback: () => unknown) => {
          late = callback;
          return new Promise((_, reject) =>
            options.signal?.addEventListener('abort', () => reject(options.signal?.reason), { once: true })
          );
        },
      },
    });
    const storage = literaturePendingStorage();
    const run = vi.fn().mockResolvedValue(receipt());
    const controller = new LiteratureMutationController('owner', { storage, run });
    const pending = controller.submit(command()).then(
      () => 'wrong ACK',
      () => 'refused'
    );
    await vi.advanceTimersByTimeAsync(5_000);
    await expect(pending).resolves.toBe('refused');
    expect(() => late?.()).toThrow('lock timed out');
    expect(run).not.toHaveBeenCalled();
    expect(storage.values.size).toBe(0);
  });

  it('clears only a matching definite rejection and allows the next explicit operation', async () => {
    const storage = literaturePendingStorage();
    const run = vi.fn().mockRejectedValueOnce({ status: 409 }).mockResolvedValueOnce(receipt(2));
    const controller = new LiteratureMutationController('owner', { storage, run });
    await expect(controller.submit(command())).rejects.toMatchObject({ status: 409 });
    expect(controller.getSnapshot()).toMatchObject({ pending: null, error: 'rejected', mutating: false });
    expect(storage.values.size).toBe(0);
    await controller.submit(command(2));
    expect(run.mock.calls.map(([value]) => value)).toEqual([command(), command(2)]);
  });
});
