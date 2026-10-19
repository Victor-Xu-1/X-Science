import { renderHook } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { useGuidSpecialistPresentation } from '@/renderer/pages/guid/hooks/useGuidSpecialistDraft';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';
import type { SessionSpecialistView } from '@/renderer/services/runtime/sessionSpecialistController';
import { specialistSelection, specialistSnapshot } from '../xscience/sessionSpecialistFixtures';
vi.mock('@/renderer/hooks/xScience/runtime/useSessionSpecialist', () => ({ useSpecialistCatalog: vi.fn() }));
const draft: XScienceSpecialistControl = {
  applied: specialistSelection('AGENT_C'),
  desired: specialistSelection('AGENT_C'),
  choices: [{ id: 'assistant:AGENT_C', name: 'AGENT_C', label: 'C' }],
  pending: false,
  available: true,
  unavailable: false,
  error: null,
  retryable: false,
  catalogLoading: false,
  catalogError: false,
  hasLiveAttempt: false,
  prepared: false,
  draft: true,
  change: vi.fn(),
  refresh: vi.fn(),
  retry: vi.fn(),
  current: () => true,
  capture: () => ({}),
};
const retry = vi.fn(),
  refresh = vi.fn();
const view = (snapshot: SessionSpecialistView['snapshot']): SessionSpecialistView => ({
  snapshot,
  available: true,
  loading: false,
  writing: false,
  error: null,
  retryable: false,
});
it('does not replace a known unavailable runtime binding with the unmaterialized draft profile', () => {
  const snapshot = specialistSnapshot(0, 'AGENT_A', {
    applied_unavailable: true,
    applied: { ...specialistSelection(), assistant_id: '', agent_name: '', selection_name: '' },
  });
  const { result } = renderHook(() => useGuidSpecialistPresentation(draft, view(snapshot), retry, refresh));
  expect(result.current.unavailable).toBe(true);
  expect(result.current.applied).toBeNull();
});
it('shows a new explicit draft choice without claiming the old prepared definition already applies to it', () => {
  const { result, rerender } = renderHook(() =>
    useGuidSpecialistPresentation(draft, view(specialistSnapshot(1, 'AGENT_B')), retry, refresh)
  );
  expect(result.current.desired?.selection_name).toBe('AGENT_C');
  expect(result.current.prepared).toBe(false);
  const originalRefresh = result.current.refresh;
  rerender();
  expect(result.current.refresh).toBe(originalRefresh);
});
