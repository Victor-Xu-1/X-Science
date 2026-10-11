import { useCallback } from 'react';
import {
  loadSynonBiomedComputeJob,
  loadSynonBiomedComputeJobLog,
  loadSynonBiomedSessionComputeProviders,
} from '@/renderer/services/synonBiomedCompute';
import { useSettingsReadResource } from '../useSettingsReadResource';

export function useComputeJobReads(jobId: string | null) {
  const readJob = useCallback(() => loadSynonBiomedComputeJob(jobId!), [jobId]);
  const detail = useSettingsReadResource(jobId, readJob);
  const readyJob = detail.status === 'ready' ? jobId : null;
  const readStdout = useCallback(
    () => loadSynonBiomedComputeJobLog(readyJob!, { stream: 'stdout', tail: 65536 }),
    [readyJob]
  );
  const readStderr = useCallback(
    () => loadSynonBiomedComputeJobLog(readyJob!, { stream: 'stderr', tail: 65536 }),
    [readyJob]
  );
  const stdout = useSettingsReadResource(readyJob, readStdout);
  const stderr = useSettingsReadResource(readyJob, readStderr);
  const rootFrameId = readyJob ? detail.data?.rootFrameId : null;
  const readProviders = useCallback(() => loadSynonBiomedSessionComputeProviders(rootFrameId!), [rootFrameId]);
  const providers = useSettingsReadResource(rootFrameId ? `${jobId}:${rootFrameId}` : null, readProviders);
  return { detail, stdout, stderr, providers };
}
