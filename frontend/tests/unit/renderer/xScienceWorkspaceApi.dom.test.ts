import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  deleteXScienceArtifact,
  renameXScienceArtifact,
  uploadXScienceArtifact,
} from '@/renderer/services/xScienceWorkspaceApi';
import { clearSharedGatewayReads, loadXScienceProjectArtifacts } from '@/renderer/services/xScienceGateway';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

afterEach(() => {
  vi.unstubAllGlobals();
  clearSharedGatewayReads();
  resetRendererAccountScopeForTest();
});

describe('X-Science workspace API', () => {
  it('uses conversation-scoped mutation routes for rename and delete', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ artifact_id: 'artifact-report', filename: 'renamed.md' })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ deleted: true })));
    vi.stubGlobal('fetch', fetchMock);

    await renameXScienceArtifact({
      conversationId: 'frame-stat6',
      artifactId: 'artifact-report',
      filename: 'renamed.md',
    });
    await deleteXScienceArtifact({ conversationId: 'frame-stat6', artifactId: 'artifact-report' });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/conversations/frame-stat6/workspace/artifacts/artifact-report',
      expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ filename: 'renamed.md' }) })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/conversations/frame-stat6/workspace/artifacts/artifact-report',
      expect.objectContaining({ method: 'DELETE' })
    );
  });

  it('uploads a file through the real X-Science chunk lifecycle and reports progress', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ upload_id: 'upload-1', chunk_size: 4 })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ received: 4 })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ received: 6 })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ artifact_id: 'artifact-uploaded', filename: 'assay.csv' })));
    vi.stubGlobal('fetch', fetchMock);
    const progress = vi.fn();
    const file = new File(['abcdef'], 'assay.csv', { type: 'text/csv' });

    const result = await uploadXScienceArtifact(file, 'frame-stat6', progress, { chunkSize: 4 });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/conversations/frame-stat6/workspace/uploads',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ filename: 'assay.csv', total_size: 6, content_type: 'text/csv', chunk_size: 4 }),
      })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/conversations/frame-stat6/workspace/uploads/upload-1/chunks/0',
      expect.objectContaining({ method: 'POST', body: expect.any(Blob) })
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      '/api/conversations/frame-stat6/workspace/uploads/upload-1/finalize',
      expect.objectContaining({ method: 'POST' })
    );
    expect(progress).toHaveBeenLastCalledWith(100);
    expect(result).toMatchObject({ artifact_id: 'artifact-uploaded', filename: 'assay.csv' });
  });

  it('does not join a pre-delete project artifact read after a workspace delete succeeds', async () => {
    const older = deferred<Response>();
    const current = deferred<Response>();
    const oldArtifacts = [{ id: 'artifact-1', filename: 'keep.txt', project_id: 'project-p' }];
    const currentArtifacts: typeof oldArtifacts = [];
    let reads = 0;
    const fetchMock = vi.fn<typeof fetch>().mockImplementation((input, init) => {
      const url = String(input);
      if (init?.method === 'DELETE') {
        return Promise.resolve(new Response(JSON.stringify({ deleted: true })));
      }
      if (url.endsWith('/project-p/artifacts')) {
        reads += 1;
        return reads === 1 ? older.promise : current.promise;
      }
      return Promise.resolve(new Response('[]'));
    });
    vi.stubGlobal('fetch', fetchMock);

    const beforeMutation = loadXScienceProjectArtifacts('project-p');
    await deleteXScienceArtifact({ conversationId: 'frame-1', artifactId: 'artifact-1' });
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

  it('does not invalidate another owner when an older owner workspace delete completes', async () => {
    const mutation = deferred<Response>();
    const current = deferred<Response>();
    const fetchMock = vi.fn<typeof fetch>().mockImplementation((input, init) => {
      const url = String(input);
      if (init?.method === 'DELETE') {
        return mutation.promise;
      }
      if (url.endsWith('/project-p/artifacts')) {
        return current.promise;
      }
      return Promise.resolve(new Response('[]'));
    });
    vi.stubGlobal('fetch', fetchMock);
    setRendererAccountOwner('owner-a');
    const write = deleteXScienceArtifact({ conversationId: 'frame-1', artifactId: 'artifact-1' });
    setRendererAccountOwner('owner-b');
    const first = loadXScienceProjectArtifacts('project-p');
    mutation.resolve(new Response(JSON.stringify({ deleted: true })));
    await write;
    const second = loadXScienceProjectArtifacts('project-p');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    current.resolve(new Response(JSON.stringify([])));
    await Promise.all([first, second]);
  });
});
