import { describe, expect, it, vi } from 'vitest';
import { loadXSciencePlanDocument } from '@/renderer/services/xScienceRuntimeOperations';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

vi.mock('@/common', () => ({ ipcBridge: {} }));

const reference = { artifactId: 'plan/one', versionId: 'version/one' };
const plan = (taskSummary: string) => ({
  version: 1,
  task_summary: taskSummary,
  phases: [],
});

describe('version-bound task plan preview', () => {
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
