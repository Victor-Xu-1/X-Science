import { describe, expect, it } from 'vitest';
import { computeJobControlHintKey, computeJobVisibleState } from '@/renderer/services/compute/jobPresentation';

describe('compute job control observations', () => {
  it('does not present an unreachable last-observed running job as live', () => {
    const job = { state: 'running', errorKind: 'control_unreachable', endedAtIso: null };
    expect(computeJobVisibleState(job)).toBe('control_unreachable');
    expect(computeJobControlHintKey(job)).toBe('settings.computeWorkspace.controlUnreachableHint');
  });

  it('preserves terminal authority and recovers the normal state after reconnection', () => {
    for (const state of ['done', 'failed', 'orphaned', 'timed_out']) {
      expect(
        computeJobVisibleState({ state, errorKind: 'control_unreachable', endedAtIso: '2026-10-06T12:00:00Z' })
      ).toBe(state);
      expect(
        computeJobControlHintKey({ state, errorKind: 'control_unreachable', endedAtIso: '2026-10-06T12:00:00Z' })
      ).toBeNull();
    }
    expect(computeJobVisibleState({ state: 'running', errorKind: null, endedAtIso: null })).toBe('running');
    expect(
      computeJobVisibleState({ state: 'harvesting', errorKind: 'control_configuration_required', endedAtIso: null })
    ).toBe('control_configuration_required');
  });
});
