import { describe, expect, it, vi } from 'vitest';
import { loadXScienceKernels } from '@/renderer/services/xScienceNotebook';
import {
  freezeManualNotebookRequest,
  normalizeManualNotebookScope,
} from '@/renderer/services/notebook/manualNotebookContract';
import {
  cancelManualNotebook,
  discoverManualNotebook,
  lookupManualNotebook,
  submitManualNotebook,
} from '@/renderer/services/notebook/manualNotebookService';
import {
  inventory,
  pendingPage,
  rawScope,
  receipt,
  request,
  response,
  summary,
  target,
} from './manualNotebookFixtures';

describe('typed manual Notebook transport', () => {
  it('freezes exact source and uses one unchanged body for submit and accepted cancellation', async () => {
    const frozen = freezeManualNotebookRequest(target, '  print("测量")\n', 600000, request.request_id);
    expect(Object.isFrozen(frozen)).toBe(true);
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(response(receipt(frozen)))
      .mockResolvedValueOnce(response(receipt(frozen, 'cancel_requested')));
    await submitManualNotebook(target, frozen, { fetchImpl });
    const cancelled = await cancelManualNotebook(target, frozen, { fetchImpl });
    expect(cancelled.state).toBe('cancel_requested');
    expect(cancelled.finishedAt).toBeNull();
    expect(fetchImpl.mock.calls[0][0]).toBe('/api/frames/frame-1/kernel-executions');
    expect(fetchImpl.mock.calls[1][0]).toBe(
      '/api/frames/frame-1/kernel-executions/by-request/' + request.request_id + '/cancel'
    );
    expect(fetchImpl.mock.calls.map(([, init]) => init.body)).toEqual([JSON.stringify(frozen), JSON.stringify(frozen)]);
  });

  it('reads with all original target fields and never places code in a lookup URL', async () => {
    const fetchImpl = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => response(receipt()));
    await lookupManualNotebook(target, request.request_id, request, { fetchImpl });
    const url = new URL(String(fetchImpl.mock.calls[0][0]), 'http://localhost');
    expect([...url.searchParams.keys()].toSorted()).toEqual([
      'environment',
      'expected_frame_incarnation_id',
      'expected_kernel_generation',
      'expected_root_frame_id',
      'expected_root_frame_incarnation_id',
      'kernel_id',
      'language',
    ]);
    expect(url.searchParams.get('expected_kernel_generation')).toBe('7');
    expect(url.toString()).not.toContain('unchanged');
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('rejects a changed frozen request instead of accepting a successful-looking receipt', async () => {
    const changed = receipt({ ...request, code: 'different code' });
    const fetchImpl = vi.fn(async () => response(changed));
    await expect(lookupManualNotebook(target, request.request_id, request, { fetchImpl })).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('keeps old generation and legacy recovery read-only without reconstructing a typed request', async () => {
    const old = { ...request, expected_kernel_generation: 3 };
    const oldTarget = { ...target, kernelGeneration: 3 };
    const fetchImpl = vi.fn(async () => response(receipt(old, 'queued', 'legacy')));
    const recovered = await lookupManualNotebook(oldTarget, old.request_id, undefined, { fetchImpl });
    expect(recovered.requestKind).toBe('legacy');
    expect(recovered.request).toBeNull();
    expect(recovered.target.kernelGeneration).toBe(3);
    expect(recovered.source).toBe(request.code);
    const forged = { ...receipt(old, 'queued', 'legacy'), request: old };
    await expect(
      lookupManualNotebook(oldTarget, old.request_id, undefined, {
        fetchImpl: vi.fn(async () => response(forged)),
      })
    ).rejects.toThrow();
  });

  it('preserves empty incomplete scan pages and original legacy/old-generation targets', async () => {
    const scope = normalizeManualNotebookScope(rawScope)!;
    const old = { ...request, expected_kernel_generation: 3 };
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(response(pendingPage([], 'fixed-cursor')))
      .mockResolvedValueOnce(response(pendingPage([summary(old, 'legacy')])));
    const first = await discoverManualNotebook(scope, null, { fetchImpl });
    expect(first).toMatchObject({ pending: [], scanComplete: false, nextCursor: 'fixed-cursor' });
    const second = await discoverManualNotebook(scope, first.nextCursor, { fetchImpl });
    expect(second.pending[0]).toMatchObject({ requestKind: 'legacy', target: { kernelGeneration: 3 } });
    expect(new URL(fetchImpl.mock.calls[1][0], 'http://localhost').searchParams.get('cursor')).toBe('fixed-cursor');
    expect(fetchImpl.mock.calls.every(([, init]) => !init.method || init.method === 'GET')).toBe(true);
  });

  it('exposes a verified empty-kernel scope but rejects a response for another actual frame', async () => {
    expect(
      await loadXScienceKernels('frame-1', { fetchImpl: vi.fn(async () => response(inventory([]))) })
    ).toMatchObject({ kernels: [], manualExecutionScope: { frameId: 'frame-1', frameIncarnationId: 'frame-inc-1' } });
    await expect(
      loadXScienceKernels('different-frame', { fetchImpl: vi.fn(async () => response(inventory([]))) })
    ).rejects.toThrow();
  });
});
