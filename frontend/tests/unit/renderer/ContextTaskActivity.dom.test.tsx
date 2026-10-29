import React, { useCallback, useState } from 'react';
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ContextUsagePanel from '@/renderer/components/xScience/runtime/ContextUsagePanel';
import XScienceTaskStatus from '@/renderer/components/xScience/runtime/XScienceTaskStatus';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { contextRequestActivity } from '@/renderer/components/xScience/runtime/contextRequestActivity';
import { parseContextUsage } from '@/renderer/services/contextUsage';
import { renderWithI18n } from '../i18nTestUtils';

const frame = 'context-task-activity';
const incarnation = 'activity-incarnation';
const wire = () => ({
  status: 'available',
  snapshot: {
    sessionId: frame,
    requestId: 'request-7',
    attempt: 7,
    model: 'fixture-model',
    observedAt: '2026-10-29T00:00:00Z',
    state: 'request',
    source: 'estimated',
    usedTokens: 20,
    limitTokens: 100,
    limitSource: 'configured',
    outputTokens: 0,
    hasMedia: false,
    observationKind: 'model_call',
    inputEstimates: [
      { key: 'systemPrompt', tokens: 4 },
      { key: 'tools', tokens: 2 },
      { key: 'messages', tokens: 12 },
      { key: 'mcp', tokens: 1 },
      { key: 'skills', tokens: 1 },
    ],
    progress: { phase: 'thinking', observedAt: '2026-10-29T00:00:02Z', usedTokens: 20, outputTokens: 0 },
    association: {
      version: 1,
      scope: {
        conversationId: frame,
        frameIncarnationId: incarnation,
        streamUid: 'stream',
        streamEpoch: 1,
        branchId: 'branch',
        branchGeneration: 1,
        throughPublicationSequence: 5,
      },
      inputMessageId: 'input',
      inputEventId: 4,
      inputRevision: 9,
      inputPublicationSequence: 5,
      admissionBranchId: 'branch',
      admissionBranchGeneration: 1,
      claimedInputRevision: 9,
      runnerAttempt: 7,
    },
  },
});

it('retires stale attempt, revision, incarnation, account, terminal and failed-read activity without changing counters', async () => {
  const parsed = parseContextUsage(wire(), frame);
  const activity = contextRequestActivity({ ...parsed, conversationId: frame }, getRendererAccountScopeToken());
  expect(activity).not.toBeNull();
  const base = normalizeXScienceRuntimeSnapshot({
    id: frame,
    root_frame_id: frame,
    status: 'processing',
    runtime_attempt: 7,
    runtime_input_revision: 9,
    runtime_active: true,
    can_cancel: true,
  });
  const make = (changes: Record<string, unknown> = {}) => (
    <XScienceTaskStatus
      snapshot={base}
      loading={false}
      snapshotError={null}
      onRefresh={() => undefined}
      {...({ contextActivity: activity, contextFrameIncarnationId: incarnation, ...changes } as object)}
    />
  );
  const view = await renderWithI18n(make(), 'en-US');
  expect(screen.getByTestId('x-science-task-details-trigger')).toHaveTextContent('Thinking');
  for (const changes of [
    { snapshot: { ...base, runtimeAttempt: 8 } },
    { snapshot: { ...base, runtimeInputRevision: 10 } },
    { contextFrameIncarnationId: 'replacement-incarnation' },
    { snapshot: { ...base, status: 'completed', runtimeActive: false } },
    { snapshotError: 'refresh' },
  ]) {
    view.rerender(make(changes));
    expect(screen.getByTestId('x-science-task-details-trigger')).not.toHaveTextContent('Thinking');
  }
  setRendererAccountOwner('replacement-account');
  view.rerender(make());
  expect(screen.getByTestId('x-science-task-details-trigger')).not.toHaveTextContent('Thinking');
  expect(
    contextRequestActivity({ ...parsed, conversationId: frame, refreshState: 'stale' }, getRendererAccountScopeToken())
  ).toBeNull();
});

it('keeps legacy context records readable without granting live activity and refuses mismatched typed authority', () => {
  const current = wire();
  const legacy: Record<string, unknown> = { ...current.snapshot, attempt: 0 };
  delete legacy.association;
  delete legacy.observationKind;
  const parsed = parseContextUsage({ status: 'available', snapshot: legacy }, frame);
  expect(parsed.status).toBe('available');
  expect(contextRequestActivity({ ...parsed, conversationId: frame }, getRendererAccountScopeToken())).toBeNull();
  expect(() =>
    parseContextUsage(
      {
        ...current,
        snapshot: { ...current.snapshot, association: { ...current.snapshot.association, runnerAttempt: 8 } },
      },
      frame
    )
  ).toThrow('Invalid context activity association');
  expect(() =>
    parseContextUsage({ ...current, snapshot: { ...current.snapshot, observationKind: 'timer' } }, frame)
  ).toThrow('Invalid context observation kind');
});

it('uses the original context reader to revoke activity after refresh failure and ignore a late old-account response', async () => {
  let deliver: ((response: Response) => void) | undefined;
  let mode: 'ready' | 'failed' | 'held' = 'ready';
  const fetcher = vi.fn(() =>
    mode === 'ready'
      ? Promise.resolve(new Response(JSON.stringify(wire())))
      : mode === 'failed'
        ? Promise.resolve(new Response('', { status: 403 }))
        : new Promise<Response>((resolve) => {
            deliver = resolve;
          })
  );
  vi.stubGlobal('fetch', fetcher);
  const observed = vi.fn();
  await renderWithI18n(<ContextUsagePanel conversationId={frame} active onRequestActivityChange={observed} />, 'en-US');
  await waitFor(() => expect(observed.mock.calls.some(([value]) => value?.phase === 'thinking')).toBe(true));
  mode = 'failed';
  window.dispatchEvent(new Event('online'));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(observed.mock.lastCall?.[0]).toBeNull());
  mode = 'held';
  window.dispatchEvent(new Event('online'));
  await waitFor(() => expect(deliver).toBeTypeOf('function'));
  const oldReply = deliver!;
  mode = 'failed';
  setRendererAccountOwner('replacement-account');
  await waitFor(() => expect(observed.mock.lastCall?.[0]).toBeNull());
  oldReply(new Response(JSON.stringify(wire())));
  await waitFor(() => expect(observed.mock.lastCall?.[0]).toBeNull());
});

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('activity-account');
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('shares one real context read with task status and overview while settled model calls remain zero', async () => {
  const fetcher = vi.fn(async () => new Response(JSON.stringify(wire())));
  vi.stubGlobal('fetch', fetcher);
  function Fixture() {
    const [activity, setActivity] = useState<unknown>(null);
    const capture = useCallback((value: unknown) => setActivity(value), []);
    const snapshot = normalizeXScienceRuntimeSnapshot({
      id: frame,
      root_frame_id: frame,
      status: 'processing',
      runtime_attempt: 7,
      runtime_input_revision: 9,
      runtime_active: true,
      can_cancel: true,
    });
    return (
      <>
        <ContextUsagePanel conversationId={frame} active {...({ onRequestActivityChange: capture } as object)} />
        <XScienceTaskStatus
          snapshot={snapshot}
          loading={false}
          snapshotError={null}
          onRefresh={() => undefined}
          taskCenterMetrics={{
            total: { tokenUsage: null, modelCallCount: 0, toolCallCount: 0 },
            latest: { tokenUsage: null, modelCallCount: 0, toolCallCount: 0 },
          }}
          {...({ contextActivity: activity, contextFrameIncarnationId: incarnation } as object)}
        />
      </>
    );
  }
  await renderWithI18n(<Fixture />, 'en-US');
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(screen.getByTestId('x-science-task-details-trigger')).toHaveTextContent('Thinking'));
  fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
  const panel = await screen.findByTestId('x-science-task-status-panel');
  expect(within(panel).getByTestId('x-science-task-request-activity')).toHaveTextContent('Thinking');
  expect(within(panel).getByTestId('x-science-task-metric-model-calls')).toHaveTextContent('Latest 0');
  expect(within(panel).getByTestId('x-science-task-metric-model-calls')).toHaveTextContent('Model calls');
  expect(fetcher).toHaveBeenCalledTimes(1);
});
