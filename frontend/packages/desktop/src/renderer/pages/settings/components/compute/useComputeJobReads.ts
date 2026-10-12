import { useCallback } from 'react';
import {
  loadXScienceComputeJob,
  loadXScienceComputeJobLog,
  loadXScienceSessionComputeProviders,
} from '@/renderer/services/xScienceCompute';
import { useSettingsReadResource } from '../useSettingsReadResource';

export function useComputeJobReads(jobId: string | null) {
  const readJob = useCallback(() => loadXScienceComputeJob(jobId!), [jobId]);
  const detail = useSettingsReadResource(jobId, readJob);
  const readyJob = detail.status === 'ready' ? jobId : null;
  const readStdout = useCallback(
    () => loadXScienceComputeJobLog(readyJob!, { stream: 'stdout', tail: 65536 }),
    [readyJob]
  );
  const readStderr = useCallback(
    () => loadXScienceComputeJobLog(readyJob!, { stream: 'stderr', tail: 65536 }),
    [readyJob]
  );
  const stdout = useSettingsReadResource(readyJob, readStdout);
  const stderr = useSettingsReadResource(readyJob, readStderr);
  const rootFrameId = readyJob ? detail.data?.rootFrameId : null;
  const readProviders = useCallback(() => loadXScienceSessionComputeProviders(rootFrameId!), [rootFrameId]);
  const providers = useSettingsReadResource(rootFrameId ? `${jobId}:${rootFrameId}` : null, readProviders);
  return { detail, stdout, stderr, providers };
}
