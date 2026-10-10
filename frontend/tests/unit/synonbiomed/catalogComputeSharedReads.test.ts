import {
  clearSynonBiomedCatalogReads,
  fetchSynonBiomedCatalog,
  loadSynonBiomedAssistants,
} from '@/renderer/services/synonBiomedCatalog';
import {
  clearSynonBiomedComputeReads,
  loadSynonBiomedComputeProviders,
  loadSynonBiomedSessionComputeProviders,
} from '@/renderer/services/synonBiomedCompute';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  clearSynonBiomedCatalogReads();
  clearSynonBiomedComputeReads();
  resetRendererAccountScopeForTest();
  vi.unstubAllGlobals();
});

const catalogBody = {
  product: 'X-Science',
  runtime: {
    runtimeAssetsDir: '/runtime',
    agents: { count: 1, names: ['OPERON'] },
    skills: { count: 0, names: [] },
    mcpServers: { count: 0, names: [] },
    thirdPartyAssets: { count: 0, names: [] },
    seedProjects: { count: 0, projects: [] },
  },
  backend: {
    baseUrl: 'http://127.0.0.1:1',
    health: { status: 'healthy', service: 'x-science' },
    agents: { count: 1, names: ['OPERON'] },
    projects: { count: 0, projects: [] },
  },
};

const expertProfiles = [
  {
    name: 'OPERON',
    displayName: 'General Research Assistant',
    description: 'Research',
    source: 'bundled',
    healthy: true,
    enabled: true,
    userHidden: false,
  },
];

describe('X-Science catalog and compute shared reads', () => {
  it('coalesces concurrent default catalog reads and revalidates after settlement', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(catalogBody)));
    vi.stubGlobal('fetch', fetchImpl);

    const [first, second] = await Promise.all([fetchSynonBiomedCatalog(), fetchSynonBiomedCatalog()]);
    expect(first).toEqual(second);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    await fetchSynonBiomedCatalog();
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it('keeps catalog and assistant reads distinct while coalescing each kind', async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/synonbiomed/catalog') return new Response(JSON.stringify(catalogBody));
      if (url === '/api/synonbiomed/expert-profiles') return new Response(JSON.stringify(expertProfiles));
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal('fetch', fetchImpl);

    const [catalogA, catalogB, assistantsA, assistantsB] = await Promise.all([
      fetchSynonBiomedCatalog(),
      fetchSynonBiomedCatalog(),
      loadSynonBiomedAssistants(),
      loadSynonBiomedAssistants(),
    ]);

    expect(catalogA).toEqual(catalogB);
    expect(assistantsA).toEqual(assistantsB);
    expect(assistantsA[0]?.agent_id).toBe('OPERON');
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it('does not share a custom catalog transport with the default map', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(catalogBody)));
    const customFetch = vi.fn(async () => new Response(JSON.stringify(catalogBody)));
    vi.stubGlobal('fetch', fetchImpl);

    await Promise.all([fetchSynonBiomedCatalog(), fetchSynonBiomedCatalog(customFetch)]);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(customFetch).toHaveBeenCalledTimes(1);
  });

  it('coalesces concurrent compute provider and session-provider reads', async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/compute/providers') {
        return new Response(JSON.stringify([{ name: 'local', displayName: 'Local', family: 'local', checked: true }]));
      }
      if (url === '/api/compute/session/frame-1/enabled') {
        return new Response(JSON.stringify(['local']));
      }
      if (url === '/api/compute/session/frame-2/enabled') {
        return new Response(JSON.stringify(['ssh:hpc']));
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal('fetch', fetchImpl);

    const [providersA, providersB, sessionA, sessionB, otherSession] = await Promise.all([
      loadSynonBiomedComputeProviders(),
      loadSynonBiomedComputeProviders(),
      loadSynonBiomedSessionComputeProviders('frame-1'),
      loadSynonBiomedSessionComputeProviders(' frame-1 '),
      loadSynonBiomedSessionComputeProviders('frame-2'),
    ]);

    expect(providersA).toEqual(providersB);
    expect(sessionA).toEqual(sessionB);
    expect(sessionA).toEqual(['local']);
    expect(otherSession).toEqual(['ssh:hpc']);
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it('does not reuse in-flight catalog or compute reads across owners', async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/synonbiomed/catalog') return new Response(JSON.stringify(catalogBody));
      if (url === '/api/compute/providers') return new Response(JSON.stringify([]));
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal('fetch', fetchImpl);

    setRendererAccountOwner('owner-a');
    const catalogA = fetchSynonBiomedCatalog();
    const providersA = loadSynonBiomedComputeProviders();
    setRendererAccountOwner('owner-b');
    const catalogB = fetchSynonBiomedCatalog();
    const providersB = loadSynonBiomedComputeProviders();

    await Promise.all([catalogA, providersA, catalogB, providersB]);
    expect(fetchImpl).toHaveBeenCalledTimes(4);
  });
});
