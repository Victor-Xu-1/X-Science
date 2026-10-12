import {
  isXScienceHttpError,
  requestXScienceJson,
  type XScienceGatewayOptions,
} from '@/renderer/services/xScienceHttp';
import { invalidateXScienceFrameReads } from './xScienceFrameReads';

export type XScienceSessionUpdate = {
  name: string;
  taskSummary: string;
};

export async function updateXScienceSession(
  frameId: string,
  update: XScienceSessionUpdate,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  await requestXScienceJson(
    `/api/frames/${encodeURIComponent(frameId)}`,
    {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ name: update.name, task_summary: update.taskSummary }),
    },
    options
  );
}

export async function moveXScienceSession(
  frameId: string,
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  await requestXScienceJson(
    `/api/frames/${encodeURIComponent(frameId)}/move`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ target_project_id: projectId }),
    },
    options
  );
}

export async function deleteXScienceSession(frameId: string, options: XScienceGatewayOptions = {}): Promise<void> {
  try {
    await requestXScienceJson(`/api/frames/${encodeURIComponent(frameId)}`, { method: 'DELETE' }, options);
  } catch (error) {
    // A realtime event or another client may have removed the frame after the
    // sidebar was rendered. Treat that already-absent state as an idempotent
    // success so a repeated click cannot leave a ghost row behind.
    if (!isXScienceHttpError(error) || error.status !== 404) throw error;
  } finally {
    // A failed delete must also invalidate reads: the next refresh should
    // prove whether the frame still exists instead of replaying stale data.
    invalidateXScienceFrameReads(frameId);
  }
}

export function getXScienceSessionExportUrl(frameId: string): string {
  return `/api/frames/${encodeURIComponent(frameId)}/export`;
}

export function getXScienceSessionArtifactsDownloadUrl(frameId: string): string {
  return `/api/frames/${encodeURIComponent(frameId)}/artifacts/download?include_metadata=true`;
}
