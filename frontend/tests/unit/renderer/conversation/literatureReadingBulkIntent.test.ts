import { describe, expect, it, vi } from 'vitest';
import {
  LITERATURE_READING_STATUS_BATCH_MAX,
  LITERATURE_READING_STATUS_BODY_BYTES,
  type LiteratureReadingStatusCommand,
} from '@/common/adapter/literatureReadingStatus';
import {
  advanceLiteratureReadingBulk,
  claimLiteratureReadingBulk,
  createLiteratureReadingBulk,
  currentLiteratureReadingBulkCommand,
  decodeLiteratureReadingBulk,
  isLiteratureReadingBulk,
  literatureReadingBulkProgress,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';

const factory = () => {
  let sequence = 0;
  return vi.fn(() => `00000000-0000-0000-0000-${(++sequence).toString(16).padStart(12, '0')}`);
};
const receipt = (command: LiteratureReadingStatusCommand) => ({
  schema: 'x-science.literature-reading-status-receipt.v1',
  operation_id: command.operation_id,
  item_ids: [...command.item_ids],
  reading_status: command.reading_status,
});
const wire = (command: LiteratureReadingStatusCommand) =>
  JSON.stringify({
    operation_id: command.operation_id,
    item_ids: command.item_ids,
    reading_status: command.reading_status,
  });
const plain = <T>(value: T): T => JSON.parse(JSON.stringify(value));

describe('Library reading bulk immutable intent', () => {
  it('keeps every target in first order, without a total-row cap or per-item UUIDs', () => {
    const ids = Array.from({ length: 1001 }, (_, i) => `paper-${i}`);
    const uuid = factory();
    const intent = createLiteratureReadingBulk([ids[0], ...ids, ids[3]], 'reading', 'project:all', uuid);
    expect(intent.batches.map((batch) => batch.item_ids.length)).toEqual([200, 200, 200, 200, 200, 1]);
    expect(intent.batches.flatMap((batch) => batch.item_ids)).toEqual(ids);
    expect(uuid).toHaveBeenCalledTimes(intent.batches.length + 1);
    expect(new Set([intent.operation_id, ...intent.batches.map((batch) => batch.operation_id)]).size).toBe(7);
    expect(Object.isFrozen(intent)).toBe(true);
    expect(Object.isFrozen(intent.batches)).toBe(true);
    expect(intent.batches.every((batch) => Object.isFrozen(batch) && Object.isFrozen(batch.item_ids))).toBe(true);
    ids[0] = 'changed-after-freeze';
    expect(intent.batches[0].item_ids[0]).toBe('paper-0');
    expect(intent.paused).toBe(false);
    expect(literatureReadingBulkProgress(intent)).toMatchObject({
      confirmedItems: 0,
      totalItems: 1001,
      totalBatches: 6,
    });
    expect(decodeLiteratureReadingBulk(JSON.parse(JSON.stringify(intent)))).toEqual(intent);
  });

  it('chunks by the real UTF-8 POST JSON size including escaped IDs', () => {
    const ids = Array.from({ length: 150 }, (_, i) => `${'界'.repeat(400)}-"\\-${i}`);
    const intent = createLiteratureReadingBulk(ids, 'read', 'query:unicode', factory());
    expect(intent.batches.length).toBeGreaterThan(1);
    for (let index = 0; index < intent.batches.length; index++) {
      const batch = intent.batches[index];
      expect(batch.item_ids.length).toBeLessThanOrEqual(LITERATURE_READING_STATUS_BATCH_MAX);
      expect(new TextEncoder().encode(wire(batch)).byteLength).toBeLessThanOrEqual(
        LITERATURE_READING_STATUS_BODY_BYTES
      );
      expect(new TextEncoder().encode(wire(batch)).byteLength).toBeGreaterThan(wire(batch).length);
      if (index + 1 < intent.batches.length) {
        const extended = { ...batch, item_ids: [...batch.item_ids, intent.batches[index + 1].item_ids[0]] };
        expect(new TextEncoder().encode(wire(extended)).byteLength).toBeGreaterThan(
          LITERATURE_READING_STATUS_BODY_BYTES
        );
      }
    }
    expect(intent.batches.flatMap((batch) => batch.item_ids)).toEqual(ids);
  });

  it('rejects malformed closed restores, reused identities and oversized child bodies', () => {
    const intent = createLiteratureReadingBulk(
      Array.from({ length: 201 }, (_, i) => `p-${i}`),
      'unread',
      'all',
      factory()
    );
    const invalid: unknown[] = [
      { ...plain(intent), unknown: true },
      { ...plain(intent), operation_id: 'bad' },
      { ...plain(intent), operation_id: intent.batches[0].operation_id },
      { ...plain(intent), batches: [] },
      { ...plain(intent), confirmed_batches: -1 },
      { ...plain(intent), confirmed_batches: 0.5 },
      { ...plain(intent), confirmed_batches: 3 },
      { ...plain(intent), phase: 'done' },
      { ...plain(intent), paused: undefined },
      { ...plain(intent), scope_key: 'bad\nquery' },
      { ...plain(intent), scope_key: '\ud800' },
      {
        ...plain(intent),
        batches: [intent.batches[0], { ...intent.batches[1], operation_id: intent.batches[0].operation_id }],
      },
      { ...plain(intent), batches: [{ ...intent.batches[0], item_ids: [] }] },
      { ...plain(intent), batches: [{ ...intent.batches[0], reading_status: 'read' }] },
      {
        ...plain(intent),
        batches: [intent.batches[0], { ...intent.batches[1], item_ids: [intent.batches[0].item_ids[0]] }],
      },
      {
        ...plain(intent),
        batches: [
          { ...intent.batches[0], item_ids: Array.from({ length: 200 }, (_, i) => `${'界'.repeat(400)}-${i}`) },
        ],
      },
    ];
    for (const value of invalid) expect(() => decodeLiteratureReadingBulk(value)).toThrow();
    expect(isLiteratureReadingBulk(intent)).toBe(true);
    expect(isLiteratureReadingBulk({ kind: 'set-reading-status' })).toBe(false);
    expect(() => createLiteratureReadingBulk(['ok', 'bad\0id'], 'read', 'all', factory())).toThrow();
  });

  it('advances only the sending child exact ACK and rechecks the final original UUID without recounting', () => {
    const initial = createLiteratureReadingBulk(
      Array.from({ length: 201 }, (_, i) => `p-${i}`),
      'read',
      'all',
      factory()
    );
    const first = currentLiteratureReadingBulkCommand(initial);
    expect(() => advanceLiteratureReadingBulk(initial, receipt(first))).toThrow();
    const sending = claimLiteratureReadingBulk(Object.freeze({ ...initial, paused: true }));
    expect(sending.paused).toBe(true);
    expect(claimLiteratureReadingBulk(sending)).toBe(sending);
    expect(() =>
      advanceLiteratureReadingBulk(sending, { ...receipt(first), operation_id: initial.batches[1].operation_id })
    ).toThrow();
    expect(() =>
      advanceLiteratureReadingBulk(sending, { ...receipt(first), item_ids: [...first.item_ids].reverse() })
    ).toThrow();
    const ready = advanceLiteratureReadingBulk(sending, receipt(first));
    expect(ready).toMatchObject({ confirmed_batches: 1, phase: 'ready', paused: true });
    expect(literatureReadingBulkProgress(ready)).toMatchObject({ confirmedItems: 200, totalItems: 201 });
    const second = currentLiteratureReadingBulkCommand(ready);
    const complete = advanceLiteratureReadingBulk(claimLiteratureReadingBulk(ready), receipt(second));
    const restored = decodeLiteratureReadingBulk(plain(complete));
    expect(currentLiteratureReadingBulkCommand(restored)).toEqual(second);
    expect(advanceLiteratureReadingBulk(claimLiteratureReadingBulk(restored), receipt(second)).confirmed_batches).toBe(
      2
    );
  });

  it('rejects UUID collisions without retrying the factory or altering the target plan', () => {
    const id = '00000000-0000-0000-0000-000000000001';
    const uuid = vi.fn(() => id);
    expect(() => createLiteratureReadingBulk(['a'], 'read', 'all', uuid)).toThrow();
    expect(uuid).toHaveBeenCalledTimes(2);
    const duplicateChild = vi.fn().mockReturnValueOnce(id).mockReturnValue('00000000-0000-0000-0000-000000000002');
    expect(() =>
      createLiteratureReadingBulk(
        Array.from({ length: 201 }, (_, i) => `${i}`),
        'read',
        'all',
        duplicateChild
      )
    ).toThrow();
    expect(duplicateChild).toHaveBeenCalledTimes(3);
  });
});
