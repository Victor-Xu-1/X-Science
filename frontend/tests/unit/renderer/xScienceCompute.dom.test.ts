import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  decodeSessionComputeState,
  loadXScienceSessionComputeState,
} from '@/renderer/services/runtime/computeSessionState';
import {
  addXScienceInferenceProvider,
  addXScienceSshHost,
  deleteXScienceComputeProvider,
  getXScienceLocalFileUrl,
  getXScienceRemoteFileUrl,
  importXScienceLocalFile,
  importXScienceRemoteFile,
  loadXScienceLocalDirectory,
  loadXScienceLocalHostInfo,
  loadXScienceRemoteDirectory,
  loadXScienceComputeGpuEnabled,
  loadXScienceComputeGpuInfo,
  loadXScienceComputeJob,
  loadXScienceComputeJobLog,
  loadXScienceComputeJobs,
  loadXScienceBioNemoSettings,
  loadXScienceModalSettings,
  loadXScienceSessionComputeProviders,
  loadXScienceManagedEndpoints,
  loadXScienceComputeProvider,
  loadXScienceComputeProviders,
  loadXScienceSshAliases,
  probeXScienceComputeProvider,
  saveXScienceComputeProviderDetails,
  setXScienceComputeGpuEnabled,
  setXScienceBioNemoSettings,
  setXScienceModalEnabled,
  setXScienceSessionComputeProvider,
  stopXScienceManagedEndpoint,
} from '@/renderer/services/xScienceCompute';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('C394 state projection strict read contract', () => {
  const inherited = {
    enabled: ['ssh:fixture'],
    configured: false,
    selectionSource: 'inherited',
    softwareRuntime: { kind: 'local_default' },
  };
  it('preserves inherited access and explicit policy evidence without inventing a software target', async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(Response.json(inherited));
    await expect(loadXScienceSessionComputeState('root', transport)).resolves.toEqual(inherited);
    expect(transport).toHaveBeenCalledTimes(1);
    expect(transport.mock.calls[0][0]).toBe('/api/compute/session/root/state');
    expect(transport.mock.calls[0][1]).toMatchObject({ credentials: 'include' });
    for (const [enabled, policy] of [
      [[], { kind: 'local_explicit' }],
      [['ssh:a', 'ssh:b'], { kind: 'invalid_selection' }],
      [['ssh:a'], { kind: 'remote_unsupported', providerName: 'ssh:a' }],
      [['ssh:missing'], { kind: 'unavailable' }],
    ] as const) {
      const state = { enabled: [...enabled], configured: true, selectionSource: 'explicit', softwareRuntime: policy };
      expect(decodeSessionComputeState(state)).toEqual(state);
    }
  });
  it('rejects malformed or contradictory fields without exposing private response data', () => {
    for (const value of [
      null,
      [],
      { ...inherited, configured: 'false' },
      { ...inherited, selectionSource: 'explicit' },
      { ...inherited, enabled: ['ssh:a', 'ssh:a'] },
      { ...inherited, softwareRuntime: { kind: 'REMOTE_PRIVATE_SENTINEL' } },
      { ...inherited, softwareRuntime: { kind: 'local_default', providerName: 'PRIVATE_SENTINEL' } },
      {
        enabled: ['ssh:a'],
        configured: true,
        selectionSource: 'explicit',
        softwareRuntime: { kind: 'remote_unsupported', providerName: null },
      },
    ]) {
      expect(() => decodeSessionComputeState(value)).toThrow('X-Science returned an invalid session compute state');
    }
  });
  it.each(['fetch', 'body'] as const)('bounds the new state %s read at15s without a legacy fallback', async (phase) => {
    vi.useFakeTimers();
    let release!: () => void;
    const transport = vi.fn<typeof fetch>().mockImplementation((_url, init) => {
      const pending = new Promise<Response | string>((resolve, reject) => {
        release = () => resolve(phase === 'fetch' ? Response.json(inherited) : JSON.stringify(inherited));
        init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true });
      });
      return phase === 'fetch'
        ? (pending as Promise<Response>)
        : Promise.resolve({ ok: true, status: 200, text: () => pending as Promise<string> } as Response);
    });
    const result = loadXScienceSessionComputeState('root', transport).catch((error: unknown) => error);
    try {
      await vi.advanceTimersByTimeAsync(15_000);
      await expect(result).resolves.toMatchObject({ name: 'TimeoutError' });
      expect(transport).toHaveBeenCalledTimes(1);
      expect(transport.mock.calls[0][0]).toBe('/api/compute/session/root/state');
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      release();
      await result;
      vi.useRealTimers();
    }
  });
  it('keeps HTTP read failure as failure and never falls back to the old enabled array', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValue(Response.json({ error: 'PRIVATE_SENTINEL' }, { status: 503 }));
    await expect(loadXScienceSessionComputeState('root', transport)).rejects.toMatchObject({ status: 503 });
    expect(transport).toHaveBeenCalledTimes(1);
    expect(transport.mock.calls[0][0]).toBe('/api/compute/session/root/state');
  });
});

describe('C394 control deadline', () => {
  const controls = [
    { label: 'catalog', call: (fetchImpl: typeof fetch) => loadXScienceComputeProviders(fetchImpl) },
    {
      label: 'session',
      call: (fetchImpl: typeof fetch) => loadXScienceSessionComputeProviders('fixture-root', fetchImpl),
    },
    {
      label: 'selection PUT',
      call: (fetchImpl: typeof fetch) =>
        setXScienceSessionComputeProvider('fixture-root', 'ssh:fixture', true, fetchImpl),
    },
  ];
  for (const control of controls) {
    it.each(['fetch', 'body'] as const)(`${control.label} bounds a pending %s without replay`, async (phase) => {
      vi.useFakeTimers();
      let release!: () => void;
      let outcome: unknown = 'pending';
      const fetchMock = vi.fn<typeof fetch>().mockImplementation((_url, init) => {
        const pending = new Promise<Response | string>((resolve, reject) => {
          release = () => resolve(phase === 'fetch' ? new Response('[]') : '[]');
          init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true });
        });
        return phase === 'fetch'
          ? (pending as Promise<Response>)
          : Promise.resolve({ ok: true, status: 200, text: () => pending as Promise<string> } as Response);
      });
      const operation = control.call(fetchMock).then(
        (value) => {
          outcome = value;
        },
        (error: unknown) => {
          outcome = error;
        }
      );
      try {
        await vi.advanceTimersByTimeAsync(15_000);
        expect(outcome).toMatchObject({ name: 'TimeoutError' });
        expect(fetchMock).toHaveBeenCalledTimes(1);
        expect(fetchMock.mock.calls[0][1]?.signal?.aborted).toBe(true);
        expect(vi.getTimerCount()).toBe(0);
      } finally {
        release();
        await operation;
        vi.useRealTimers();
      }
    });
    it(`${control.label} cleans a completed request deadline without changing its result`, async () => {
      vi.useFakeTimers();
      try {
        const fetchMock = vi
          .fn<typeof fetch>()
          .mockResolvedValue(
            control.label === 'selection PUT' ? new Response(null, { status: 204 }) : Response.json([])
          );
        await expect(control.call(fetchMock)).resolves.toEqual(control.label === 'selection PUT' ? undefined : []);
        const init = fetchMock.mock.calls[0][1];
        expect(init).toMatchObject({
          credentials: 'include',
          headers: expect.objectContaining({ Accept: 'application/json' }),
        });
        expect(init?.signal).toBeInstanceOf(AbortSignal);
        expect(init?.signal?.aborted).toBe(false);
        expect(vi.getTimerCount()).toBe(0);
        await vi.advanceTimersByTimeAsync(16_000);
        expect(init?.signal?.aborted).toBe(false);
        expect(fetchMock).toHaveBeenCalledTimes(1);
      } finally {
        vi.useRealTimers();
      }
    });
  }
  it.each(['HTTP', 'invalid JSON'] as const)(
    'cleans the deadline after %s failure and keeps a safe summary',
    async (kind) => {
      vi.useFakeTimers();
      try {
        const fetchMock = vi
          .fn<typeof fetch>()
          .mockResolvedValue(
            kind === 'HTTP'
              ? Response.json({ detail: 'PRIVATE_COMPUTE_DETAIL' }, { status: 503 })
              : new Response('{PRIVATE_COMPUTE_PARSE_TEXT')
          );
        const error = await loadXScienceComputeProviders(fetchMock).catch((failure: unknown) => failure);
        expect(error).toBeInstanceOf(Error);
        expect((error as Error).message).not.toContain('PRIVATE_COMPUTE');
        if (kind === 'HTTP')
          expect(error).toMatchObject({
            status: 503,
            backendMessage: 'PRIVATE_COMPUTE_DETAIL',
            body: { detail: 'PRIVATE_COMPUTE_DETAIL' },
          });
        else
          expect((error as Error).message).toBe('X-Science compute API returned invalid JSON: /api/compute/providers');
        expect(vi.getTimerCount()).toBe(0);
        await vi.advanceTimersByTimeAsync(16_000);
        expect(fetchMock.mock.calls[0][1]?.signal?.aborted).toBe(false);
        expect(fetchMock).toHaveBeenCalledTimes(1);
      } finally {
        vi.useRealTimers();
      }
    }
  );
});

describe('compute GET collection boundary', () => {
  const provider = { name: 'ssh:fixture-host', displayName: 'Fixture host', family: 'ssh', checked: true };
  const reads = [
    {
      name: 'catalog',
      read: loadXScienceComputeProviders,
      error: 'invalid compute provider list',
    },
    {
      name: 'session',
      read: (fetchImpl: typeof fetch) => loadXScienceSessionComputeProviders('fixture-root', fetchImpl),
      error: 'invalid session compute provider list',
    },
  ];
  const nonArrays: Array<{ label: string; payload: unknown }> = [
    { label: 'object wrapper', payload: { providers: [] } },
    { label: 'null', payload: null },
    { label: 'number', payload: 42 },
    { label: 'encoded array string', payload: '["ssh:fixture-host"]' },
    { label: 'empty response body', payload: undefined },
  ];

  for (const { name, read, error } of reads) {
    it.each(nonArrays)(`${name} rejects a successful $label instead of an empty list`, async ({ payload }) => {
      const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload)));
      await expect(read(fetchMock)).rejects.toThrow(error);
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it(`${name} preserves a legitimate empty array`, async () => {
      const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(Response.json([]));
      await expect(read(fetchMock)).resolves.toEqual([]);
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it(`${name} retains HTTP status and structured detail without leaking it through the summary`, async () => {
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockResolvedValue(Response.json({ detail: 'synthetic unavailable' }, { status: 503 }));
      const request = read(fetchMock);
      await expect(request).rejects.toMatchObject({
        status: 503,
        backendMessage: 'synthetic unavailable',
        body: { detail: 'synthetic unavailable' },
      });
      await request.catch((error: Error) => {
        expect(error.message).not.toContain('synthetic unavailable');
        expect(error.message).toContain('failed (503)');
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it(`${name} retains the original invalid JSON error`, async () => {
      const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response('{'));
      await expect(read(fetchMock)).rejects.toThrow('compute API returned invalid JSON');
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  }

  it.each([null, 42, {}, { name: '' }, { name: 7 }])(
    'catalog rejects an adapter-invalid member %j without returning a partial list',
    async (invalid) => {
      const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(Response.json([provider, invalid]));
      await expect(loadXScienceComputeProviders(fetchMock)).rejects.toThrow('invalid compute provider list');
      expect(fetchMock).toHaveBeenCalledTimes(1);
    }
  );

  it.each([null, 42, ''])('session rejects an invalid member %j without returning a partial list', async (invalid) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(Response.json(['ssh:fixture-host', invalid]));
    await expect(loadXScienceSessionComputeProviders('fixture-root', fetchMock)).rejects.toThrow(
      'invalid session compute provider list'
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('retains current provider adaptation and optional-field string-array compatibility', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(Response.json([provider, { name: 'extension:fixture', dataRoots: '["/fixture-data"]' }]));
    await expect(loadXScienceComputeProviders(fetchMock)).resolves.toMatchObject([
      provider,
      {
        name: 'extension:fixture',
        displayName: 'fixture',
        family: 'extension',
        checked: false,
        dataRoots: ['/fixture-data'],
      },
    ]);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('retains enabled identity order, duplicates and unknown providers without a new policy', async () => {
    const values = ['extension:fixture-b', 'ssh:fixture-host', 'ssh:fixture-host'];
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(Response.json(values));
    await expect(loadXScienceSessionComputeProviders('fixture-root', fetchMock)).resolves.toEqual(values);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe('X-Science compute service', () => {
  it('loads provider summaries, details and SSH aliases from the native compute API', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify([
            {
              name: 'ssh:hpc-a',
              displayName: 'HPC A',
              family: 'ssh',
              checked: true,
              scratchRoot: '/scratch/victor',
              dataRoots: ['/data/shared'],
              maxConcurrentJobs: 4,
            },
          ])
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            name: 'ssh:hpc-a',
            displayName: 'HPC A',
            family: 'ssh',
            checked: true,
            detailsMd: '## Cluster\nSlurm cluster',
            scratchRoot: '/scratch/victor',
            scratchRootSource: 'user',
            dataRoots: ['/data/shared'],
            maxConcurrentJobs: 4,
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            aliases: [{ alias: 'hpc-a', hostName: 'hpc.example.org', user: 'victor' }],
            configFound: true,
            configPath: '/home/victor_1/.ssh/config',
            wildcardCount: 0,
            isWsl: true,
          })
        )
      );
    vi.stubGlobal('fetch', fetchMock);

    const providers = await loadXScienceComputeProviders();
    const details = await loadXScienceComputeProvider('ssh:hpc-a');
    const aliases = await loadXScienceSshAliases();

    expect(providers[0]).toMatchObject({
      name: 'ssh:hpc-a',
      scratchRoot: '/scratch/victor',
      dataRoots: ['/data/shared'],
      maxConcurrentJobs: 4,
    });
    expect(details).toMatchObject({ detailsMd: '## Cluster\nSlurm cluster', scratchRootSource: 'user' });
    expect(aliases.aliases[0]).toMatchObject({ alias: 'hpc-a', hostName: 'hpc.example.org' });
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/compute/providers/ssh%3Ahpc-a',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      '/api/compute/ssh-config-aliases',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
  });

  it('maps the native Modal and BioNeMo family settings without legacy UI adapters', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            provider: 'modal',
            enabled: true,
            detailsMd: 'Modal notes',
            appName: 'xscience-research',
            environmentName: 'main',
            egressPolicy: { mode: 'allowlist', mirror: true, additional: ['api.example.com'] },
            maxConcurrentJobs: 10,
            maxTimeoutSec: 43200,
            profiles: [{ name: 'X-Science (stored)', active: true, tokenIdMasked: 'ak-a····test' }],
            ignoredProfiles: ['legacy'],
            tomlMissing: true,
            hasStoredCredential: true,
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ enabled: true, override: true, mode: 'hosted', hostedHost: 'health.api.nvidia.com' })
        )
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ enabled: false, override: false, mode: 'local', hostedHost: 'health.api.nvidia.com' })
        )
      );
    vi.stubGlobal('fetch', fetchMock);

    await expect(loadXScienceModalSettings()).resolves.toMatchObject({
      enabled: true,
      appName: 'xscience-research',
      maxTimeoutSec: 43200,
      profiles: [{ name: 'X-Science (stored)', active: true }],
    });
    await expect(loadXScienceBioNemoSettings()).resolves.toEqual({
      enabled: true,
      override: true,
      mode: 'hosted',
      hostedHost: 'health.api.nvidia.com',
    });
    await setXScienceModalEnabled(false);
    await expect(
      setXScienceBioNemoSettings({ enabled: false, mode: 'local', hostedHost: 'health.api.nvidia.com' })
    ).resolves.toEqual({ enabled: false, override: false, mode: 'local', hostedHost: 'health.api.nvidia.com' });

    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      '/api/compute/byoc/modal/enabled',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ enabled: false }) })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      '/api/compute/bionemo/enabled',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ enabled: false, mode: 'local', hostedHost: 'health.api.nvidia.com' }),
      })
    );
  });

  it('uses the exact X-Science mutation contracts for provider lifecycle operations', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ warning: 'credential is not saved' })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ scheduler: 'none', cpus: 0, gpus: 0 })))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await addXScienceSshHost({
      alias: 'hpc-a',
      initialContext: 'Slurm cluster',
      dataRoots: ['/data/shared'],
      overrides: { user: 'victor', port: 2222, identityFile: '~/.ssh/id_ed25519' },
    });
    await addXScienceInferenceProvider({
      name: 'boltz-local',
      endpoint: 'http://127.0.0.1:9000',
      skillName: 'boltz2-nim',
      credentialName: 'NVIDIA_API_KEY',
    });
    await probeXScienceComputeProvider('infer:boltz-local');
    await saveXScienceComputeProviderDetails('ssh:hpc-a', {
      detailsMd: '## Cluster\nUpdated',
      maxConcurrentJobs: 8,
      scratchRoot: '/scratch/victor',
      dataRoots: ['/data/shared', '/datasets'],
    });
    await deleteXScienceComputeProvider({ name: 'ssh:hpc-a', family: 'ssh' });
    await deleteXScienceComputeProvider({ name: 'infer:boltz-local', family: 'infer' });

    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      alias: 'hpc-a',
      initialContext: 'Slurm cluster',
      dataRoots: ['/data/shared'],
      overrides: { user: 'victor', port: 2222, identityFile: '~/.ssh/id_ed25519' },
    });
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/compute/inference-providers');
    expect(fetchMock.mock.calls[2]?.[0]).toBe('/api/compute/providers/infer%3Aboltz-local/probe');
    expect(fetchMock.mock.calls[3]).toEqual([
      '/api/compute/providers/ssh%3Ahpc-a',
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ detailsMd: '## Cluster\nUpdated', maxConcurrentJobs: 8 }),
      }),
    ]);
    expect(fetchMock.mock.calls[4]).toEqual([
      '/api/compute/providers/ssh%3Ahpc-a/scratch-root',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ scratchRoot: '/scratch/victor' }) }),
    ]);
    expect(fetchMock.mock.calls[5]).toEqual([
      '/api/compute/providers/ssh%3Ahpc-a/data-roots',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ roots: ['/data/shared', '/datasets'] }) }),
    ]);
    expect(fetchMock.mock.calls[6]?.[0]).toBe('/api/compute/providers/ssh%3Ahpc-a');
    expect(fetchMock.mock.calls[7]?.[0]).toBe('/api/compute/inference-providers/infer%3Aboltz-local');
  });

  it('surfaces backend validation details instead of replacing them with a generic error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>().mockResolvedValue(
        new Response(JSON.stringify({ detail: "alias 'missing' must be defined in ~/.ssh/config" }), {
          status: 400,
          headers: { 'content-type': 'application/json' },
        })
      )
    );

    await expect(addXScienceSshHost({ alias: 'missing' })).rejects.toThrow(
      "alias 'missing' must be defined in ~/.ssh/config"
    );
  });

  it('surfaces a stable probe failure without retrying the provider endpoint', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(JSON.stringify({ detail: 'inference endpoint is unreachable' }), {
        status: 502,
        headers: { 'content-type': 'application/json' },
      })
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(probeXScienceComputeProvider('infer:boltz-local')).rejects.toThrow(
      'inference endpoint is unreachable'
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('uses the exact v1.1 GPU, managed endpoint and compute job contracts', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            available: true,
            gpu_name: 'NVIDIA A100',
            gpu_memory_mb: 81920,
            cuda_version: '12.4',
            gpu_count: 2,
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ enabled: true, override: true, present: true, name: 'NVIDIA A100' }))
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify([
            {
              name: 'boltz2-local',
              displayName: 'Boltz2 Local',
              location: 'local',
              state: 'live',
              port: 9000,
              endpoint: 'http://127.0.0.1:9000',
              serviceDir: '/srv/boltz2',
              serviceDirBytes: 1024,
            },
          ])
        )
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            jobs: [
              {
                jobId: 'compute-fixture-job-0001',
                environment: 'python',
                tierType: 'gpu',
                provider: 'fixture-nim',
                frameId: 'frame-a',
                projectId: 'project-a',
                state: 'running',
                startedAt: '2026-07-11T08:00:00Z',
                startedAtIso: '2026-07-11T08:00:00Z',
                intent: { kind: 'inference' },
                hardwareDetails: { gpu: 'fixture' },
                originToolUseId: 'tool-compute-1',
                rootFrameId: 'root-a',
                providerFamily: 'infer',
                providerLabel: 'fixture-nim',
                externalId: 'sandbox-1',
                externalUrl: 'https://compute.example/jobs/sandbox-1',
                supportsTail: true,
                endedAtIso: null,
                errorKind: null,
                leftOnRemote: [],
                systemHint: null,
              },
            ],
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            jobId: 'compute-fixture-job-0001',
            environment: 'python',
            tierType: 'gpu',
            provider: 'fixture-nim',
            frameId: 'frame-a',
            projectId: 'project-a',
            state: 'running',
            startedAt: '2026-07-11T08:00:00Z',
            startedAtIso: '2026-07-11T08:00:00Z',
            intent: { kind: 'inference' },
            hardwareDetails: { gpu: 'fixture' },
            originToolUseId: 'tool-compute-1',
            rootFrameId: 'root-a',
            providerFamily: 'infer',
            providerLabel: 'fixture-nim',
            externalId: 'sandbox-1',
            externalUrl: 'https://compute.example/jobs/sandbox-1',
            supportsTail: true,
            endedAtIso: null,
            harvest: {
              stdout: { exists: true, size: 70009 },
              stderr: { exists: true, size: 8 },
            },
            errorKind: null,
            leftOnRemote: [],
            systemHint: null,
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ exists: true, size: 14, text: 'step 1\nstep 2\n', truncated: false }))
      )
      .mockResolvedValueOnce(new Response(JSON.stringify(['infer:fixture-nim'])))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(loadXScienceComputeGpuInfo()).resolves.toMatchObject({
      available: true,
      name: 'NVIDIA A100',
      memoryMb: 81920,
      count: 2,
    });
    await expect(loadXScienceComputeGpuEnabled()).resolves.toEqual({
      enabled: true,
      override: true,
      present: true,
      name: 'NVIDIA A100',
    });
    await setXScienceComputeGpuEnabled(false);
    await expect(loadXScienceManagedEndpoints({ includeSizes: true })).resolves.toEqual([
      expect.objectContaining({ name: 'boltz2-local', state: 'live', serviceDirBytes: 1024 }),
    ]);
    await stopXScienceManagedEndpoint('boltz2-local');
    await expect(loadXScienceComputeJobs('project-a')).resolves.toEqual([
      expect.objectContaining({
        jobId: 'compute-fixture-job-0001',
        state: 'running',
        rootFrameId: 'root-a',
        providerLabel: 'fixture-nim',
      }),
    ]);
    await expect(loadXScienceComputeJob('compute-fixture-job-0001')).resolves.toMatchObject({
      state: 'running',
      supportsTail: true,
      harvest: { stdout: { exists: true, size: 70009 }, stderr: { exists: true, size: 8 } },
    });
    await expect(
      loadXScienceComputeJobLog('compute-fixture-job-0001', { stream: 'stderr', tail: 65536 })
    ).resolves.toEqual({
      exists: true,
      size: 14,
      content: 'step 1\nstep 2\n',
      truncated: false,
    });
    await expect(loadXScienceSessionComputeProviders('root-a')).resolves.toEqual(['infer:fixture-nim']);
    await setXScienceSessionComputeProvider('root-a', 'infer:fixture-nim', false);

    expect(fetchMock.mock.calls.map(([url, init]) => `${init?.method ?? 'GET'} ${url}`)).toEqual([
      'GET /api/compute/gpu',
      'GET /api/compute/gpu/enabled',
      'PUT /api/compute/gpu/enabled',
      'GET /api/compute/managed-endpoints?sizes=1',
      'POST /api/compute/managed-endpoints/boltz2-local/stop',
      'GET /api/compute/jobs?projectId=project-a',
      'GET /api/compute/jobs/compute-fixture-job-0001',
      'GET /api/compute/jobs/compute-fixture-job-0001/logs?stream=stderr&tail=65536',
      'GET /api/compute/session/root-a/enabled',
      'PUT /api/compute/session/root-a/enabled/infer%3Afixture-nim',
    ]);
    expect(fetchMock.mock.calls[2]?.[1]?.body).toBe(JSON.stringify({ enabled: false }));
    expect(fetchMock.mock.calls[9]?.[1]?.body).toBe(JSON.stringify({ checked: false }));
  });

  it('reports a missing compute job returned as JSON null', async () => {
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response('null')));
    await expect(loadXScienceComputeJob('missing-job')).rejects.toThrow(
      'X-Science compute job does not exist: missing-job'
    );
  });

  it('uses the live v1.1 local and SSH file browse, download and import contracts', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ hostLabel: 'workstation', hostDetail: 'victor' })))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            entries: [{ name: 'results', isDirectory: true, size: 0, mtime: 123 }],
            truncated: false,
            roots: { home: '/home/victor' },
            resolvedPath: '/home/victor',
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            entries: [{ name: 'output.csv', isDirectory: false, size: 42, mtime: 456 }],
            truncated: false,
            roots: { home: '/home/remote' },
            resolvedPath: '/scratch/job',
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ artifactId: 'artifact-local', versionId: 'version-local', filename: 'a.txt' }))
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ artifact_id: 'artifact-remote', version_id: null, filename: 'output.csv' }))
      );

    await expect(loadXScienceLocalHostInfo(fetchMock)).resolves.toEqual({
      hostLabel: 'workstation',
      hostDetail: 'victor',
    });
    await expect(loadXScienceLocalDirectory('/home/victor', fetchMock)).resolves.toMatchObject({
      resolvedPath: '/home/victor',
      entries: [{ name: 'results', isDirectory: true }],
    });
    await expect(loadXScienceRemoteDirectory('ssh:hpc-a', '/scratch/job', fetchMock)).resolves.toMatchObject({
      resolvedPath: '/scratch/job',
      entries: [{ name: 'output.csv', size: 42 }],
    });
    await expect(importXScienceLocalFile('/tmp/a.txt', 'project-a', fetchMock)).resolves.toMatchObject({
      artifactId: 'artifact-local',
    });
    await expect(
      importXScienceRemoteFile('ssh:hpc-a', '/scratch/job/output.csv', 'project-a', fetchMock)
    ).resolves.toMatchObject({ artifactId: 'artifact-remote' });

    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method ?? 'GET'])).toEqual([
      ['/api/compute/local/hostinfo', 'GET'],
      ['/api/compute/local/files?path=%2Fhome%2Fvictor', 'GET'],
      ['/api/compute/providers/ssh%3Ahpc-a/files?path=%2Fscratch%2Fjob', 'GET'],
      ['/api/compute/local/import', 'POST'],
      ['/api/compute/providers/ssh%3Ahpc-a/import', 'POST'],
    ]);
    expect(getXScienceLocalFileUrl('/tmp/a b.txt', 'inline')).toBe(
      '/api/compute/local/download?path=%2Ftmp%2Fa+b.txt&disposition=inline'
    );
    expect(getXScienceRemoteFileUrl('ssh:hpc-a', '/scratch/out.csv')).toBe(
      '/api/compute/providers/ssh%3Ahpc-a/download?path=%2Fscratch%2Fout.csv&disposition=attachment'
    );
  });

  it('preserves a typed remote import reason without exposing backend detail', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        Response.json({ remoteKind: 'too_large', detail: '/private/result.csv exceeds a limit' }, { status: 413 })
      );

    await expect(
      importXScienceRemoteFile('ssh:hpc-a', '/scratch/result.csv', 'project-a', fetchMock)
    ).rejects.toMatchObject({
      name: 'XScienceFileImportError',
      status: 413,
      kind: 'too_large',
      message: 'X-Science file import failed (413) [too_large]',
    });
  });
});
