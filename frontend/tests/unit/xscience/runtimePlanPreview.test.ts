import { describe, expect, it, vi } from 'vitest';
import { loadXSciencePlanDocument, loadXSciencePlanVersions } from '@/renderer/services/xScienceRuntimeOperations';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

vi.mock('@/common', () => ({ ipcBridge: {} }));

const reference = { artifactId: 'plan/one', versionId: 'version/one' };
const plan = (taskSummary: string) => ({
  version: 1,
  task_summary: taskSummary,
  phases: [],
});

describe('version-bound task plan preview', () => {
  it('reads actual content-version pairs without exposing storage or current runtime metadata', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            artifact_id: 'plan/one',
            version_id: 'old/version',
            version_number: 1,
            file_path: '/storage/private',
            revision_number: 9,
            progress: { completed: 8 },
          },
          { artifact_id: 'plan/one', version_id: 'new/version', version_number: 3 },
        ])
      )
    );
    const versions = await loadXSciencePlanVersions('plan/one', { baseUrl: 'http://gateway.test', fetchImpl });
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl.mock.calls[0][0]).toBe('http://gateway.test/api/artifacts/plan%2Fone/versions');
    expect(versions).toEqual([
      { artifactId: 'plan/one', versionId: 'old/version', contentVersion: 1 },
      { artifactId: 'plan/one', versionId: 'new/version', contentVersion: 3 },
    ]);
  });

  it('retains a valid empty content-version list separately from request errors', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response('[]'));
    await expect(loadXSciencePlanVersions('plan', { fetchImpl })).resolves.toEqual([]);
  });

  it.each(
    [
      null,
      {},
      { versions: [] },
      [null],
      [{ artifact_id: 'foreign', version_id: 'v1', version_number: 1 }],
      [{ artifact_id: 'plan', version_id: '', version_number: 1 }],
      [{ artifact_id: 'plan', version_id: '   ', version_number: 1 }],
      [{ artifact_id: 'plan', version_id: 'v1', version_number: 0 }],
      [{ artifact_id: 'plan', version_id: 'v1', version_number: 1.5 }],
      [{ artifact_id: 'plan', version_id: 'v1', version_number: '1' }],
      [{ artifact_id: 'plan', version_id: 'v1', version_number: Number.MAX_SAFE_INTEGER + 1 }],
      [
        { artifact_id: 'plan', version_id: 'v1', version_number: 1 },
        { artifact_id: 'plan', version_id: 'v1', version_number: 2 },
      ],
      [
        { artifact_id: 'plan', version_id: 'v1', version_number: 1 },
        { artifact_id: 'plan', version_id: 'v2', version_number: 1 },
      ],
    ].map((payload) => ({ payload }))
  )('rejects a malformed, foreign or duplicate content-version list as a whole: $payload', async ({ payload }) => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify(payload)));
    await expect(loadXSciencePlanVersions('plan', { fetchImpl })).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('preserves an unavailable content-version list error without reading latest', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response('{"detail":"Artifact unavailable"}', { status: 404 }));
    await expect(loadXSciencePlanVersions('plan', { fetchImpl })).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl.mock.calls[0][0]).toMatch(/\/api\/artifacts\/plan\/versions$/);
  });

  it('rejects a blank artifact before requesting a content-version list', async () => {
    const fetchImpl = vi.fn();
    await expect(loadXSciencePlanVersions(' ', { fetchImpl })).rejects.toThrow();
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('loads the captured exact version rather than the latest artifact body', async () => {
    const fetchImpl = vi.fn(
      async (url: string) =>
        new Response(JSON.stringify(plan(url.includes('/versions/') ? 'Captured version' : 'Unreviewed latest')))
    );
    const document = await loadXSciencePlanDocument(reference, {
      baseUrl: 'http://gateway.test',
      fetchImpl,
    });
    expect(fetchImpl.mock.calls[0][0]).toBe('http://gateway.test/api/artifacts/plan%2Fone/versions/version%2Fone');
    expect(document.taskSummary).toBe('Captured version');
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('does not fall back to latest when the captured version is unavailable', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      new Response('{"detail":"Artifact version unavailable"}', {
        status: 404,
      })
    );
    await expect(
      loadXSciencePlanDocument(reference, {
        baseUrl: 'http://gateway.test',
        fetchImpl,
      })
    ).rejects.toThrow();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl.mock.calls[0][0]).toBe('http://gateway.test/api/artifacts/plan%2Fone/versions/version%2Fone');
  });

  it('retains the explicit artifact-only legacy download contract', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify(plan('Legacy plan'))));
    const document = await loadXSciencePlanDocument(
      { ...reference, versionId: null },
      { baseUrl: 'http://gateway.test', fetchImpl }
    );
    expect(fetchImpl.mock.calls[0][0]).toBe('http://gateway.test/api/artifacts/plan%2Fone');
    expect(document.taskSummary).toBe('Legacy plan');
  });

  it('does not turn a malformed present version into an implicit legacy latest request', async () => {
    const fetchImpl = vi.fn();
    await expect(loadXSciencePlanDocument({ ...reference, versionId: '' }, { fetchImpl })).rejects.toThrow(
      'Invalid task plan version'
    );
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('accepts only real counts bound to the projected artifact and exact version', () => {
    const snapshot = (progress: unknown) =>
      normalizeXScienceRuntimeSnapshot({
        id: 'frame',
        status: 'processing',
        output_data: {
          plan_artifact_id: 'plan',
          plan_version_id: 'version',
          plan_progress: progress,
        },
      }).taskPlan;
    expect(
      snapshot({
        artifact_id: 'plan',
        version_id: 'version',
        completed: 0,
        total: 3,
      })?.progress
    ).toEqual({ completed: 0, total: 3 });
    expect(
      snapshot({
        artifact_id: 'plan',
        version_id: 'version',
        completed: 3,
        total: 3,
      })?.progress
    ).toEqual({ completed: 3, total: 3 });
    for (const invalid of [
      { artifact_id: 'plan', version_id: 'old', completed: 1, total: 3 },
      { artifact_id: 'foreign', version_id: 'version', completed: 1, total: 3 },
      { artifact_id: 'plan', version_id: 'version', completed: 4, total: 3 },
      { artifact_id: 'plan', version_id: 'version', completed: -1, total: 3 },
      { artifact_id: 'plan', version_id: 'version', completed: 1.5, total: 3 },
      null,
    ])
      expect(snapshot(invalid)?.progress).toBeUndefined();
  });
});
