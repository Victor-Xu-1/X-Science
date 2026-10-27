import { describe, expect, it, vi } from 'vitest';
import {
  loadXScienceAutoMemoryEnabled,
  loadXScienceMemoryContext,
  setXScienceAutoMemoryEnabled,
} from '@/renderer/services/xScienceMemory';

describe('Memory recovery preference transport', () => {
  it('decodes missing policy as strict and rejects unknown policy on the shared reads', async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json({ entities: [] }))
      .mockResolvedValueOnce(Response.json({ enabled: true }))
      .mockResolvedValueOnce(Response.json({ forked_view_recovery: 'any-error' }))
      .mockResolvedValueOnce(Response.json({ enabled: true, forked_view_recovery: null }));
    expect((await loadXScienceMemoryContext({}, { fetchImpl })).forkedViewRecovery).toBe('strict');
    expect(await loadXScienceAutoMemoryEnabled({ fetchImpl })).toBe(true);
    await expect(loadXScienceMemoryContext({}, { fetchImpl })).rejects.toThrow('recovery policy is invalid');
    await expect(loadXScienceAutoMemoryEnabled({ fetchImpl })).rejects.toThrow('recovery policy is invalid');
    expect(fetchImpl).toHaveBeenCalledTimes(4);
  });

  it('omits policy on enabled-only writes and sends an explicit choice through the original endpoint', async () => {
    const observed: unknown[] = [];
    const fetchImpl = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe('/api/memory/auto-enabled');
      expect(init?.method).toBe('PUT');
      const body = JSON.parse(String(init?.body));
      observed.push(body);
      return Response.json({
        enabled: body.enabled,
        forked_view_recovery: body.forked_view_recovery ?? 'compact-on-oversize',
      });
    });
    await setXScienceAutoMemoryEnabled(false, { fetchImpl });
    await setXScienceAutoMemoryEnabled(true, { fetchImpl, forkedViewRecovery: 'strict' });
    expect(observed).toEqual([{ enabled: false }, { enabled: true, forked_view_recovery: 'strict' }]);
  });
});
