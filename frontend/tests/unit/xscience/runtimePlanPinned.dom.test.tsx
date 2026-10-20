import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import type { XSciencePlanDocument } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

const runtime = vi.hoisted(() => ({
  snapshot: vi.fn(),
  log: vi.fn(),
  plan: vi.fn(),
  approve: vi.fn(),
  discard: vi.fn(),
  subscribe: vi.fn(),
}));

vi.mock('@arco-design/web-react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@arco-design/web-react')>()),
  Message: { useMessage: () => [{ success: vi.fn(), error: vi.fn(), info: vi.fn() }, null] },
}));
vi.mock('@/renderer/services/xScienceRuntimeOperations', () => ({
  loadXScienceRuntimeSnapshot: runtime.snapshot,
  loadXScienceExecutionLogPage: runtime.log,
  loadXSciencePlanDocument: runtime.plan,
  approveXSciencePlan: runtime.approve,
  discardXSciencePlan: runtime.discard,
  subscribeXScienceRuntimeInvalidation: runtime.subscribe,
  resumeXScienceFrame: vi.fn(),
  resolveXScienceInputRequest: vi.fn(),
  resolveXScienceAskUserRequest: vi.fn(),
}));
vi.mock('@/renderer/services/xScienceFrameReads', () => ({ invalidateXScienceFrameReads: vi.fn() }));
vi.mock('@/renderer/services/xScienceAnnotations', () => ({ loadXScienceFrameVerification: vi.fn() }));
vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  useRealtime: () => ({ runtime: {}, snapshot: { identity: 'authenticated', status: 'connected' } }),
}));

import XScienceRuntimeOperations from '@/renderer/components/xScience/runtime/XScienceRuntimeOperations';
import { emitter } from '@/renderer/utils/emitter';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const reference = (version: string) => ({
  artifactId: 'fixture-plan',
  versionId: `version-${version}`,
  revisionNumber: version === 'A' ? 1 : 2,
  revisionCount: version === 'A' ? 1 : 2,
});
const snapshot = (version = 'A', frameId = 'frame-plan') => ({
  frameId,
  rootFrameId: frameId,
  status: 'awaiting_plan_approval',
  statusDescription: 'Synthetic plan review',
  error: null,
  updatedAt: '2026-07-11T04:30:00.000Z',
  canCancel: true,
  canResume: false,
  pendingInputRequests: [],
  taskPlan: reference(version),
  planApproval: reference(version),
});
const document = (version: string): XSciencePlanDocument => ({
  version: 1,
  taskSummary: `Immutable document ${version}`,
  phases: [],
  feasibility: null,
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
};

describe('C397 pinned immutable Plan preview', () => {
  let invalidate: (() => void) | undefined;
  beforeEach(() => {
    for (const mock of Object.values(runtime)) mock.mockReset();
    invalidate = undefined;
    runtime.snapshot.mockResolvedValue(snapshot());
    runtime.log.mockResolvedValue({ records: [], nextBefore: null, total: 0 });
    runtime.plan.mockImplementation(async (target: ReturnType<typeof reference>) =>
      document(target.versionId === 'version-A' ? 'A' : 'B')
    );
    runtime.subscribe.mockImplementation((_owner: string, listener: () => void) => {
      invalidate = listener;
      return vi.fn();
    });
  });

  const mount = async () => {
    const view = await renderWithI18n(
      <XScienceRuntimeOperations conversationId='frame-plan' runtimeState='waiting_confirmation' onResumed={vi.fn()} />
    );
    await screen.findByRole('button', { name: '查看计划并审批' });
    act(() => emitter.emit('xscience.runtime.plan.open', 'frame-plan'));
    return view;
  };
  const advance = async () => {
    const count = runtime.snapshot.mock.calls.length;
    runtime.snapshot.mockResolvedValue(snapshot('B'));
    act(() => invalidate?.());
    await waitFor(() => expect(runtime.snapshot.mock.calls.length).toBeGreaterThan(count));
    await act(async () => {});
  };

  it('keeps selected A read-only without inventing historical revision metadata when current Plan becomes B', async () => {
    await mount();
    expect(await screen.findByText('Immutable document A')).toBeInTheDocument();
    await advance();
    expect(screen.getByText('Immutable document A')).toBeInTheDocument();
    expect(screen.getByText('此计划已被替换，仅供查看。')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-plan-review')).not.toHaveTextContent(/修订 \d/);
    expect(runtime.plan.mock.calls[0][0]).toEqual(reference('A'));
    expect(screen.queryByRole('button', { name: '批准并执行' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '丢弃计划并结束任务' })).not.toBeInTheDocument();
    expect(runtime.approve).not.toHaveBeenCalled();
    expect(runtime.discard).not.toHaveBeenCalled();
  });

  it('admits a late A document into its still-selected A view, not current B', async () => {
    const pending = deferred<XSciencePlanDocument>();
    runtime.plan.mockReturnValueOnce(pending.promise);
    await mount();
    await waitFor(() => expect(runtime.plan).toHaveBeenCalledOnce());
    await advance();
    await act(async () => pending.resolve(document('A')));
    expect(await screen.findByText('Immutable document A')).toBeInTheDocument();
    expect(screen.getByText('此计划已被替换，仅供查看。')).toBeInTheDocument();
    expect(runtime.plan).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: '批准并执行' })).not.toBeInTheDocument();
  });

  it('retries a failed selected A using A after B becomes current; opening current is explicit', async () => {
    runtime.plan.mockRejectedValueOnce(new Error('synthetic unavailable exact version'));
    await mount();
    await screen.findByText('任务计划加载失败。');
    await advance();
    fireEvent.click(screen.getByRole('button', { name: '重新加载计划' }));
    expect(await screen.findByText('Immutable document A')).toBeInTheDocument();
    expect(runtime.plan.mock.calls.map(([target]) => target)).toEqual([reference('A'), reference('A')]);
    fireEvent.click(screen.getByRole('button', { name: '查看当前计划' }));
    expect(await screen.findByText('Immutable document B')).toBeInTheDocument();
    expect(runtime.plan.mock.calls[2][0]).toEqual(reference('B'));
    expect(screen.queryByText('此计划已被替换，仅供查看。')).not.toBeInTheDocument();
  });

  it('does not restore A after Close and an explicit reopen selects B', async () => {
    const pending = deferred<XSciencePlanDocument>();
    runtime.plan.mockReturnValueOnce(pending.promise);
    const view = await mount();
    await waitFor(() => expect(runtime.plan).toHaveBeenCalledOnce());
    await advance();
    fireEvent.click(view.container.ownerDocument.querySelector('.arco-drawer-close-icon')!);
    act(() => emitter.emit('xscience.runtime.plan.open', 'frame-plan'));
    expect(await screen.findByText('Immutable document B')).toBeInTheDocument();
    await act(async () => pending.resolve(document('A')));
    expect(screen.queryByText('Immutable document A')).not.toBeInTheDocument();
    expect(screen.getByText('Immutable document B')).toBeInTheDocument();
  });

  it('isolates an owner switch from the previous late document and request signal', async () => {
    setRendererAccountOwner('fixture-plan-owner-A');
    try {
      const pending = deferred<XSciencePlanDocument>();
      runtime.plan.mockReturnValueOnce(pending.promise);
      const view = await mount();
      await waitFor(() => expect(runtime.plan).toHaveBeenCalledOnce());
      const originalSignal = runtime.plan.mock.calls[0][1].signal as AbortSignal;
      setRendererAccountOwner('fixture-plan-owner-B');
      runtime.snapshot.mockResolvedValue(snapshot('B', 'frame-other'));
      view.rerender(
        <XScienceRuntimeOperations
          conversationId='frame-other'
          runtimeState='waiting_confirmation'
          onResumed={vi.fn()}
        />
      );
      await waitFor(() => expect(runtime.snapshot).toHaveBeenCalledWith('frame-other', expect.anything()));
      expect(originalSignal.aborted).toBe(true);
      act(() => emitter.emit('xscience.runtime.plan.open', 'frame-other'));
      expect(await screen.findByText('Immutable document B')).toBeInTheDocument();
      await act(async () => pending.resolve(document('A')));
      expect(screen.queryByText('Immutable document A')).not.toBeInTheDocument();
      expect(screen.getByText('Immutable document B')).toBeInTheDocument();
    } finally {
      resetRendererAccountScopeForTest();
    }
  });

  it('does not label a versionless changed source as immutable history or retry another current version', async () => {
    const legacy = {
      ...snapshot(),
      taskPlan: { artifactId: 'fixture-plan', versionId: null },
      planApproval: { artifactId: 'fixture-plan', versionId: null },
    };
    runtime.snapshot.mockResolvedValue(legacy);
    runtime.plan.mockResolvedValueOnce(document('Legacy'));
    await mount();
    expect(await screen.findByText('Immutable document Legacy')).toBeInTheDocument();
    expect(runtime.plan.mock.calls[0][0]).toEqual(legacy.taskPlan);
    await advance();
    expect(screen.queryByText('Immutable document Legacy')).not.toBeInTheDocument();
    expect(screen.queryByText('此计划已被替换，仅供查看。')).not.toBeInTheDocument();
    expect(screen.getByText('任务计划已更新。请重新加载并审阅当前计划后再操作。')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '重新加载计划' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '批准并执行' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看当前计划' }));
    expect(await screen.findByText('Immutable document B')).toBeInTheDocument();
    expect(runtime.plan.mock.calls[1][0]).toEqual(reference('B'));
  });

  it('does not let a late rejection or finally from closed A replace the still-loading B view', async () => {
    const first = deferred<XSciencePlanDocument>();
    const second = deferred<XSciencePlanDocument>();
    runtime.plan.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const view = await mount();
    await waitFor(() => expect(runtime.plan).toHaveBeenCalledOnce());
    fireEvent.click(view.container.ownerDocument.querySelector('.arco-drawer-close-icon')!);
    await advance();
    act(() => emitter.emit('xscience.runtime.plan.open', 'frame-plan'));
    await waitFor(() => expect(runtime.plan).toHaveBeenCalledTimes(2));
    await act(async () => first.reject(new Error('late original read failure')));
    expect(screen.queryByText('任务计划加载失败。')).not.toBeInTheDocument();
    expect(screen.getByText('正在加载任务计划')).toBeInTheDocument();
    await act(async () => second.resolve(document('B')));
    expect(await screen.findByText('Immutable document B')).toBeInTheDocument();
  });

  it.each(['success', 'failure'])(
    'late settled %s for an old decision cannot close or overwrite a reopened same-version preview',
    async (outcome) => {
      const action = deferred<{ snapshot: ReturnType<typeof snapshot>; runtime: { state: string } }>();
      runtime.approve.mockReturnValueOnce(action.promise);
      const onResumed = vi.fn();
      const view = await renderWithI18n(
        <XScienceRuntimeOperations
          conversationId='frame-plan'
          runtimeState='waiting_confirmation'
          onResumed={onResumed}
        />
      );
      fireEvent.click(await screen.findByRole('button', { name: '查看计划并审批' }));
      expect(await screen.findByText('Immutable document A')).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: '批准并执行' }));
      await waitFor(() => expect(runtime.approve).toHaveBeenCalledOnce());
      fireEvent.click(view.container.ownerDocument.querySelector('.arco-drawer-close-icon')!);
      act(() => emitter.emit('xscience.runtime.plan.open', 'frame-plan'));
      await waitFor(() => expect(runtime.plan).toHaveBeenCalledTimes(2));
      expect(await screen.findByText('Immutable document A')).toBeInTheDocument();
      await act(async () => {
        if (outcome === 'success') action.resolve({ snapshot: snapshot(), runtime: { state: 'running' } });
        else action.reject(new Error('synthetic old decision failure'));
      });
      expect(screen.getByText('Immutable document A')).toBeInTheDocument();
      expect(runtime.approve).toHaveBeenCalledOnce();
      expect(onResumed).toHaveBeenCalledTimes(outcome === 'success' ? 1 : 0);
    }
  );
});
