import React, { useCallback, useState } from 'react';
import { act, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import { renderWithI18n } from '../i18nTestUtils';
import {
  normalizeXScienceRuntimeSnapshot,
  type XSciencePlanReference,
} from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { useAcpPlanPresentation } from '@/renderer/pages/conversation/platforms/acp/useAcpPlanPresentation';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const mocks = vi.hoisted(() => ({
  load: vi.fn(),
  plan: vi.fn(),
  approve: vi.fn(),
  invalidate: null as (() => void) | null,
}));
vi.mock('@/renderer/services/xScienceRuntimeOperations', () => ({
  loadXScienceRuntimeSnapshot: mocks.load,
  loadXSciencePlanDocument: mocks.plan,
  approveXSciencePlan: mocks.approve,
  discardXSciencePlan: vi.fn(),
  loadXScienceExecutionLogPage: vi.fn(),
  resumeXScienceFrame: vi.fn(),
  resolveXScienceInputRequest: vi.fn(),
  resolveXScienceAskUserRequest: vi.fn(),
  subscribeXScienceRuntimeInvalidation: (_owner: string, listener: () => void) => {
    mocks.invalidate = listener;
    return vi.fn();
  },
}));
vi.mock('@/renderer/services/xScienceFrameReads', () => ({
  invalidateXScienceFrameReads: vi.fn(),
}));
vi.mock('@/renderer/services/xScienceAnnotations', () => ({
  loadXScienceFrameVerification: vi.fn().mockResolvedValue({}),
}));
vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  useRealtime: () => ({
    runtime: {},
    snapshot: { identity: 'authenticated', status: 'connected' },
  }),
}));
vi.mock('@/renderer/utils/platform', () => ({
  isElectronDesktop: () => false,
}));
vi.mock('@/renderer/services/FileService', () => ({
  FileService: { processDroppedFiles: vi.fn() },
}));
vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: async () => [],
  loadXScienceMcpServers: async () => [],
}));

import XScienceRuntimeOperations from '@/renderer/components/xScience/runtime/XScienceRuntimeOperations';
import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import { emitter } from '@/renderer/utils/emitter';

const snapshot = (completed: number, versionId: string | null = 'version') =>
  normalizeXScienceRuntimeSnapshot({
    id: 'frame',
    status: 'completed',
    output_data: {
      plan_artifact_id: 'plan',
      plan_version_id: versionId,
      plan_progress: {
        artifact_id: 'plan',
        version_id: versionId,
        completed,
        total: 3,
      },
    },
  });
const document = {
  version: 1,
  taskSummary: 'Captured task plan',
  phases: [],
  feasibility: null,
};

function Harness() {
  const [reference, setReference] = useState<XSciencePlanReference | null>(null);
  const changed = useCallback((owner: string, plan: XSciencePlanReference | null) => {
    if (owner === 'frame') setReference(plan);
  }, []);
  return (
    <>
      <XScienceRuntimeOperations
        conversationId='frame'
        runtimeState='completed'
        onResumed={vi.fn()}
        onPlanReferenceChange={changed}
      />
      <FileAttachButton
        openFileSelector={vi.fn()}
        planProgress={reference?.progress}
        onViewPlan={reference ? () => emitter.emit('xscience.runtime.plan.open', 'frame') : undefined}
      />
    </>
  );
}

beforeEach(() => {
  mocks.load.mockReset();
  mocks.plan.mockReset().mockResolvedValue(document);
  mocks.approve.mockReset();
  mocks.invalidate = null;
});
afterEach(() => resetRendererAccountScopeForTest());

describe('single-owner View plan projection', () => {
  it('publishes real zero, partial and complete counts without another frame reader', async () => {
    mocks.load.mockResolvedValue(snapshot(0));
    await renderWithI18n(<Harness />, 'en-US', { wrapper: MemoryRouter });
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    expect(await screen.findByRole('menuitem', { name: /View plan.*0\/3/ })).toBeInTheDocument();
    expect(mocks.load).toHaveBeenCalledTimes(1);
    for (const completed of [2, 3]) {
      mocks.load.mockResolvedValue(snapshot(completed));
      vi.useFakeTimers();
      try {
        await act(async () => {
          mocks.invalidate?.();
          await vi.advanceTimersByTimeAsync(100);
        });
      } finally {
        vi.useRealTimers();
      }
      await waitFor(() =>
        expect(
          screen.getByRole('menuitem', {
            name: new RegExp(`View plan.*${completed}/3`),
          })
        ).toBeInTheDocument()
      );
    }
    expect(mocks.plan).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('menuitem', { name: /View plan.*3\/3/ }));
    expect(await screen.findByText('Captured task plan')).toBeInTheDocument();
    expect(mocks.plan).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ artifactId: 'plan', versionId: 'version' }),
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    expect(mocks.approve).not.toHaveBeenCalled();
  });

  it('marks artifact-only legacy content without disabling its existing approval contract', async () => {
    mocks.load.mockResolvedValue({
      ...snapshot(0, null),
      status: 'awaiting_plan_approval',
      planApproval: { artifactId: 'plan', versionId: null },
    });
    mocks.approve.mockResolvedValue({
      snapshot: snapshot(0, null),
      runtime: { state: 'completed' },
    });
    await renderWithI18n(
      <XScienceRuntimeOperations conversationId='frame' runtimeState='waiting_confirmation' onResumed={vi.fn()} />,
      'en-US'
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Review and approve plan' }));
    expect(await screen.findByText('Captured task plan')).toBeInTheDocument();
    expect(screen.getByText('Legacy plan: latest content; no fixed version is available.')).toBeInTheDocument();
    const approve = screen.getByRole('button', { name: 'Approve and execute' });
    expect(approve).toBeEnabled();
    fireEvent.click(approve);
    await waitFor(() =>
      expect(mocks.approve).toHaveBeenCalledWith(
        'frame',
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
        { artifactId: 'plan', versionId: null }
      )
    );
  });

  it('retains the last authoritative plan through a transient snapshot read failure', async () => {
    mocks.load.mockResolvedValue(snapshot(2));
    await renderWithI18n(<Harness />, 'en-US', { wrapper: MemoryRouter });
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    expect(await screen.findByRole('menuitem', { name: /View plan.*2\/3/ })).toBeInTheDocument();
    mocks.load.mockRejectedValue(new Error('temporary read failure'));
    vi.useFakeTimers();
    try {
      await act(async () => {
        mocks.invalidate?.();
        await vi.advanceTimersByTimeAsync(100);
      });
    } finally {
      vi.useRealTimers();
    }
    expect(screen.getByRole('menuitem', { name: /View plan.*2\/3/ })).toBeInTheDocument();
  });

  it('fences delayed presentation callbacks across owner and incarnation changes, including return to the original identity', () => {
    const { result, rerender } = renderHook(
      ({ owner, frame, incarnation }) => useAcpPlanPresentation(owner, frame, incarnation),
      {
        initialProps: { owner: 'owner-a', frame: 'frame', incarnation: 'one' },
      }
    );
    const original = result.current.onPlanReferenceChange;
    const reference = { artifactId: 'plan', versionId: 'version' };
    act(() => original('frame', reference));
    expect(result.current.reference).toEqual(reference);
    rerender({ owner: 'owner-b', frame: 'other', incarnation: 'two' });
    expect(result.current.reference).toBeNull();
    act(() => original('frame', reference));
    expect(result.current.reference).toBeNull();
    rerender({ owner: 'owner-a', frame: 'frame', incarnation: 'one' });
    act(() => original('frame', reference));
    expect(result.current.reference).toBeNull();
    act(() => result.current.onPlanReferenceChange('frame', reference));
    rerender({
      owner: 'owner-a',
      frame: 'frame',
      incarnation: 'new-incarnation',
    });
    expect(result.current.reference).toBeNull();
  });

  it('rejects an old account callback before the replacement view has rendered', () => {
    setRendererAccountOwner('account-a');
    const { result, rerender } = renderHook(() => useAcpPlanPresentation('owner', 'frame', 'incarnation'));
    const original = result.current.onPlanReferenceChange;
    setRendererAccountOwner('account-b');
    act(() =>
      original('frame', {
        artifactId: 'old-account-plan',
        versionId: 'old-version',
      })
    );
    expect(result.current.reference).toBeNull();
    rerender();
    act(() =>
      result.current.onPlanReferenceChange('frame', {
        artifactId: 'current-plan',
        versionId: 'current-version',
      })
    );
    expect(result.current.reference?.artifactId).toBe('current-plan');
  });

  it('opens only the current plan owner across navigation, identity return and incarnation changes', () => {
    const emit = vi.spyOn(emitter, 'emit');
    const { result, rerender, unmount } = renderHook(
      ({ owner, frame, incarnation }) => useAcpPlanPresentation(owner, frame, incarnation),
      { initialProps: { owner: 'owner-a', frame: 'frame-a', incarnation: 'one' } }
    );
    try {
      const original = result.current.openPlan;
      original();
      expect(emit).toHaveBeenLastCalledWith('xscience.runtime.plan.open', 'frame-a');
      emit.mockClear();
      rerender({ owner: 'owner-b', frame: 'frame-b', incarnation: 'two' });
      original();
      expect(emit).not.toHaveBeenCalled();
      result.current.openPlan();
      expect(emit).toHaveBeenLastCalledWith('xscience.runtime.plan.open', 'frame-b');
      emit.mockClear();
      rerender({ owner: 'owner-a', frame: 'frame-a', incarnation: 'one' });
      original();
      expect(emit).not.toHaveBeenCalled();
      const previousIncarnation = result.current.openPlan;
      rerender({ owner: 'owner-a', frame: 'frame-a', incarnation: 'new' });
      previousIncarnation();
      expect(emit).not.toHaveBeenCalled();
      const current = result.current.openPlan;
      current();
      expect(emit).toHaveBeenLastCalledWith('xscience.runtime.plan.open', 'frame-a');
      emit.mockClear();
      unmount();
      current();
      expect(emit).not.toHaveBeenCalled();
    } finally {
      emit.mockRestore();
    }
  });

  it('does not open a plan for an account replaced before the next render', () => {
    setRendererAccountOwner('account-a');
    const emit = vi.spyOn(emitter, 'emit');
    const { result, rerender } = renderHook(() => useAcpPlanPresentation('owner', 'frame', 'incarnation'));
    try {
      const original = result.current.openPlan;
      setRendererAccountOwner('account-b');
      original();
      expect(emit).not.toHaveBeenCalled();
      rerender();
      result.current.openPlan();
      expect(emit).toHaveBeenLastCalledWith('xscience.runtime.plan.open', 'frame');
    } finally {
      emit.mockRestore();
    }
  });
});
