import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  clearSharedGatewayReads,
  createXScienceProject,
  deleteXScienceProject,
  loadXScienceAssistantComposerCapabilities,
  loadXScienceComposerCapabilities,
  loadXScienceProjectArtifacts,
  loadXScienceProjectBenches,
  loadXScienceProjectFolders,
  loadXScienceProject,
  loadXScienceProjects,
  loadXScienceLinkedTask,
  loadXScienceProjectWorkbench,
  updateXScienceProject,
} from '@/renderer/services/xScienceGateway';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { isXScienceHttpError, XScienceHttpError } from '@/renderer/services/xScienceHttp';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('X-Science project gateway request authority', () => {
  afterEach(() => {
    clearSharedGatewayReads();
    resetRendererAccountScopeForTest();
    vi.unstubAllGlobals();
  });

  it('rail retention: preserves typed GET status without changing request transport or exposing an error body', async () => {
    for (const status of [401, 403, 503]) {
      const controller = new AbortController();
      const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response('ignored response detail', { status }));
      const error = await loadXScienceProjects({ fetchImpl: fetchMock, signal: controller.signal }).catch(
        (failure: unknown) => failure
      );
      expect(isXScienceHttpError(error)).toBe(true);
      expect(error).toBeInstanceOf(XScienceHttpError);
      expect(error).toMatchObject({ status, body: undefined });
      expect((error as Error).message).toContain(`GET /api/projects failed (${status})`);
      expect(fetchMock).toHaveBeenCalledWith('/api/projects', expect.objectContaining({ signal: controller.signal }));
    }
  });

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
    expect(await loadXScienceLinkedTask('older/task', { fetchImpl: fetchMock, signal: controller.signal })).toEqual({
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
    await expect(loadXScienceLinkedTask('expected', { fetchImpl: fetchMock })).rejects.toThrow('identity');
  });

  it('reads only exact project metadata, with encoded identity and caller cancellation', async () => {
    const controller = new AbortController();
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(JSON.stringify({ project_id: 'project/a', name: '原始项目名称' })));
    expect(await loadXScienceProject('project/a', { fetchImpl: fetchMock, signal: controller.signal })).toMatchObject({
      projectId: 'project/a',
      name: '原始项目名称',
    });
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
    await expect(loadXScienceProject('expected', { fetchImpl: fetchMock })).rejects.toThrow('identity');
  });

  it('isolates a caller-owned bench abort from concurrent shared readers', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async () => new Response('[]'));
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    await Promise.all([
      loadXScienceProjectBenches('isolated', { signal: controller.signal }),
      loadXScienceProjectBenches('isolated'),
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
    expect(await loadXScienceProjectWorkbench('p', { fetchImpl: fetchMock })).toMatchObject({
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

    const routeRead = loadXScienceProjectBenches('project-a');
    const sidebarRead = loadXScienceProjectBenches('project-a');
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

    await loadXScienceProjectBenches('project-a');
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

    await expect(loadXScienceProjectBenches('project-b')).rejects.toThrow('503');
    await expect(loadXScienceProjectBenches('project-b')).resolves.toEqual([
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

    await expect(loadXScienceComposerCapabilities('conversation/a', { fetchImpl: fetchMock })).resolves.toEqual({
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
      loadXScienceAssistantComposerCapabilities('xscience:operon', { fetchImpl: fetchMock })
    ).resolves.toEqual({
      skills: ['autodock-vina'],
      mcpStatuses: [{ id: 'bundled:pubmed', name: 'PubMed', status: 'loaded' }],
    });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/assistants/xscience%3Aoperon/composer-capabilities',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
  });

  it('coalesces concurrent project list, artifact, and folder reads without later stale reuse', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/projects') {
        return new Response(JSON.stringify({ projects: [{ project_id: 'project-1', name: 'Project One' }] }));
      }
      if (url.endsWith('/project-1/artifacts')) {
        return new Response(
          JSON.stringify([{ id: 'artifact-1', filename: 'artifact-1.txt', project_id: 'project-1' }])
        );
      }
      if (url.endsWith('/project-1/folders')) {
        return new Response(JSON.stringify([{ id: 'folder-1', name: 'Folder 1', project_id: 'project-1' }]));
      }
      if (url.endsWith('/project-2/artifacts')) {
        return new Response(
          JSON.stringify([{ id: 'artifact-2', filename: 'artifact-2.txt', project_id: 'project-2' }])
        );
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const [projectsA, projectsB, artifactsA, artifactsB, foldersA, foldersB, otherProject] = await Promise.all([
      loadXScienceProjects(),
      loadXScienceProjects(),
      loadXScienceProjectArtifacts('project-1'),
      loadXScienceProjectArtifacts('project-1'),
      loadXScienceProjectFolders('project-1'),
      loadXScienceProjectFolders('project-1'),
      loadXScienceProjectArtifacts('project-2'),
    ]);

    expect(projectsA).toEqual(projectsB);
    expect(artifactsA).toEqual(artifactsB);
    expect(foldersA).toEqual(foldersB);
    expect(otherProject[0]?.artifactId).toBe('artifact-2');
    expect(fetchMock).toHaveBeenCalledTimes(4);

    await expect(loadXScienceProjects()).resolves.toEqual(projectsA);
    expect(fetchMock).toHaveBeenCalledTimes(5);
  });

  it('does not reuse an in-flight project list across authenticated owners', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => new Response(JSON.stringify({ projects: [{ project_id: 'p', name: 'P' }] })));
    vi.stubGlobal('fetch', fetchMock);

    setRendererAccountOwner('owner-a');
    const ownerA = loadXScienceProjects();
    setRendererAccountOwner('owner-b');
    const ownerB = loadXScienceProjects();

    await Promise.all([expect(ownerA).resolves.toHaveLength(1), expect(ownerB).resolves.toHaveLength(1)]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it.each(['create', 'update', 'delete', 'already-deleted'] as const)(
    'does not join a pre-%s project read after the mutation succeeds',
    async (operation) => {
      const older = deferred<Response>();
      const current = deferred<Response>();
      const oldProject = { project_id: 'p', name: 'Old name' };
      const newProject = { project_id: 'p', name: 'Current name' };
      const oldProjects = operation === 'create' ? [] : [oldProject];
      const deleting = operation === 'delete' || operation === 'already-deleted';
      const currentProjects = deleting ? [] : [newProject];
      let reads = 0;
      const fetchMock = vi.fn<typeof fetch>().mockImplementation((_input, init) => {
        if (init?.method) {
          return Promise.resolve(
            deleting
              ? new Response(null, { status: operation === 'already-deleted' ? 404 : 204 })
              : new Response(JSON.stringify(newProject))
          );
        }
        reads += 1;
        return reads === 1 ? older.promise : current.promise;
      });
      vi.stubGlobal('fetch', fetchMock);

      const beforeMutation = loadXScienceProjects();
      const input: Parameters<typeof createXScienceProject>[0] = {
        name: newProject.name,
        description: null,
        context: null,
      };
      if (operation === 'create') await createXScienceProject(input);
      if (operation === 'update') await updateXScienceProject('p', input);
      if (deleting) await deleteXScienceProject('p');

      const afterMutation = loadXScienceProjects();
      expect(reads).toBe(2);
      older.resolve(new Response(JSON.stringify({ projects: oldProjects })));
      await beforeMutation;
      // Settling the older request must not evict the current generation.
      const concurrentRefresh = loadXScienceProjects();
      expect(reads).toBe(2);
      current.resolve(new Response(JSON.stringify({ projects: currentProjects })));
      await expect(Promise.all([afterMutation, concurrentRefresh])).resolves.toEqual([
        currentProjects.map((project) =>
          expect.objectContaining({ projectId: project.project_id, name: project.name })
        ),
        currentProjects.map((project) =>
          expect.objectContaining({ projectId: project.project_id, name: project.name })
        ),
      ]);
      expect(fetchMock).toHaveBeenCalledTimes(3);
    }
  );

  it('shares the workbench artifact and folder reads with other project surfaces', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input) => {
      const url = String(input);
      return new Response(url.endsWith('/p') ? JSON.stringify({ project_id: 'p', name: 'Project p' }) : '[]');
    });
    vi.stubGlobal('fetch', fetchMock);
    await Promise.all([
      loadXScienceProjectWorkbench('p'),
      loadXScienceProjectArtifacts('p'),
      loadXScienceProjectFolders('p'),
    ]);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it('does not invalidate another owner when an older owner mutation completes', async () => {
    const mutation = deferred<Response>();
    const current = deferred<Response>();
    const fetchMock = vi.fn<typeof fetch>().mockImplementation((_input, init) => {
      return init?.method ? mutation.promise : current.promise;
    });
    vi.stubGlobal('fetch', fetchMock);
    setRendererAccountOwner('owner-a');
    const write = updateXScienceProject('p', { name: 'P', description: null, context: null });
    setRendererAccountOwner('owner-b');
    const first = loadXScienceProjects();
    mutation.resolve(new Response(JSON.stringify({ project_id: 'p', name: 'P' })));
    await write;
    const second = loadXScienceProjects();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    current.resolve(new Response(JSON.stringify({ projects: [] })));
    await Promise.all([first, second]);
  });

  it.each(['transport', 'origin'] as const)('isolates a custom %s mutation from shared default reads', async (kind) => {
    const current = deferred<Response>();
    const fetchMock = vi.fn<typeof fetch>().mockImplementation((_input, init) => {
      return init?.method
        ? Promise.resolve(new Response(JSON.stringify({ project_id: 'p', name: 'P' })))
        : current.promise;
    });
    vi.stubGlobal('fetch', fetchMock);
    const first = loadXScienceProjects();
    await updateXScienceProject(
      'p',
      { name: 'P', description: null, context: null },
      kind === 'transport' ? { fetchImpl: fetchMock } : { baseUrl: 'https://other.invalid' }
    );
    const second = loadXScienceProjects();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    current.resolve(new Response(JSON.stringify({ projects: [] })));
    await Promise.all([first, second]);
  });
});
