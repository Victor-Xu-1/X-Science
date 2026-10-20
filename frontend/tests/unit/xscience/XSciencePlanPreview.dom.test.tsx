import React, { useCallback, useState } from 'react';
import { act, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import { I18nextProvider } from 'react-i18next';
import { createTestI18n, renderWithI18n } from '../i18nTestUtils';
import {
  normalizeXScienceRuntimeSnapshot,
  type XSciencePlanReference,
} from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { useAcpPlanPresentation } from '@/renderer/pages/conversation/platforms/acp/useAcpPlanPresentation';
import { useXSciencePlanPreview } from '@/renderer/components/xScience/runtime/useXSciencePlanPreview';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const mocks = vi.hoisted(() => ({
  load: vi.fn(),
  plan: vi.fn(),
  versions: vi.fn(),
  approve: vi.fn(),
  invalidate: null as (() => void) | null,
}));
vi.mock('@/renderer/services/xScienceRuntimeOperations', () => ({
  loadXScienceRuntimeSnapshot: mocks.load,
  loadXSciencePlanDocument: mocks.plan,
  loadXSciencePlanVersions: mocks.versions,
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
  mocks.versions.mockReset().mockResolvedValue([]);
  mocks.approve.mockReset();
  mocks.invalidate = null;
});

const contentVersions = [
  { artifactId: 'plan', versionId: 'version-1', contentVersion: 1 },
  { artifactId: 'plan', versionId: 'version-2', contentVersion: 2 },
];
const currentPlan: XSciencePlanReference = {
  ...contentVersions[1],
  progress: { completed: 1, total: 3 },
  revisionNumber: 7,
  revisionCount: 9,
  generatedAt: '2026-10-20T00:00:00Z',
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function previewHook(reference: XSciencePlanReference = currentPlan) {
  const i18n = await createTestI18n('en-US');
  return renderHook(({ frame, plan }) => useXSciencePlanPreview(frame, plan), {
    initialProps: { frame: 'frame', plan: reference },
    wrapper: ({ children }) => <I18nextProvider i18n={i18n}>{children}</I18nextProvider>,
  });
}

describe('Plan content-version inspection', () => {
  it('selects a real historical content version in the Plan drawer and returns to the current approval fence', async () => {
    mocks.load.mockResolvedValue({ ...snapshot(1, 'version-2'), taskPlan: currentPlan, planApproval: currentPlan });
    mocks.versions.mockResolvedValue(contentVersions);
    mocks.plan.mockImplementation(async (reference: XSciencePlanReference) => ({
      ...document,
      taskSummary: reference.versionId === 'version-1' ? 'Historical body' : 'Current body',
    }));
    mocks.approve.mockResolvedValue({ snapshot: snapshot(1, 'version-2'), runtime: { state: 'completed' } });
    await renderWithI18n(
      <XScienceRuntimeOperations conversationId='frame' runtimeState='completed' onResumed={vi.fn()} />,
      'en-US'
    );
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(1));
    act(() => emitter.emit('xscience.runtime.plan.open', 'frame'));
    expect(await screen.findByText('Current body')).toBeInTheDocument();
    expect(screen.getByText('Revision 7 of 9')).toBeInTheDocument();
    expect(mocks.versions).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Content versions' }));
    const selector = await screen.findByRole('combobox', { name: 'Content version' });
    fireEvent.change(selector, { target: { value: 'version-1' } });
    expect(await screen.findByText('Historical body')).toBeInTheDocument();
    expect(selector).toHaveValue('version-1');
    expect(screen.getByText('Historical execution plan')).toBeInTheDocument();
    expect(screen.queryByText('Revision 7 of 9')).not.toBeInTheDocument();
    expect(screen.queryByText(/^Generated /)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve and execute' })).not.toBeInTheDocument();
    expect(mocks.plan).toHaveBeenLastCalledWith(
      contentVersions[0],
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    expect(mocks.approve).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'View current plan' }));
    expect(await screen.findByText('Current body')).toBeInTheDocument();
    expect(screen.getByText('Revision 7 of 9')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Approve and execute' }));
    await waitFor(() =>
      expect(mocks.approve).toHaveBeenCalledExactlyOnceWith(
        'frame',
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
        expect.objectContaining(currentPlan)
      )
    );
  });

  it('separates list loading, error, empty and retry in the existing drawer without rereading the document', async () => {
    const pending = deferred<typeof contentVersions>();
    mocks.load.mockResolvedValue({ ...snapshot(0, 'version-2'), taskPlan: currentPlan });
    mocks.versions
      .mockReturnValueOnce(pending.promise)
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce(contentVersions);
    await renderWithI18n(
      <XScienceRuntimeOperations conversationId='frame' runtimeState='completed' onResumed={vi.fn()} />,
      'en-US'
    );
    await waitFor(() => expect(mocks.load).toHaveBeenCalledTimes(1));
    act(() => emitter.emit('xscience.runtime.plan.open', 'frame'));
    await screen.findByText('Captured task plan');
    fireEvent.click(screen.getByRole('button', { name: 'Content versions' }));
    expect(await screen.findByText('Loading content versions…')).toBeInTheDocument();
    await act(async () => pending.resolve([]));
    expect(await screen.findByText('No content versions recorded.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh versions' }));
    expect(await screen.findByText('Failed to load content versions.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry versions' }));
    expect(await screen.findByText('No content versions recorded.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh versions' }));
    expect(await screen.findByRole('combobox', { name: 'Content version' })).toHaveValue('version-2');
    expect(mocks.plan).toHaveBeenCalledTimes(1);
    expect(mocks.approve).not.toHaveBeenCalled();
  });

  it('retries only the selected exact version and never copies current runtime metadata onto it', async () => {
    mocks.versions.mockResolvedValue(contentVersions);
    mocks.plan
      .mockResolvedValueOnce(document)
      .mockRejectedValueOnce(new Error('exact version unavailable'))
      .mockResolvedValueOnce({ ...document, taskSummary: 'Recovered v1' });
    const { result } = await previewHook();
    await act(async () => {
      result.current.open();
    });
    await act(async () => {
      result.current.loadVersions();
    });
    await act(async () => {
      result.current.selectVersion('version-1');
    });
    expect(result.current.error).toBe('Failed to load the task plan.');
    expect(result.current.reference).toEqual(contentVersions[0]);
    expect(result.current.captureDecision(currentPlan, currentPlan)).toBeNull();
    await act(async () => {
      result.current.retry();
    });
    expect(result.current.document?.taskSummary).toBe('Recovered v1');
    expect(mocks.plan.mock.calls.slice(1).map(([reference]) => reference)).toEqual([
      contentVersions[0],
      contentVersions[0],
    ]);
  });

  it('retains the selected exact body through a same-artifact update without treating content numbers as plan revisions', async () => {
    mocks.versions.mockResolvedValue(contentVersions);
    const { result, rerender } = await previewHook();
    await act(async () => {
      result.current.open();
      result.current.loadVersions();
    });
    await act(async () => {
      result.current.selectVersion('version-1');
    });
    const body = result.current.document;
    const advanced = {
      ...currentPlan,
      versionId: 'version-3',
      contentVersion: 3,
      revisionNumber: 11,
      progress: { completed: 3, total: 3 },
    };
    rerender({ frame: 'frame', plan: advanced });
    expect(result.current.document).toBe(body);
    expect(result.current.reference).toEqual(contentVersions[0]);
    expect(result.current.captureDecision(advanced, advanced)).toBeNull();
    expect(mocks.plan).toHaveBeenCalledTimes(2);
    await act(async () => {
      result.current.open();
    });
    expect(result.current.reference).toEqual(advanced);
    expect(result.current.captureDecision(advanced, advanced)).toEqual(advanced);
  });

  it.each(['close', 'conversation', 'account', 'unmount', 'replacement'] as const)(
    'aborts list and document reads and rejects late replies after %s',
    async (change) => {
      setRendererAccountOwner('account-a');
      const list = deferred<typeof contentVersions>();
      const body = deferred<typeof document>();
      mocks.versions.mockResolvedValueOnce(contentVersions).mockReturnValueOnce(list.promise);
      mocks.plan.mockResolvedValueOnce(document).mockReturnValueOnce(body.promise);
      const { result, rerender, unmount } = await previewHook();
      await act(async () => {
        result.current.open();
        result.current.loadVersions();
      });
      act(() => {
        result.current.selectVersion('version-1');
        result.current.loadVersions();
      });
      const listSignal = mocks.versions.mock.calls[1][1].signal as AbortSignal;
      const documentSignal = mocks.plan.mock.calls[1][1].signal as AbortSignal;
      const oldSelection = result.current.selectVersion;
      if (change === 'close') act(() => result.current.close());
      else if (change === 'unmount') unmount();
      else if (change === 'account') {
        setRendererAccountOwner('account-b');
        act(() => oldSelection('version-2'));
        rerender({ frame: 'frame', plan: currentPlan });
      } else
        rerender({
          frame: change === 'conversation' ? 'other' : 'frame',
          plan:
            change === 'replacement' ? { artifactId: 'different-plan', versionId: 'different-version' } : currentPlan,
        });
      expect(listSignal.aborted).toBe(true);
      expect(documentSignal.aborted).toBe(true);
      await act(async () => {
        list.resolve(contentVersions);
        body.resolve({ ...document, taskSummary: 'Late old body' });
        oldSelection('version-2');
      });
      if (change !== 'unmount') {
        expect(result.current.versions).toBeNull();
        expect(result.current.document?.taskSummary).not.toBe('Late old body');
        expect(result.current.versionsLoading).toBe(false);
      }
      expect(mocks.plan).toHaveBeenCalledTimes(2);
    }
  );

  it('selects only a returned pair and preserves explicit legacy-current behavior', async () => {
    const legacy = { artifactId: 'plan', versionId: null };
    mocks.versions.mockResolvedValue(contentVersions);
    const { result } = await previewHook(legacy);
    await act(async () => {
      result.current.open();
      result.current.loadVersions();
    });
    act(() => result.current.selectVersion('unlisted-version'));
    expect(mocks.plan).toHaveBeenCalledTimes(1);
    expect(result.current.reference).toEqual(legacy);
    await act(async () => {
      result.current.selectVersion('version-1');
    });
    expect(result.current.reference).toEqual(contentVersions[0]);
    expect(result.current.captureDecision(legacy, legacy)).toBeNull();
    await act(async () => {
      result.current.open();
    });
    expect(result.current.reference).toEqual(legacy);
    expect(result.current.captureDecision(legacy, legacy)).toEqual(legacy);
  });
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
