/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useArtifactCloudExport } from '@/renderer/pages/artifact/useArtifactCloudExport';
import { useArtifactFileActionEditor } from '@/renderer/pages/artifact/useArtifactFileActionEditor';

const service = vi.hoisted(() => ({
  storage: vi.fn(),
  credentials: vi.fn(),
  buckets: vi.fn(),
  copy: vi.fn(),
  move: vi.fn(),
  export: vi.fn(),
}));
vi.mock('@/renderer/services/synonBiomedWorkspaceSettings', () => ({
  loadSynonBiomedStorageSettings: service.storage,
  loadSynonBiomedCloudCredentials: service.credentials,
  loadSynonBiomedCloudBuckets: service.buckets,
  exportSynonBiomedArtifactToCloud: service.export,
}));
vi.mock('@/renderer/services/synonBiomedArtifacts', () => ({
  copySynonBiomedArtifact: service.copy,
  moveSynonBiomedArtifact: service.move,
}));
const artifact = { artifactId: 'file-a', filename: 'source.txt', folderId: 'folder-a' };
const credential = (id: string, defaultBucket = `${id}-bucket`) => ({
  id,
  name: id,
  provider: 's3',
  connected: true,
  defaultBucket,
  credentialType: 'access_key',
  region: '',
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
};
beforeEach(() => {
  service.storage.mockReset().mockImplementation(() => new Promise(() => {}));
  service.credentials.mockReset().mockResolvedValue([credential('first'), credential('second')]);
  service.buckets.mockReset().mockImplementation(async (id: string) => [`${id}-bucket`]);
  service.copy.mockReset().mockResolvedValue({ artifactId: 'copied' });
  service.move.mockReset().mockResolvedValue({ artifactId: 'file-a' });
  service.export.mockReset().mockResolvedValue({ exported: true });
});

describe('File operation read and write ownership', () => {
  it('loads only the cloud catalogue without starting or waiting for unrelated disk scans', async () => {
    const { result } = renderHook(() => useArtifactCloudExport(true));
    await waitFor(() => expect(result.current.ready).toBe(true));
    expect(service.credentials).toHaveBeenCalledTimes(1);
    expect(service.storage).not.toHaveBeenCalled();
  });
  it('keeps the newest credential and bucket when an older bucket read finishes', async () => {
    const pending = deferred<string[]>();
    service.buckets.mockReturnValueOnce(pending.promise);
    const { result } = renderHook(() => useArtifactCloudExport(true));
    await waitFor(() => expect(result.current.credentialId).toBe('first'));
    expect(result.current.bucketLoading).toBe(true);
    await act(() => result.current.selectCredential('second'));
    expect(result.current.bucket).toBe('second-bucket');
    await act(async () => {
      pending.resolve(['first-bucket']);
      await pending.promise;
    });
    expect(result.current.credentialId).toBe('second');
    expect(result.current.buckets).toEqual(['second-bucket']);
    expect(result.current.ready).toBe(true);
  });

  it('retries a bucket read without resetting the user-selected credential', async () => {
    service.buckets.mockRejectedValueOnce(new Error('fixture bucket error'));
    const { result } = renderHook(() => useArtifactCloudExport(true));
    await waitFor(() => expect(result.current.bucketFailed).toBe(true));
    expect(result.current.ready).toBe(false);
    await act(() => result.current.retryBuckets());
    expect(result.current.credentialId).toBe('first');
    expect(result.current.bucket).toBe('first-bucket');
    expect(result.current.bucketFailed).toBe(false);
    expect(service.credentials).toHaveBeenCalledTimes(1);
    expect(service.buckets).toHaveBeenCalledTimes(2);
  });

  it('does not preselect an unavailable configured default bucket', async () => {
    service.credentials.mockResolvedValueOnce([credential('first', 'unavailable')]);
    const { result } = renderHook(() => useArtifactCloudExport(true));
    await waitFor(() => expect(result.current.ready).toBe(true));
    expect(result.current.bucket).toBe('first-bucket');
  });

  it('distinguishes a successfully empty catalogue from a failed read', async () => {
    service.credentials.mockResolvedValueOnce([]);
    const { result } = renderHook(() => useArtifactCloudExport(true));
    await waitFor(() => expect(result.current.status).toBe('ready'));
    expect(result.current.credentials).toEqual([]);
    expect(result.current.ready).toBe(false);
    expect(service.buckets).not.toHaveBeenCalled();
  });

  it('does not apply an older catalogue after an explicit reload has completed', async () => {
    const pending = deferred<ReturnType<typeof credential>[]>();
    service.credentials.mockReturnValueOnce(pending.promise).mockResolvedValueOnce([credential('second')]);
    const { result } = renderHook(() => useArtifactCloudExport(true));
    expect(service.credentials).toHaveBeenCalledTimes(1);
    await act(() => result.current.retryCatalogue());
    await waitFor(() => expect(result.current.bucket).toBe('second-bucket'));
    await act(async () => {
      pending.resolve([credential('first')]);
      await pending.promise;
    });
    expect(result.current.credentialId).toBe('second');
    expect(service.buckets).not.toHaveBeenCalledWith('first');
  });

  it('fences two synchronous copy submissions and preserves the existing payload', async () => {
    const pending = deferred<{ artifactId: string }>();
    service.copy.mockReturnValueOnce(pending.promise);
    const completed = vi.fn();
    const { result } = renderHook(() => useArtifactFileActionEditor('copy', artifact, 'copy', completed));
    let submitted!: Promise<void>;
    act(() => {
      submitted = result.current.submit();
      void result.current.submit();
    });
    expect(service.copy).toHaveBeenCalledTimes(1);
    expect(service.copy).toHaveBeenCalledWith({
      artifactId: 'file-a',
      newFilename: 'source-copy.txt',
      targetFolderId: null,
    });
    expect(result.current.pending).toBe(true);
    await act(async () => {
      pending.resolve({ artifactId: 'copied' });
      await submitted;
    });
    expect(completed).toHaveBeenCalledWith({ copiedArtifactId: 'copied', folderId: null });
    expect(result.current.canSubmit).toBe(false);
    await act(() => result.current.submit());
    expect(service.copy).toHaveBeenCalledTimes(1);
  });

  it('keeps copy drafts after a failed write and only retries on another explicit submit', async () => {
    service.copy.mockRejectedValueOnce(new Error('fixture mutation error'));
    const completed = vi.fn();
    const { result } = renderHook(() => useArtifactFileActionEditor('copy', artifact, 'copy', completed));
    act(() => {
      result.current.setFilename('  chosen.txt  ');
      result.current.setFolderId('chosen-folder');
    });
    await act(() => result.current.submit());
    expect(result.current.failed).toBe(true);
    expect(result.current.filename).toBe('  chosen.txt  ');
    expect(result.current.folderId).toBe('chosen-folder');
    expect(completed).not.toHaveBeenCalled();
    expect(service.copy).toHaveBeenCalledTimes(1);
    await act(() => result.current.submit());
    expect(service.copy).toHaveBeenLastCalledWith({
      artifactId: 'file-a',
      newFilename: 'chosen.txt',
      targetFolderId: 'chosen-folder',
    });
    expect(service.copy).toHaveBeenCalledTimes(2);
  });

  it.each(['move', 'export'] as const)('does not deliver a %s completion after unmount', async (action) => {
    const pending = deferred<{ artifactId: string }>();
    service[action].mockReturnValueOnce(pending.promise);
    const completed = vi.fn();
    const { result, unmount } = renderHook(() => useArtifactFileActionEditor(action, artifact, 'copy', completed));
    await waitFor(() => expect(result.current.canSubmit).toBe(true));
    let submitted!: Promise<void>;
    act(() => {
      submitted = result.current.submit();
    });
    unmount();
    await act(async () => {
      pending.resolve({ artifactId: 'file-a' });
      await submitted;
    });
    expect(completed).not.toHaveBeenCalled();
    expect(service[action]).toHaveBeenCalledTimes(1);
  });
});
