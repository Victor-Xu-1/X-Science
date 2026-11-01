import React from 'react';
import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { selectXScienceBranch } from '@/renderer/services/xScienceConversationBranches';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
const read = vi.hoisted(() => ({ load: vi.fn(), invalidate: vi.fn(), resumed: vi.fn(), originalLoad: vi.fn() }));
vi.mock('@arco-design/web-react', async (original) => ({
  ...(await original<typeof import('@arco-design/web-react')>()),
  Message: { useMessage: () => [{ success: vi.fn(), error: vi.fn(), info: vi.fn() }, null] },
}));
vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  useRealtime: () => ({ runtime: {}, snapshot: { identity: 'authenticated', status: 'connected' } }),
}));
vi.mock('@/renderer/services/xScienceFrameReads', async (original) => {
  const actual = await original<typeof import('@/renderer/services/xScienceFrameReads')>();
  return {
    ...actual,
    invalidateXScienceFrameReads: (id: string) => {
      read.invalidate(id);
      actual.invalidateXScienceFrameReads(id);
    },
  };
});
vi.mock('@/renderer/services/xScienceRuntimeOperations', async (original) => {
  const actual = await original<typeof import('@/renderer/services/xScienceRuntimeOperations')>();
  read.originalLoad.mockImplementation(actual.loadXScienceRuntimeSnapshot);
  return {
    loadXScienceRuntimeSnapshot: read.load,
    loadXScienceExecutionLogPage: vi.fn(),
    resumeXScienceFrame: vi.fn(),
    resolveXScienceInputRequest: vi.fn(),
    resolveXScienceAskUserRequest: vi.fn(),
    loadXSciencePlanDocument: vi.fn(),
    approveXSciencePlan: vi.fn(),
    discardXSciencePlan: vi.fn(),
    subscribeXScienceRuntimeInvalidation: () => () => {},
  };
});
import XScienceRuntimeOperations from '@/renderer/components/xScience/runtime/XScienceRuntimeOperations';
const complete = (id = 'synthetic-frame') =>
  normalizeXScienceRuntimeSnapshot({
    id,
    root_frame_id: id,
    status: 'completed',
    runtime_active: false,
    runtime_task_active: false,
    created_at: '2026-01-01T00:00:00Z',
    completed_at: '2026-01-01T00:00:05Z',
  });
async function settle() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}
const mount = () =>
  renderWithI18n(
    <XScienceRuntimeOperations conversationId='synthetic-frame' runtimeState='idle' onResumed={read.resumed} />
  );
beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  read.load.mockReset();
  setRendererAccountOwner('snapshot-owner');
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
  vi.useRealTimers();
  vi.restoreAllMocks();
});
it('recovers an inactive first snapshot failure by rereading the original authority without a task resend', async () => {
  read.load.mockRejectedValueOnce(new TypeError('controlled initial read failure')).mockResolvedValue(complete());
  await mount();
  await settle();
  expect(read.load).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'unavailable');
  await advance(2500);
  expect(read.load).toHaveBeenCalledTimes(2);
  expect(read.invalidate).toHaveBeenCalledWith('synthetic-frame');
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  expect(read.resumed).not.toHaveBeenCalled();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
it('bounds failed initial reads and keeps the original explicit refresh usable after exhaustion', async () => {
  read.load.mockRejectedValue(new TypeError('controlled unavailable authority'));
  await mount();
  await settle();
  await advance(2500);
  await advance(5000);
  await advance(10000);
  expect(read.load).toHaveBeenCalledTimes(4);
  await advance(60000);
  expect(read.load).toHaveBeenCalledTimes(4);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'unavailable');
  read.load.mockResolvedValue(complete());
  fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
  await advance(200);
  fireEvent.click(screen.getByTestId('x-science-task-refresh'));
  await settle();
  expect(read.load).toHaveBeenCalledTimes(5);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  expect(read.resumed).not.toHaveBeenCalled();
});
it('never overlaps an initial recovery read while the same original request is pending', async () => {
  const held = deferred<ReturnType<typeof complete>>();
  read.load.mockRejectedValueOnce(new TypeError('controlled first read failure')).mockReturnValueOnce(held.promise);
  await mount();
  await settle();
  await advance(2500);
  expect(read.load).toHaveBeenCalledTimes(2);
  await advance(60000);
  expect(read.load).toHaveBeenCalledTimes(2);
  await act(async () => {
    held.resolve(complete());
  });
  await settle();
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
});
it.each(['dispose', 'account'] as const)('cancels a scheduled initial recovery on %s retirement', async (change) => {
  read.load.mockRejectedValue(new TypeError('controlled first read failure'));
  const view = await mount();
  await settle();
  if (change === 'dispose') view.unmount();
  else {
    read.load.mockReturnValueOnce(deferred<ReturnType<typeof complete>>().promise);
    act(() => {
      setRendererAccountOwner('other-owner');
    });
  }
  await advance(60000);
  expect(read.load).toHaveBeenCalledTimes(change === 'dispose' ? 1 : 2);
});
it.each(['conversation', 'incarnation', 'branch', 'account'] as const)(
  'fences a late snapshot and retires the old read on %s change',
  async (change) => {
    const held = deferred<ReturnType<typeof complete>>();
    const observed = vi.fn();
    read.load.mockReturnValueOnce(held.promise).mockImplementation((id: string) => Promise.resolve(complete(id)));
    const props = {
      conversationId: 'synthetic-frame',
      contextFrameIncarnationId: 'first-incarnation',
      runtimeState: 'idle',
      onResumed: read.resumed,
      onRuntimeUpdated: observed,
    };
    const view = await renderWithI18n(<XScienceRuntimeOperations {...props} />);
    await settle();
    const oldSignal = read.load.mock.calls[0][1].signal as AbortSignal;
    const next = { ...props };
    if (change === 'conversation') next.conversationId = 'second-frame';
    if (change === 'incarnation') next.contextFrameIncarnationId = 'second-incarnation';
    if (change === 'branch')
      act(() => {
        selectXScienceBranch('synthetic-frame', 'br_12345678');
      });
    if (change === 'account')
      act(() => {
        setRendererAccountOwner('other-owner');
      });
    view.rerender(<XScienceRuntimeOperations {...next} />);
    await settle();
    expect(oldSignal.aborted).toBe(true);
    expect(read.load).toHaveBeenCalledTimes(2);
    const beforeLate = observed.mock.calls.length;
    await act(async () => {
      held.resolve(normalizeXScienceRuntimeSnapshot({ id: 'synthetic-frame', status: 'failed' }));
    });
    await advance(60000);
    expect(read.load).toHaveBeenCalledTimes(2);
    expect(observed).toHaveBeenCalledTimes(beforeLate);
    expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  }
);

it('retires the original pending frame cache before a new incarnation reads real canonical transport', async () => {
  const held = deferred<Response>();
  const fetch = vi
    .fn()
    .mockReturnValueOnce(held.promise)
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: 'synthetic-frame',
          root_frame_id: 'synthetic-frame',
          status: 'completed',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      )
    );
  vi.stubGlobal('fetch', fetch);
  read.load.mockImplementation(read.originalLoad);
  const observed = vi.fn();
  const props = {
    conversationId: 'synthetic-frame',
    contextFrameIncarnationId: 'original-incarnation',
    runtimeState: 'idle',
    onResumed: read.resumed,
    onRuntimeUpdated: observed,
  };
  const view = await renderWithI18n(<XScienceRuntimeOperations {...props} />);
  await settle();
  expect(fetch).toHaveBeenCalledTimes(1);
  view.rerender(<XScienceRuntimeOperations {...props} contextFrameIncarnationId='new-incarnation' />);
  await settle();
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(String(fetch.mock.calls[1][0])).toBe('/api/frames/synthetic-frame');
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  const beforeLate = observed.mock.calls.length;
  await act(async () => {
    held.resolve(
      new Response(JSON.stringify({ id: 'synthetic-frame', status: 'failed' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );
  });
  await settle();
  expect(observed).toHaveBeenCalledTimes(beforeLate);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  expect(read.resumed).not.toHaveBeenCalled();
});

it('hands an initially failed active read to bounded recovery when the task becomes idle before its heartbeat', async () => {
  read.load.mockRejectedValueOnce(new TypeError('controlled active initial failure')).mockResolvedValue(complete());
  const props = {
    conversationId: 'synthetic-frame',
    runtimeState: 'running',
    runtimeView: { state: 'running', terminalProjectionPending: false },
    onResumed: read.resumed,
  };
  const view = await renderWithI18n(<XScienceRuntimeOperations {...props} />);
  await settle();
  expect(read.load).toHaveBeenCalledTimes(1);
  view.rerender(
    <XScienceRuntimeOperations
      {...props}
      runtimeState='idle'
      runtimeView={{ state: 'idle', terminalProjectionPending: false }}
    />
  );
  await settle();
  await advance(2500);
  expect(read.load).toHaveBeenCalledTimes(2);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  expect(read.resumed).not.toHaveBeenCalled();
});
it('clears a successful independent component snapshot on account reset without an external rerender', async () => {
  const held = deferred<ReturnType<typeof complete>>();
  read.load.mockResolvedValueOnce(complete()).mockReturnValueOnce(held.promise);
  await mount();
  await settle();
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  act(() => {
    setRendererAccountOwner('next-independent-owner');
  });
  await settle();
  expect(screen.queryByTestId('x-science-runtime-status')).not.toHaveAttribute('data-state', 'completed');
  expect(read.load).toHaveBeenCalledTimes(2);
  await act(async () => {
    held.resolve(complete());
  });
  await settle();
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
});

it('keeps one bounded recovery budget across repeated active to inactive handoffs in the same scope', async () => {
  read.load.mockRejectedValue(new TypeError('controlled sustained read failure'));
  const props = {
    conversationId: 'synthetic-frame',
    runtimeState: 'running',
    runtimeView: { state: 'running', terminalProjectionPending: false },
    onResumed: read.resumed,
  };
  const view = await renderWithI18n(<XScienceRuntimeOperations {...props} />);
  await settle();
  const inactive = async () => {
    view.rerender(<XScienceRuntimeOperations {...props} runtimeState='running' />);
    await settle();
    view.rerender(
      <XScienceRuntimeOperations
        {...props}
        runtimeState='idle'
        runtimeView={{ state: 'idle', terminalProjectionPending: false }}
      />
    );
    await settle();
  };
  await inactive();
  await advance(2500);
  await inactive();
  await advance(5000);
  await inactive();
  await advance(10000);
  expect(read.load).toHaveBeenCalledTimes(4);
  for (let index = 0; index < 3; index += 1) {
    await inactive();
    await advance(30000);
  }
  expect(read.load).toHaveBeenCalledTimes(4);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'unavailable');
  expect(read.resumed).not.toHaveBeenCalled();
});

it('hands off a pending heartbeat only after its original request releases the single inflight slot', async () => {
  const held = deferred<ReturnType<typeof complete>>();
  read.load
    .mockRejectedValueOnce(new TypeError('controlled initial active failure'))
    .mockReturnValueOnce(held.promise)
    .mockResolvedValue(complete());
  const props = {
    conversationId: 'synthetic-frame',
    runtimeState: 'running',
    runtimeView: { state: 'running', terminalProjectionPending: false },
    onResumed: read.resumed,
  };
  const view = await renderWithI18n(<XScienceRuntimeOperations {...props} />);
  await settle();
  await advance(5000);
  expect(read.load).toHaveBeenCalledTimes(2);
  view.rerender(
    <XScienceRuntimeOperations
      {...props}
      runtimeState='idle'
      runtimeView={{ state: 'idle', terminalProjectionPending: false }}
    />
  );
  await settle();
  await advance(20000);
  expect(read.load).toHaveBeenCalledTimes(2);
  await act(async () => {
    held.resolve(complete());
  });
  await settle();
  await advance(2500);
  expect(read.load).toHaveBeenCalledTimes(3);
  expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
  expect(read.resumed).not.toHaveBeenCalled();
});
