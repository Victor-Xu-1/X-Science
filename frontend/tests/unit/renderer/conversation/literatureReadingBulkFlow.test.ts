import { describe, expect, it, vi } from 'vitest';
import { type LiteratureReadingStatusCommand } from '@/common/adapter/literatureReadingStatus';
import {
  advanceLiteratureReadingBulk,
  claimLiteratureReadingBulk,
  createLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import {
  BulkPausedError,
  runLiteratureReadingBulkFlow,
  type LiteratureReadingBulkPort,
} from '@/renderer/services/runtime/literatureReadingBulkFlow';

const create = (count = 201) => {
  let sequence = 0;
  return createLiteratureReadingBulk(
    Array.from({ length: count }, (_, i) => `paper-${i}`),
    'read',
    'all',
    () => `00000000-0000-0000-0000-${(++sequence).toString(16).padStart(12, '0')}`
  );
};
const ack = (command: LiteratureReadingStatusCommand) => ({
  schema: 'x-science.literature-reading-status-receipt.v1' as const,
  operation_id: command.operation_id,
  item_ids: [...command.item_ids],
  reading_status: command.reading_status,
});
function fixture(initial: LiteratureReadingBulkIntent) {
  let pending: LiteratureReadingBulkIntent | null = initial;
  let active = true;
  const events: string[] = [];
  const assertExpected = (expected: LiteratureReadingBulkIntent) => {
    expect(pending).toEqual(expected);
  };
  const port = {
    checkActive: vi.fn(() => {
      if (!active) throw new Error('superseded');
    }),
    shouldPause: vi.fn(() => Boolean(pending?.paused)),
    save: vi.fn<LiteratureReadingBulkPort['save']>(async (expected, next, receipt) => {
      assertExpected(expected);
      events.push(`save:${next.phase}:${next.confirmed_batches}`);
      if (next.phase === 'ready')
        expect(receipt).toMatchObject({
          operation_id:
            expected.batches[Math.min(expected.confirmed_batches, expected.batches.length - 1)].operation_id,
        });
      pending = next;
    }),
    send: vi.fn(async (command: LiteratureReadingStatusCommand) => {
      expect(pending?.phase).toBe('sending');
      events.push(`send:${command.operation_id}`);
      return ack(command);
    }),
    finish: vi.fn<LiteratureReadingBulkPort['finish']>(async (expected, receipt) => {
      assertExpected(expected);
      events.push('finish');
      expect(receipt.operation_id).toBe(expected.batches.at(-1)?.operation_id);
      pending = null;
    }),
    paused: vi.fn(),
    failed: vi.fn(async () => {}),
  } satisfies LiteratureReadingBulkPort;
  return {
    port,
    events,
    get pending() {
      return pending;
    },
    setPending(value: LiteratureReadingBulkIntent) {
      pending = value;
    },
    supersede() {
      active = false;
    },
  };
}

describe('Library reading bulk existing-owner flow', () => {
  it('persists sending before wire and each real ACK before the next child or exact finish', async () => {
    const initial = create();
    const f = fixture(initial);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const save = f.port.save;
    f.port.save = vi.fn(async (expected, next, receipt) => {
      if (next.phase === 'sending' && next.confirmed_batches === 0) await gate;
      await save(expected, next, receipt);
    });
    const result = runLiteratureReadingBulkFlow(initial, f.port);
    await Promise.resolve();
    expect(f.port.send).not.toHaveBeenCalled();
    release();
    expect(await result).toEqual(ack(initial.batches[1]));
    expect(f.events).toEqual([
      'save:sending:0',
      `send:${initial.batches[0].operation_id}`,
      'save:ready:1',
      'save:sending:1',
      `send:${initial.batches[1].operation_id}`,
      'save:ready:2',
      'finish',
    ]);
    expect(f.pending).toBeNull();
  });

  it('retains the sending child after a lost ACK and replays its original UUID on restart', async () => {
    const initial = create();
    const f = fixture(initial);
    const unknown = new Error('response lost after acceptance');
    f.port.send.mockRejectedValueOnce(unknown);
    await expect(runLiteratureReadingBulkFlow(initial, f.port)).rejects.toBe(unknown);
    expect(f.pending).toMatchObject({ phase: 'sending', confirmed_batches: 0 });
    expect(f.port.failed).toHaveBeenCalledWith(f.pending, unknown);
    expect(f.port.finish).not.toHaveBeenCalled();
    const restored = JSON.parse(JSON.stringify(f.pending));
    expect(await runLiteratureReadingBulkFlow(restored, f.port)).toEqual(ack(initial.batches[1]));
    expect(f.port.send.mock.calls.map(([command]) => command.operation_id)).toEqual([
      initial.batches[0].operation_id,
      initial.batches[0].operation_id,
      initial.batches[1].operation_id,
    ]);
  });

  it('does not advance or finish on an invalid child ACK', async () => {
    const initial = create();
    const f = fixture(initial);
    f.port.send.mockResolvedValueOnce({ ...ack(initial.batches[0]), operation_id: initial.batches[1].operation_id });
    await expect(runLiteratureReadingBulkFlow(initial, f.port)).rejects.toThrow();
    expect(f.pending).toMatchObject({ phase: 'sending', confirmed_batches: 0 });
    expect(f.port.failed).toHaveBeenCalledTimes(1);
    expect(f.port.finish).not.toHaveBeenCalled();
    expect(f.events).toEqual(['save:sending:0']);
  });

  it('uses a control result for stop before the first ACK and pauses a persisted sending batch only after confirmation', async () => {
    const initial = create();
    const before = fixture(Object.freeze({ ...initial, paused: true }));
    await expect(runLiteratureReadingBulkFlow(before.pending!, before.port)).rejects.toBeInstanceOf(BulkPausedError);
    expect(before.port.send).not.toHaveBeenCalled();
    expect(before.port.failed).not.toHaveBeenCalled();
    const sending = claimLiteratureReadingBulk(Object.freeze({ ...initial, paused: true }));
    const during = fixture(sending);
    expect(await runLiteratureReadingBulkFlow(sending, during.port)).toEqual(ack(initial.batches[0]));
    expect(during.pending).toMatchObject({ phase: 'ready', confirmed_batches: 1, paused: true });
    expect(during.port.send).toHaveBeenCalledTimes(1);
    expect(during.port.paused).toHaveBeenCalledWith(during.pending);
    during.setPending(Object.freeze({ ...during.pending!, paused: false }));
    expect(await runLiteratureReadingBulkFlow(during.pending!, during.port)).toEqual(ack(initial.batches[1]));
    expect(during.pending).toBeNull();
  });

  it('honours a pause discovered by the locked save without a new request or fabricated receipt', async () => {
    for (const afterFirst of [false, true]) {
      const initial = create();
      const f = fixture(initial);
      f.port.shouldPause.mockReturnValue(false);
      const save = f.port.save;
      f.port.save = vi.fn(async (expected, next, receipt) => {
        if (next.phase === 'sending' && next.confirmed_batches === (afterFirst ? 1 : 0)) {
          const paused = Object.freeze({ ...expected, paused: true });
          f.setPending(paused);
          throw new BulkPausedError(paused);
        }
        await save(expected, next, receipt);
      });
      const result = runLiteratureReadingBulkFlow(initial, f.port);
      if (afterFirst) expect(await result).toEqual(ack(initial.batches[0]));
      else await expect(result).rejects.toBeInstanceOf(BulkPausedError);
      expect(f.port.send).toHaveBeenCalledTimes(afterFirst ? 1 : 0);
      expect(f.port.finish).not.toHaveBeenCalled();
      expect(f.port.failed).not.toHaveBeenCalled();
      expect(f.pending?.paused).toBe(true);
    }
  });

  it('replays the final original child for a completed restore even when persisted pause is true', async () => {
    const initial = create(1);
    const complete = advanceLiteratureReadingBulk(claimLiteratureReadingBulk(initial), ack(initial.batches[0]));
    const restored = Object.freeze({ ...complete, paused: true });
    const f = fixture(restored);
    expect(await runLiteratureReadingBulkFlow(restored, f.port)).toEqual(ack(initial.batches[0]));
    expect(f.port.send).toHaveBeenCalledWith(initial.batches[0]);
    expect(f.events).toEqual(['save:sending:1', `send:${initial.batches[0].operation_id}`, 'save:ready:1', 'finish']);
    expect(f.port.paused).not.toHaveBeenCalled();
    expect(f.pending).toBeNull();
  });

  it('awaits the original owner asynchronous pause persistence before returning an ACK or pause control', async () => {
    for (const afterFirst of [false, true]) {
      const initial = create();
      const f = fixture(
        afterFirst
          ? claimLiteratureReadingBulk(Object.freeze({ ...initial, paused: true }))
          : Object.freeze({ ...initial, paused: true })
      );
      let release!: () => void;
      const stored = new Promise<void>((resolve) => {
        release = resolve;
      });
      f.port.paused = vi.fn(async () => {
        await stored;
      });
      let settled = false;
      const result = runLiteratureReadingBulkFlow(f.pending!, f.port);
      const observed = result.then(
        (value) => {
          settled = true;
          return value;
        },
        (error) => {
          settled = true;
          throw error;
        }
      );
      for (let turn = 0; turn < 10 && !f.port.paused.mock.calls.length; turn++) await Promise.resolve();
      expect(f.port.paused).toHaveBeenCalledTimes(1);
      expect(settled).toBe(false);
      expect(f.port.send).toHaveBeenCalledTimes(afterFirst ? 1 : 0);
      release();
      if (afterFirst) expect(await observed).toEqual(ack(initial.batches[0]));
      else await expect(observed).rejects.toBeInstanceOf(BulkPausedError);
      expect(settled).toBe(true);
      expect(f.port.failed).not.toHaveBeenCalled();
      expect(f.port.finish).not.toHaveBeenCalled();
    }
  });

  it('keeps storage and supersession failures outside the request failure handler', async () => {
    for (const phase of ['sending', 'ready'] as const) {
      const initial = create(1);
      const f = fixture(initial);
      const storage = new Error(`storage ${phase}`);
      const save = f.port.save;
      f.port.save = vi.fn(async (expected, next, receipt) => {
        if (next.phase === phase) throw storage;
        await save(expected, next, receipt);
      });
      await expect(runLiteratureReadingBulkFlow(initial, f.port)).rejects.toBe(storage);
      expect(f.port.failed).not.toHaveBeenCalled();
      expect(f.port.send).toHaveBeenCalledTimes(phase === 'sending' ? 0 : 1);
      expect(f.pending?.phase).toBe(phase === 'sending' ? 'ready' : 'sending');
    }
    const initial = create(1);
    const f = fixture(initial);
    f.port.send.mockImplementationOnce(async (command) => {
      f.supersede();
      return ack(command);
    });
    await expect(runLiteratureReadingBulkFlow(initial, f.port)).rejects.toThrow('superseded');
    expect(f.port.failed).not.toHaveBeenCalled();
    expect(f.port.finish).not.toHaveBeenCalled();
    expect(f.pending).toMatchObject({ phase: 'sending', confirmed_batches: 0 });
  });
});
