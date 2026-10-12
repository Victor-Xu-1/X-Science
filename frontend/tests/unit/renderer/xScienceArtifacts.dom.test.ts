import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  copyXScienceArtifact,
  createXScienceTextArtifactVersion,
  deleteXScienceArtifact,
  getXScienceArtifactVersionContentUrl,
  loadXScienceArtifactLineage,
  loadXScienceArtifactVersions,
  moveXScienceArtifact,
} from '@/renderer/services/xScienceArtifacts';
import { clearSharedGatewayReads, loadXScienceProjectArtifacts } from '@/renderer/services/xScienceGateway';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('X-Science artifact lifecycle service', () => {
  afterEach(() => {
    clearSharedGatewayReads();
    vi.unstubAllGlobals();
  });

  it('maps real version and lineage response shapes', async () => {
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      if (url === '/api/artifacts/artifact-1/versions') {
        return Response.json([
          {
            version_id: 'version-2',
            version_number: 2,
            artifact_id: 'artifact-1',
            frame_id: 'frame-1',
            agent_name: 'OPERON',
            language: 'markdown',
            content_type: 'text/markdown',
            size_bytes: 84,
            created_at: '2026-07-11T02:00:00.000Z',
            file_path: '/state/artifact-1/v2-report.md',
            parent_version_id: 'version-1',
          },
        ]);
      }
      if (url === '/api/artifacts/artifact-1/lineage?slim=1') {
        return Response.json({
          artifact_id: 'artifact-1',
          version_id: 'version-2',
          version_number: 2,
          filename: 'report.md',
          code: 'python analysis.py',
          code_description: '运行统计分析',
          messages: [{ role: 'assistant', text: '完成分析' }],
          environment_snapshot: { python: '3.12' },
          language: 'python',
          interactions: [{ tool: 'python' }],
          has_cell_sources: true,
          has_messages: true,
          has_environment: true,
          pending: false,
          dependency_mappings: [{ artifact_id: 'input-1' }],
        });
      }
      return Response.json({ detail: 'not found' }, { status: 404 });
    });
    vi.stubGlobal('fetch', fetchMock);

    await expect(loadXScienceArtifactVersions('artifact-1')).resolves.toEqual([
      expect.objectContaining({
        versionId: 'version-2',
        versionNumber: 2,
        artifactId: 'artifact-1',
        parentVersionId: 'version-1',
      }),
    ]);
    await expect(loadXScienceArtifactLineage('artifact-1', { slim: true })).resolves.toMatchObject({
      artifactId: 'artifact-1',
      versionId: 'version-2',
      codeDescription: '运行统计分析',
      hasMessages: true,
      hasEnvironment: true,
      pending: false,
    });
    expect(getXScienceArtifactVersionContentUrl('version-2')).toBe('/api/artifacts/versions/version-2');
  });

  it('uses the native copy, move and text-version contracts', async () => {
    const observed: Array<{ url: string; method: string; body?: unknown }> = [];
    const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      observed.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      });
      return Response.json({ id: 'artifact-copy', version_id: 'version-copy', filename: 'report-copy.md' });
    });
    vi.stubGlobal('fetch', fetchMock);

    await copyXScienceArtifact({
      artifactId: 'artifact-1',
      newFilename: 'report-copy.md',
      targetFolderId: 'folder-2',
    });
    await moveXScienceArtifact({ artifactId: 'artifact-1', folderId: 'folder-2', sortOrder: 3 });
    await createXScienceTextArtifactVersion({
      artifactId: 'artifact-1',
      content: '# updated',
      contentType: 'text/markdown',
      parentVersionId: 'version-2',
    });

    expect(observed).toEqual([
      {
        url: '/api/artifacts/artifact-1/copy',
        method: 'POST',
        body: { new_filename: 'report-copy.md', target_folder_id: 'folder-2' },
      },
      {
        url: '/api/artifacts/artifact-1/folder',
        method: 'PATCH',
        body: { folder_id: 'folder-2', sort_order: 3 },
      },
      {
        url: '/api/artifacts/artifact-1/versions',
        method: 'POST',
        body: { content: '# updated', content_type: 'text/markdown', parent_version_id: 'version-2' },
      },
    ]);
  });

  it('reads and validates the explicitly selected immutable lineage version', async () => {
    const fetchMock = vi.fn(async (url: string) =>
      url === '/api/artifacts/versions/version%2F1/lineage?slim=1'
        ? Response.json({ artifact_id: 'artifact-1', version_id: 'version/1', version_number: 1, pending: true })
        : Response.json({ detail: 'unexpected current-version request' }, { status: 404 })
    );
    vi.stubGlobal('fetch', fetchMock);
    await expect(
      loadXScienceArtifactLineage('artifact-1', { slim: true, versionId: 'version/1' })
    ).resolves.toMatchObject({ versionId: 'version/1', pending: true });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('does not join a pre-delete project artifact read after the mutation succeeds', async () => {
    const older = deferred<Response>();
    const current = deferred<Response>();
    const oldArtifacts = [{ id: 'artifact-1', filename: 'keep.txt', project_id: 'project-p' }];
    const currentArtifacts: typeof oldArtifacts = [];
    let reads = 0;
    const fetchMock = vi.fn<typeof fetch>().mockImplementation((input, init) => {
      const url = String(input);
      if (init?.method) {
        return Promise.resolve(new Response(JSON.stringify({ id: 'artifact-1' })));
      }
      if (url.endsWith('/project-p/artifacts')) {
        reads += 1;
        return reads === 1 ? older.promise : current.promise;
      }
      return Promise.resolve(new Response('[]'));
    });
    vi.stubGlobal('fetch', fetchMock);

    const beforeMutation = loadXScienceProjectArtifacts('project-p');
    await deleteXScienceArtifact('artifact-1');
    const afterMutation = loadXScienceProjectArtifacts('project-p');
    expect(reads).toBe(2);
    older.resolve(new Response(JSON.stringify(oldArtifacts)));
    await beforeMutation;
    const concurrentRefresh = loadXScienceProjectArtifacts('project-p');
    expect(reads).toBe(2);
    current.resolve(new Response(JSON.stringify(currentArtifacts)));
    await expect(Promise.all([afterMutation, concurrentRefresh])).resolves.toEqual([[], []]);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it.each([
    { artifact_id: 'another-file', version_id: 'version-1' },
    { artifact_id: 'artifact-1', version_id: 'another-version' },
  ])('rejects lineage belonging to a different requested owner', async (identity) => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json({ ...identity, version_number: 1 }))
    );
    await expect(loadXScienceArtifactLineage('artifact-1', { versionId: 'version-1' })).rejects.toThrow('identity');
  });
});
