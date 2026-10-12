import { createXScienceProject, deleteXScienceProject } from '@/renderer/services/xScienceGateway';
import {
  deleteXScienceSession,
  getXScienceSessionArtifactsDownloadUrl,
  getXScienceSessionExportUrl,
  moveXScienceSession,
  updateXScienceSession,
} from '@/renderer/services/xScienceSessionActions';
import { describe, expect, it } from 'vitest';
import { createRealArtifactFixture, type RealArtifactFixture } from './xscienceRealArtifactFixture';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science v0.1.0 session actions through XScience', () => {
  it('updates, moves, exports, and deletes a real frame without an LLM call', async () => {
    const fetchImpl = await createXScienceTestFetch(gatewayBaseUrl);
    const options = { baseUrl: gatewayBaseUrl, fetchImpl };
    const suffix = Date.now();
    const source = await createXScienceProject(
      { name: `Session action source ${suffix}`, description: 'Integration fixture', context: '' },
      options
    );
    const target = await createXScienceProject(
      { name: `Session action target ${suffix}`, description: 'Integration fixture', context: '' },
      options
    );
    let frameId: string | null = null;
    let archiveFixture: RealArtifactFixture | null = null;

    try {
      const createResponse = await fetchImpl(`${gatewayBaseUrl}/api/frames`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ project_id: source.projectId }),
      });
      expect(createResponse.status).toBe(201);
      frameId = ((await createResponse.json()) as { root_frame_id?: string }).root_frame_id ?? null;
      expect(frameId).toBeTruthy();

      await updateXScienceSession(
        frameId!,
        { name: 'Updated session title', taskSummary: 'Updated session description' },
        options
      );
      let frameResponse = await fetchImpl(`${gatewayBaseUrl}/api/frames/${encodeURIComponent(frameId!)}?shallow=true`);
      expect(frameResponse.status).toBe(200);
      await expect(frameResponse.json()).resolves.toMatchObject({
        name: 'Updated session title',
        task_summary: 'Updated session description',
        project_id: source.projectId,
      });

      await moveXScienceSession(frameId!, target.projectId, options);
      frameResponse = await fetchImpl(`${gatewayBaseUrl}/api/frames/${encodeURIComponent(frameId!)}?shallow=true`);
      expect(frameResponse.status).toBe(200);
      await expect(frameResponse.json()).resolves.toMatchObject({ project_id: target.projectId });

      const exportResponse = await fetchImpl(`${gatewayBaseUrl}${getXScienceSessionExportUrl(frameId!)}`);
      expect(exportResponse.status).toBe(200);
      expect(exportResponse.headers.get('content-type')).toContain('application/json');
      expect(exportResponse.headers.get('content-disposition')).toContain('attachment');

      archiveFixture = await createRealArtifactFixture({
        gatewayBaseUrl,
        conversationId: frameId!,
        filename: `session-archive-${suffix}.md`,
        contentType: 'text/markdown',
        content: '# Session archive fixture\n',
      });
      const artifactArchive = await fetchImpl(
        `${gatewayBaseUrl}${getXScienceSessionArtifactsDownloadUrl(frameId!)}`
      );
      expect(artifactArchive.status).toBe(200);
      expect(artifactArchive.headers.get('content-type')).toContain('application/zip');
      expect((await artifactArchive.arrayBuffer()).byteLength).toBeGreaterThan(100);
      await archiveFixture.dispose();
      archiveFixture = null;

      const deletedFrameId = frameId!;
      await deleteXScienceSession(deletedFrameId, options);
      frameId = null;
      const missing = await fetchImpl(`${gatewayBaseUrl}/api/frames/${encodeURIComponent(deletedFrameId)}`);
      expect(missing.status).toBe(404);
    } finally {
      if (frameId) {
        await deleteXScienceSession(frameId, options).catch(() => undefined);
        await archiveFixture?.dispose().catch(() => undefined);
      }
      await Promise.all([
        deleteXScienceProject(source.projectId, options).catch(() => undefined),
        deleteXScienceProject(target.projectId, options).catch(() => undefined),
      ]);
    }
  });
});
