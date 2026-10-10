import {
  clearSynonBiomedExpertProfileReads,
  loadSynonBiomedAgents,
} from '@/renderer/services/synonBiomedCapabilities';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  clearSynonBiomedExpertProfileReads();
  resetRendererAccountScopeForTest();
  vi.unstubAllGlobals();
});

const agentRow = {
  name: 'OPERON',
  displayName: 'General Research Assistant',
  description: 'Research',
  source: 'bundled',
  healthy: true,
  enabled: true,
  userHidden: false,
};

describe('X-Science expert profile shared reads', () => {
  it('coalesces concurrent default expert-profile reads and revalidates after settlement', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify([agentRow])));
    vi.stubGlobal('fetch', fetchImpl);

    const [first, second] = await Promise.all([loadSynonBiomedAgents(), loadSynonBiomedAgents()]);
    expect(first).toEqual(second);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    await loadSynonBiomedAgents();
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it('does not share isolated transports with the default map', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify([agentRow])));
    const customFetch = vi.fn(async () => new Response(JSON.stringify([agentRow])));
    vi.stubGlobal('fetch', fetchImpl);

    await Promise.all([
      loadSynonBiomedAgents(),
      loadSynonBiomedAgents({ fetchImpl: customFetch, baseUrl: 'http://127.0.0.1:1' }),
    ]);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(customFetch).toHaveBeenCalledTimes(1);
  });

  it('does not reuse in-flight reads across authenticated owners', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify([agentRow])));
    vi.stubGlobal('fetch', fetchImpl);

    setRendererAccountOwner('owner-a');
    const ownerA = loadSynonBiomedAgents();
    setRendererAccountOwner('owner-b');
    const ownerB = loadSynonBiomedAgents();

    await Promise.all([ownerA, ownerB]);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });
});
