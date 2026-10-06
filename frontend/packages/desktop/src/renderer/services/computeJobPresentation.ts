import type { SynonBiomedComputeJob } from './synonBiomedCompute';

type JobObservation = Pick<SynonBiomedComputeJob, 'state' | 'errorKind' | 'endedAtIso'>;
const activeStates = new Set(['pending', 'staging', 'queued', 'running', 'harvesting']);

// A last-observed running state is not fresh evidence while the control plane
// is unreachable. Terminal receipts always win over stale control markers.
export function computeJobVisibleState(job: JobObservation): string {
  if (!job.endedAtIso && activeStates.has(job.state) && isComputeControlUnavailable(job.errorKind)) {
    return job.errorKind!;
  }
  return job.state;
}

export function isComputeControlUnavailable(kind: string | null): boolean {
  return kind === 'control_unreachable' || kind === 'control_configuration_required';
}

export function computeJobControlHintKey(job: JobObservation): string | null {
  const visibleState = computeJobVisibleState(job);
  if (visibleState === 'control_unreachable') return 'settings.computeWorkspace.controlUnreachableHint';
  if (visibleState === 'control_configuration_required') return 'settings.computeWorkspace.controlConfigurationHint';
  return null;
}
