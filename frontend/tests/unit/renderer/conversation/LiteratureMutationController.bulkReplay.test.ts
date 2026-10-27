import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type {
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt,
} from '@/common/adapter/literatureReadingStatus';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  isLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import { decodePendingRecord } from '@/renderer/services/runtime/literatureUploadIntent';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';

const OWNER = 'bulk-replay-owner';
const KEY = `x-science.literature-mutation.v1:${OWNER}`;
const items = () => Array.from({ length: 205 }, (_, index) => `replay-paper-${index}`);
const operation = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const uuid = () => {
  let sequence = 0;
  return () => operation(++sequence);
};
const ack = (command: LiteratureReadingStatusCommand): LiteratureReadingStatusReceipt => ({
  schema: 'x-science.literature-reading-status-receipt.v1',
  operation_id: command.operation_id,
  item_ids: command.item_ids,
  reading_status: command.reading_status,
});
const rejection = (status: number) =>
  new BackendHttpError({
    method: 'POST',
    path: '/api/literature/items/reading-status',
    status,
    body: {},
  });
type MemoryStorage = ReturnType<typeof literaturePendingStorage>;
function readBulk(storage: MemoryStorage): LiteratureReadingBulkIntent {
  const raw = storage.getItem(KEY);
  expect(raw).not.toBeNull();
  const pending = decodePendingRecord(JSON.parse(raw!), OWNER);
  if (!isLiteratureReadingBulk(pending)) throw new Error('Expected the original bulk pending record');
  return pending;
}

/** Synthetic dispatch boundary: applying once and replaying a receipt are independently observable. */
function ledger() {
  const committed = new Map<
    string,
    { command: LiteratureReadingStatusCommand; receipt: LiteratureReadingStatusReceipt }
  >();
  const applied: string[] = [];
  return {
    applied,
    commit(command: LiteratureReadingStatusCommand): LiteratureReadingStatusReceipt {
      const previous = committed.get(command.operation_id);
      if (previous) {
        expect(command).toEqual(previous.command);
        return previous.receipt;
      }
      const receipt = ack(command);
      committed.set(command.operation_id, { command, receipt });
      applied.push(command.operation_id);
      return receipt;
    },
  };
}

async function committedWithoutAck() {
  const storage = literaturePendingStorage();
  const server = ledger();
  const wire: LiteratureReadingStatusCommand[] = [];
  const lost = new TypeError('Response lost after synthetic server commit');
  const first = new LiteratureMutationController(OWNER, {
    storage,
    uuid: uuid(),
    runReadingStatus: async (command) => {
      wire.push(command);
      server.commit(command);
      throw lost;
    },
  });
  await expect(first.startReadingStatusBulk(items(), 'read', 'library:replay')).rejects.toBe(lost);
  const original = readBulk(storage);
  expect(original).toMatchObject({ phase: 'sending', confirmed_batches: 0, paused: true });
  expect(server.applied).toEqual([original.batches[0].operation_id]);
  return { storage, server, wire, original };
}

describe('Library bulk preserves an original unknown outcome across rejected replays', () => {
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

  it('retains uncertain sending for rejected original UUID replays and recovers only from real child ACKs', async () => {
    for (const status of [401, 403, 400]) {
      const { storage, server, wire, original } = await committedWithoutAck();
      const rejected = new LiteratureMutationController(OWNER, {
        storage,
        runReadingStatus: async (command) => {
          wire.push(command);
          throw rejection(status);
        },
      });
      expect(rejected.getSnapshot().pending).toEqual(original);
      await expect(rejected.resumeReadingStatusBulk()).rejects.toMatchObject({ status });
      expect(readBulk(storage)).toMatchObject({ phase: 'sending', confirmed_batches: 0, paused: true });
      expect(readBulk(storage).batches).toEqual(original.batches);
      expect(wire).toEqual([original.batches[0], original.batches[0]]);
      expect(server.applied).toEqual([original.batches[0].operation_id]);
      await expect(rejected.discardReadingStatusBulk(original.operation_id)).rejects.toThrow('Confirm the original');
      expect(rejected.getSnapshot()).toMatchObject({ error: 'uncertain', mutating: false });

      const authStatus = status === 401 || status === 403;
      const recovering = new LiteratureMutationController(OWNER, {
        storage,
        runReadingStatus: async (command) => {
          wire.push(command);
          if (authStatus && command.operation_id === original.batches[1].operation_id) throw rejection(status);
          return server.commit(command);
        },
      });
      if (authStatus) {
        // Authentication may reject the next fresh request before reaching its ledger too.
        await expect(recovering.resumeReadingStatusBulk()).rejects.toMatchObject({ status });
        expect(readBulk(storage)).toMatchObject({ phase: 'sending', confirmed_batches: 1, paused: true });
        await expect(recovering.discardReadingStatusBulk(original.operation_id)).rejects.toThrow(
          'Confirm the original'
        );
        expect(server.applied).toEqual([original.batches[0].operation_id]);
        const final = new LiteratureMutationController(OWNER, {
          storage,
          runReadingStatus: async (command) => {
            wire.push(command);
            return server.commit(command);
          },
        });
        await expect(final.resumeReadingStatusBulk()).resolves.toEqual(ack(original.batches[1]));
      } else await expect(recovering.resumeReadingStatusBulk()).resolves.toEqual(ack(original.batches[1]));
      expect(wire).toEqual([
        original.batches[0],
        original.batches[0],
        original.batches[0],
        original.batches[1],
        ...(authStatus ? [original.batches[1]] : []),
      ]);
      expect(server.applied).toEqual(original.batches.map((batch) => batch.operation_id));
      expect(storage.values.size).toBe(0);
    }
  });

  it('keeps a later fresh batch business rejection discardable after confirming the recovered child', async () => {
    const { storage, server, wire, original } = await committedWithoutAck();
    const resumed = new LiteratureMutationController(OWNER, {
      storage,
      runReadingStatus: async (command) => {
        wire.push(command);
        if (command.operation_id === original.batches[1].operation_id) throw rejection(400);
        return server.commit(command);
      },
    });
    await expect(resumed.resumeReadingStatusBulk()).rejects.toMatchObject({ status: 400 });
    expect(readBulk(storage)).toMatchObject({ phase: 'ready', confirmed_batches: 1, paused: true });
    expect(readBulk(storage).batches).toEqual(original.batches);
    expect(wire).toEqual([original.batches[0], ...original.batches]);
    expect(server.applied).toEqual([original.batches[0].operation_id]);
    expect(resumed.getSnapshot()).toMatchObject({
      error: 'rejected',
      receipt: ack(original.batches[0]),
      mutating: false,
    });
    await resumed.discardReadingStatusBulk(original.operation_id);
    expect(storage.values.size).toBe(0);
    expect(wire).toHaveLength(3);
  });
});
