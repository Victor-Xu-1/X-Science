import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useArtifactMetadataReads } from '@/renderer/pages/artifact/useArtifactMetadataReads';
import type { SynonBiomedProjectArtifact } from '@/renderer/services/synonBiomedGateway';

const api = vi.hoisted(() => ({ versions: vi.fn(), lineage: vi.fn(), folders: vi.fn(), resources: vi.fn() }));
vi.mock('@/renderer/services/synonBiomedArtifacts', () => ({
  loadSynonBiomedArtifactVersions: api.versions,
  loadSynonBiomedArtifactLineage: api.lineage,
}));
vi.mock('@/renderer/services/synonBiomedGateway', () => ({
  loadSynonBiomedProjectFolders: api.folders,
  loadSynonBiomedProjectArtifacts: api.resources,
}));

const artifact: SynonBiomedProjectArtifact = {
  artifactId: 'read-owner',
  versionId: 'read-v1',
  versionNumber: 1,
  projectId: 'project',
  filename: 'read.txt',
  contentType: 'text/plain',
  sizeBytes: 12,
  createdAt: null,
  updatedAt: null,
  frameId: 'frame',
  rootFrameId: 'frame',
  isUserUpload: false,
  agentName: null,
  isIntermediate: false,
  creatingFrameId: null,
  checksum: null,
  filePath: null,
  folderId: null,
  priority: 'normal',
};
function deferred() {
  let resolve!: (value: []) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<[]>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, resolve, reject };
}
beforeEach(() => {
  api.versions.mockReset().mockResolvedValue([]);
  api.lineage.mockReset().mockResolvedValue(null);
  api.folders.mockReset().mockResolvedValue([]);
  api.resources.mockReset().mockResolvedValue([]);
});

describe('independent artifact metadata reads', () => {
  it('does not start queued reads after the view already unmounted', async () => {
    const view = renderHook(() => useArtifactMetadataReads(artifact, 0));
    view.unmount();
    await act(async () => {
      await Promise.resolve();
    });
    for (const read of Object.values(api)) expect(read).not.toHaveBeenCalled();
  });

  it('coalesces repeated retry calls for one section without rereading peers', async () => {
    api.versions.mockRejectedValueOnce(new Error('fixture failed read'));
    const { result } = renderHook(() => useArtifactMetadataReads(artifact, 0));
    await waitFor(() => expect(result.current.versions.status).toBe('failed'));
    const pending = deferred();
    api.versions.mockReturnValueOnce(pending.promise);
    act(() => {
      result.current.retry('versions');
      result.current.retry('versions');
    });
    await waitFor(() => expect(api.versions).toHaveBeenCalledTimes(2));
    for (const read of [api.lineage, api.folders, api.resources]) expect(read).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.resolve([]);
      await pending.promise;
    });
    expect(result.current.versions.status).toBe('ready');
  });

  it('rejects an old failure after the same file started a new refresh epoch', async () => {
    const pending = deferred();
    api.versions.mockReturnValueOnce(pending.promise);
    const { result, rerender } = renderHook(({ revision }) => useArtifactMetadataReads(artifact, revision), {
      initialProps: { revision: 0 },
    });
    await waitFor(() => expect(api.versions).toHaveBeenCalledTimes(1));
    rerender({ revision: 1 });
    await waitFor(() => expect(result.current.versions.status).toBe('ready'));
    await act(async () => {
      pending.reject(new Error('obsolete epoch failure'));
      await Promise.resolve();
    });
    expect(result.current.versions.status).toBe('ready');
    expect(result.current.versions.value).toEqual([]);
  });

  it('keeps unlinked project resources known rather than making a fake project request', async () => {
    const { result } = renderHook(() => useArtifactMetadataReads({ ...artifact, projectId: null }, 0));
    await waitFor(() => expect(result.current.versions.status).toBe('ready'));
    expect(result.current.folders).toEqual({ status: 'ready', value: [] });
    expect(result.current.resources.value).toEqual([{ ...artifact, projectId: null }]);
    expect(api.folders).not.toHaveBeenCalled();
    expect(api.resources).not.toHaveBeenCalled();
  });

  it('changes only lineage ownership and ignores a previous selected-version failure', async () => {
    const pending = deferred();
    api.lineage.mockReturnValueOnce(pending.promise);
    const { result, rerender } = renderHook(({ versionId }) => useArtifactMetadataReads(artifact, 0, versionId), {
      initialProps: { versionId: 'read-v1' },
    });
    await waitFor(() => expect(api.lineage).toHaveBeenCalledTimes(1));
    rerender({ versionId: 'read-v2' });
    await waitFor(() => expect(result.current.lineage.status).toBe('ready'));
    expect(api.lineage).toHaveBeenLastCalledWith('read-owner', { slim: true, versionId: 'read-v2' });
    for (const read of [api.versions, api.folders, api.resources]) expect(read).toHaveBeenCalledTimes(1);
    await act(async () => {
      pending.reject(new Error('obsolete version failure'));
      await Promise.resolve();
    });
    expect(result.current.lineage.status).toBe('ready');
  });
});
