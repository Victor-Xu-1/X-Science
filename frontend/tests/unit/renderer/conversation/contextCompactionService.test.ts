import { beforeEach, describe, expect, it, vi } from 'vitest';
import { httpRequest } from '@/common/adapter/httpBridge';
import {
  cancelContextCompaction,
  lookupContextCompaction,
  readContextCompaction,
  submitContextCompaction,
} from '@/renderer/services/compaction/contextCompactionService';
import {
  decodeCompactionRequest,
  decodeCompactionReceipt,
  decodeCompactionSnapshot,
} from '@/renderer/services/compaction/contextCompactionContract';
import {
  manualCompactionIdentity as identity,
  manualCompactionRequest,
  manualCompactionReceipt,
  manualCompactionSnapshot,
} from './manualContextCompactionFixtures';

vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: vi.fn() }));
const transport = vi.mocked(httpRequest);
const request = decodeCompactionRequest(manualCompactionRequest, identity);
const path = '/api/conversations/compaction-child/context-compactions';
beforeEach(() => {
  transport.mockReset();
});
describe('C11 typed context maintenance transport', () => {
  it('accepts Go-shaped numeric event coordinates for committed and cancelled receipts', () => {
    const committed = decodeCompactionReceipt(
      {
        ...manualCompactionReceipt(),
        state: 'committed',
        boundary_event_id: 41,
        terminal_event_id: 42,
        archive_status: 'available',
      },
      identity,
      request
    );
    expect(committed.boundary_event_id).toBe(41);
    expect(committed.terminal_event_id).toBe(42);
    const cancelled = decodeCompactionReceipt(
      { ...manualCompactionReceipt(), state: 'cancelled', cancellation_status: 'cancelled', terminal_event_id: 43 },
      identity,
      request
    );
    expect(cancelled.terminal_event_id).toBe(43);
  });
  it.each(['stream_uid', 'stream_epoch'] as const)('rejects a snapshot operation from a different %s', (key) => {
    const operation = manualCompactionReceipt();
    const changed = {
      ...operation,
      request: {
        ...operation.request,
        expected: { ...operation.request.expected, [key]: key === 'stream_uid' ? 'other-stream' : 3 },
      },
    };
    expect(() => decodeCompactionSnapshot({ ...manualCompactionSnapshot(), operation: changed }, identity)).toThrow();
  });
  it.each(['canonical-maintenance-cancelled', '42', 0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, null])(
    'rejects invalid event coordinates %s without coercion',
    (value) => {
      expect(() =>
        decodeCompactionReceipt(
          { ...manualCompactionReceipt(), state: 'committed', boundary_event_id: value },
          identity
        )
      ).toThrow();
      expect(() =>
        decodeCompactionReceipt(
          { ...manualCompactionReceipt(), state: 'cancelled', terminal_event_id: value },
          identity
        )
      ).toThrow();
    }
  );
  it('preserves the complete target and frozen original request through submit, lookup and cancellation', async () => {
    transport.mockResolvedValueOnce(manualCompactionSnapshot()).mockResolvedValue(manualCompactionReceipt());
    const snapshot = await readContextCompaction(identity);
    expect(snapshot.target).toEqual(request.expected);
    expect(Object.isFrozen(snapshot.target)).toBe(true);
    await submitContextCompaction(identity, request);
    await lookupContextCompaction(identity, request);
    await cancelContextCompaction(identity, request);
    expect(transport.mock.calls[1]).toEqual(['POST', path, request, expect.any(Object)]);
    const lookup = new URL(transport.mock.calls[2][1], 'http://localhost');
    expect(lookup.pathname).toBe(`${path}/by-request/${request.request_id}`);
    expect(Object.fromEntries(lookup.searchParams)).toEqual({
      expected_frame_incarnation_id: request.expected.frame_incarnation_id,
      expected_root_frame_id: request.expected.root_frame_id,
      expected_root_frame_incarnation_id: request.expected.root_frame_incarnation_id,
      expected_stream_uid: request.expected.stream_uid,
      expected_stream_epoch: '2',
    });
    expect(transport.mock.calls[3]).toEqual([
      'DELETE',
      `${path}/by-request/${request.request_id}`,
      request,
      expect.any(Object),
    ]);
  });
  it.each([
    'frame_incarnation_id',
    'root_frame_id',
    'root_frame_incarnation_id',
    'stream_uid',
    'stream_epoch',
    'branch_id',
    'branch_generation',
    'input_revision',
    'through_publication_seq',
  ])('rejects missing target %s', (key) => {
    const snapshot = manualCompactionSnapshot();
    delete (snapshot.target as Record<string, unknown>)[key];
    expect(() => decodeCompactionSnapshot(snapshot, identity)).toThrow();
  });
  it.each(['request_id', 'branch_generation', 'input_revision', 'through_publication_seq'])(
    'rejects receipt binding change %s',
    (key) => {
      const receipt = manualCompactionReceipt();
      if (key === 'request_id')
        receipt.request = { ...receipt.request, request_id: '03030303-0303-4303-8303-030303030303' };
      else receipt.request = { ...receipt.request, expected: { ...receipt.request.expected, [key]: 99 } };
      expect(() => decodeCompactionReceipt(receipt, identity, request)).toThrow();
    }
  );
  it('rejects foreign frame/root scopes and partial accepted receipts', () => {
    expect(() =>
      decodeCompactionSnapshot(manualCompactionSnapshot(), { ...identity, frameIncarnationId: 'replacement' })
    ).toThrow();
    expect(() =>
      decodeCompactionSnapshot(manualCompactionSnapshot(), { ...identity, rootFrameId: 'foreign-root' })
    ).toThrow();
    expect(() =>
      decodeCompactionReceipt({ ...manualCompactionReceipt(), input_message_id: undefined }, identity)
    ).toThrow();
    expect(() => decodeCompactionReceipt({ ...manualCompactionReceipt(), accepted: false }, identity)).toThrow();
  });
  it('keeps unavailable observation independent of safe admission and committed pending archive', () => {
    const receipt = decodeCompactionReceipt(
      {
        ...manualCompactionReceipt(),
        state: 'committed',
        execution_observation: 'unavailable',
        can_submit_next_input: true,
        archive_status: 'pending',
      },
      identity,
      request
    );
    expect(receipt.execution_observation).toBe('unavailable');
    expect(receipt.can_submit_next_input).toBe(true);
    expect(receipt.archive_status).toBe('pending');
    expect(() =>
      decodeCompactionReceipt({ ...manualCompactionReceipt(), archive_status: 'pending' }, identity)
    ).toThrow();
  });
  it('does not retry a lost mutation acknowledgement in transport', async () => {
    transport.mockRejectedValue(new Error('lost acknowledgement'));
    await expect(submitContextCompaction(identity, request)).rejects.toThrow('lost acknowledgement');
    expect(transport).toHaveBeenCalledTimes(1);
  });
});
