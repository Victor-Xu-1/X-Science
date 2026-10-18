import { beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeDelegatedStopReceipt,
  decodeDelegatedStopRequest,
  decodeDelegatedStopSnapshot,
} from '@/renderer/services/runtime/delegatedStopContract';
import {
  readDelegatedStopReceipt,
  readDelegatedStopSnapshot,
  submitDelegatedStop,
} from '@/renderer/services/runtime/delegatedStopTransport';
import { request, snapshot, receipt } from './delegatedStopFixtures';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => {
  const actual = await original<typeof import('@/common/adapter/httpBridge')>();
  return { ...actual, httpRequest: mocks.http };
});
describe('delegated Stop typed transport', () => {
  beforeEach(() => {
    mocks.http.mockReset();
  });
  it('preserves the full exact scope and target and rejects display-only capability guesses', () => {
    expect(decodeDelegatedStopSnapshot(snapshot)).toEqual(snapshot);
    expect(() =>
      decodeDelegatedStopSnapshot({ ...snapshot, targets: [{ ...snapshot.targets[0], target: null }] })
    ).toThrow();
    const legacy = {
      ...snapshot,
      can_stop_branch: false,
      targets: [{ ...snapshot.targets[0], origin: 'legacy-unavailable', can_stop: false, target: null }],
    };
    expect(decodeDelegatedStopSnapshot(legacy).targets[0].can_stop).toBe(false);
    expect(() => decodeDelegatedStopRequest({ ...request, mode: 'child' })).toThrow();
    expect(() => decodeDelegatedStopRequest({ ...request, target: snapshot.targets[0].target })).toThrow();
  });
  it('checks all frozen request coordinates before accepting a receipt', () => {
    expect(decodeDelegatedStopReceipt(receipt(), request).request).toEqual(request);
    for (const changed of [
      { ...request, request_id: '20a994ad-78d0-4ee3-98d7-650042a9a599' },
      { ...request, scope: { ...request.scope, root_frame_incarnation_id: 'new-root' } },
      { ...request, branch: { ...request.branch, generation: 4 } },
      { ...request, expected_fence_generation: 1 },
    ])
      expect(() => decodeDelegatedStopReceipt(receipt(changed), request)).toThrow();
  });
  it('uses only scoped snapshot, POST and exact by-request paths', async () => {
    mocks.http.mockResolvedValueOnce(snapshot).mockResolvedValueOnce(receipt()).mockResolvedValueOnce(receipt());
    await readDelegatedStopSnapshot('stop-parent', snapshot.branch.branch_id);
    await submitDelegatedStop(request);
    await readDelegatedStopReceipt(request);
    expect(mocks.http.mock.calls.map((call) => call.slice(0, 3))).toEqual([
      ['GET', '/api/frames/stop-parent/delegated-stops?branch_id=br_11111111', undefined],
      ['POST', '/api/frames/stop-parent/delegated-stops', request],
      ['GET', '/api/frames/stop-parent/delegated-stops/' + request.request_id, undefined],
    ]);
  });
  it('keeps receipt 404 as an error without POST or root fallback', async () => {
    const unavailable = new BackendHttpError({
      method: 'GET',
      path: '/receipt',
      status: 404,
      body: { code: 'DELEGATED_STOP_UNAVAILABLE', message: 'not found' },
    });
    mocks.http.mockRejectedValue(unavailable);
    await expect(readDelegatedStopReceipt(request)).rejects.toBe(unavailable);
    expect(mocks.http).toHaveBeenCalledTimes(1);
    expect(mocks.http.mock.calls[0][0]).toBe('GET');
  });
  it('retains unavailable physical observation independently from durable cancellation', () => {
    const original = receipt();
    const settled = {
      ...original,
      state: 'settled',
      targets: [{ ...original.targets[0], outcome: 'cancelled', execution_observation: 'unavailable' }],
    };
    const parsed = decodeDelegatedStopReceipt(settled, request);
    expect(parsed.state).toBe('settled');
    expect(parsed.targets[0].execution_observation).toBe('unavailable');
  });
  it('accepts authenticated descendant outcomes only with the original child target exactly once', () => {
    const childRequest = decodeDelegatedStopRequest({ ...request, mode: 'child', target: snapshot.targets[0].target });
    const original = receipt(childRequest);
    const child = original.targets[0];
    const descendant = {
      ...child,
      target: {
        ...child.target,
        frame_id: 'owned-descendant',
        frame_incarnation_id: 'descendant-inc',
        stream_uid: 'descendant-stream',
        input_message_id: 'descendant-input',
      },
    };
    const nested = {
      ...descendant,
      target: {
        ...descendant.target,
        frame_id: 'owned-nested-descendant',
        frame_incarnation_id: 'nested-inc',
        stream_uid: 'nested-stream',
        input_message_id: 'nested-input',
      },
    };
    const canonicalTargets = [descendant, child, nested];
    const parsed = decodeDelegatedStopReceipt({ ...original, targets: canonicalTargets }, childRequest);
    expect(parsed.request).toEqual(childRequest);
    expect(parsed.targets.map((row) => row.target)).toEqual(canonicalTargets.map((row) => row.target));
    expect(parsed.request.target).toEqual(child.target);
    for (const targets of [
      [descendant, nested],
      [{ ...child, target: { ...child.target, frame_incarnation_id: 'replacement-inc' } }, descendant],
      [{ ...child, target: { ...child.target, runner_attempt: child.target.runner_attempt + 1 } }, descendant],
      [child, child, descendant],
      [child, descendant, descendant],
      [child, { ...descendant, target: { ...descendant.target, runner_attempt: -1 } }],
      [child, { ...descendant, target: { ...descendant.target, injected_scope: 'other' } }],
      [child, { ...descendant, outcome: 'stopped' }],
    ])
      expect(() => decodeDelegatedStopReceipt({ ...original, targets }, childRequest)).toThrow();
  });
});
