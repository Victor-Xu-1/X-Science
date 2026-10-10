import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  loadSynonBiomedAssistantComposerCapabilities,
  loadSynonBiomedComposerCapabilities,
  loadSynonBiomedProjectBenches,
  loadSynonBiomedProject,
  loadSynonBiomedLinkedTask,
  loadSynonBiomedProjectWorkbench,
} from '@/renderer/services/synonBiomedGateway';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('X-Science project gateway request authority', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('reads one exact task record without capped collections or message history', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: 'older/task',
          name: 'Original older task',
          extra: { project_id: 'p' },
        })
      )
    );
    expect(await loadSynonBiomedLinkedTask('older/task', { fetchImpl: fetchMock, signal: controller.signal })).toEqual({
      frameId: 'older/task',
      name: 'Original older task',
      projectId: 'p',
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/conversations/older%2Ftask',
      expect.objectContaining({ signal: controller.signal })
    );
  });

  it.each([
    { id: 'another-task', name: 'Wrong task' },
    { id: 'expected', name: '' },
  ])('rejects mismatching or incomplete exact task metadata: %o', async (payload) => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(payload)));
    await expect(loadSynonBiomedLinkedTask('expected', { fetchImpl: fetchMock })).rejects.toThrow('identity');
  });

  it('reads only exact project metadata, with encoded identity and caller cancellation', async () => {
    const controller = new AbortController();
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(JSON.stringify({ project_id: 'project/a', name: '原始项目名称' })));
    expect(
      await loadSynonBiomedProject('project/a', { fetchImpl: fetchMock, signal: controller.signal })
    ).toMatchObject({ projectId: 'project/a', name: '原始项目名称' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/projects/project%2Fa',
      expect.objectContaining({ signal: controller.signal })
    );
  });

  it('rejects a project metadata response for another identity', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(JSON.stringify({ project_id: 'other', name: 'Wrong project' })));
    await expect(loadSynonBiomedProject('expected', { fetchImpl: fetchMock })).rejects.toThrow('identity');
  });

  it('isolates a caller-owned bench abort from concurrent shared readers', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async () => new Response('[]'));
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    await Promise.all([
      loadSynonBiomedProjectBenches('isolated', { signal: controller.signal }),
      loadSynonBiomedProjectBenches('isolated'),
    ]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ signal: controller.signal });
    expect(fetchMock.mock.calls[1][1]).not.toHaveProperty('signal');
  });

  it('keeps the existing workbench on the same project metadata decoder', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(
        async (url) =>
          new Response(String(url).endsWith('/p') ? JSON.stringify({ project_id: 'p', name: 'Project p' }) : '[]')
      );
    expect(await loadSynonBiomedProjectWorkbench('p', { fetchImpl: fetchMock })).toMatchObject({
      project: { projectId: 'p', name: 'Project p' },
      benches: [],
      artifacts: [],
      folders: [],
    });
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it('coalesces only concurrent default bench reads and revalidates after settlement', async () => {
    const first = deferred<Response>();
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValueOnce(
        new Response(JSON.stringify([{ id: 'frame-a', root_frame_id: 'frame-a', name: 'Bench A' }]), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        })
      );
    vi.stubGlobal('fetch', fetchMock);

    const routeRead = loadSynonBiomedProjectBenches('project-a');
    const sidebarRead = loadSynonBiomedProjectBenches('project-a');
    expect(fetchMock).toHaveBeenCalledTimes(1);

    first.resolve(
      new Response(JSON.stringify([{ id: 'frame-a', root_frame_id: 'frame-a', name: 'Bench A' }]), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      })
    );
    await expect(Promise.all([routeRead, sidebarRead])).resolves.toEqual([
      [expect.objectContaining({ frameId: 'frame-a' })],
      [expect.objectContaining({ frameId: 'frame-a' })],
    ]);

    await loadSynonBiomedProjectBenches('project-a');
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('does not retain a failed bench request', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(null, { status: 503 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify([{ id: 'frame-b', root_frame_id: 'frame-b', name: 'Bench B' }]), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        })
      );
    vi.stubGlobal('fetch', fetchMock);

    await expect(loadSynonBiomedProjectBenches('project-b')).rejects.toThrow('503');
    await expect(loadSynonBiomedProjectBenches('project-b')).resolves.toEqual([
      expect.objectContaining({ frameId: 'frame-b' }),
    ]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('normalizes the exact composer Skill and MCP authority returned by the conversation endpoint', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          skills: ['single-cell-analysis', ' single-cell-analysis ', '', 12],
          mcp_statuses: [
            { id: 'pubmed-id', name: 'PubMed', status: 'loaded' },
            { id: 'PUBMED-ID', name: 'duplicate', status: 'loaded' },
            { id: 'broken-id', name: 'Broken', status: 'unknown' },
          ],
        }),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    );

    await expect(loadSynonBiomedComposerCapabilities('conversation/a', { fetchImpl: fetchMock })).resolves.toEqual({
      skills: ['single-cell-analysis'],
      mcpStatuses: [{ id: 'pubmed-id', name: 'PubMed', status: 'loaded' }],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/conversations/conversation%2Fa/composer-capabilities',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
  });

  it('uses the same normalized authority contract for a new assistant draft', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          skills: ['autodock-vina'],
          mcp_statuses: [{ id: 'bundled:pubmed', name: 'PubMed', status: 'loaded' }],
        }),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    );

    await expect(
      loadSynonBiomedAssistantComposerCapabilities('synonbiomed:operon', { fetchImpl: fetchMock })
    ).resolves.toEqual({
      skills: ['autodock-vina'],
      mcpStatuses: [{ id: 'bundled:pubmed', name: 'PubMed', status: 'loaded' }],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/assistants/synonbiomed%3Aoperon/composer-capabilities',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
  });
});
