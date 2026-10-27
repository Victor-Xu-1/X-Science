import React, { useRef, useState } from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ContextUsagePanel from '@/renderer/components/xScience/runtime/ContextUsagePanel';
import ContextWindowInspector from '@/renderer/components/xScience/runtime/ContextWindowInspector';
import {
  CALL_COLORS,
  callSegments,
  reportedCallToken,
  summarizeInspectionCalls,
  inspectionTurnPoints,
} from '@/renderer/components/xScience/runtime/contextInspectionModel';
import {
  fetchContextUsage,
  parseContextUsage,
  type ContextCallUsage,
  type ContextUsageSnapshot,
} from '@/renderer/services/contextUsage';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';

const conversationId = 'inspection-fixture';
const inspection = () => ({
  version: 1,
  scope: {
    conversationId,
    frameIncarnationId: 'incarnation-one',
    streamUid: 'frame:inspection-fixture',
    streamEpoch: 1,
    branchId: 'branch-one',
    branchGeneration: 1,
    throughPublicationSequence: 12,
  },
  coverage: {
    association: 'partial',
    retainedCallCount: 1,
    observedSampleCount: 3,
    unassociatedCallCount: 2,
    truncated: false,
  },
  calls: [
    {
      id: 'call-one',
      inputMessageId: 'input-one',
      inputEventId: 2,
      inputRevision: 1,
      runnerAttempt: 1,
      model: 'fixture-model',
      state: 'complete',
      observedAt: '2026-10-22T00:01:00Z',
      usedTokens: 40,
      limitTokens: 100,
      limitSource: 'configured',
      outputTokens: 4,
      callUsage: {
        input_tokens: 11,
        output_tokens: 4,
        total_tokens: 15,
        reported: { input: true, output: true, total: true, cache_read: false, cache_write: false },
      },
    },
  ],
  turns: [
    {
      id: 'turn-one',
      inputMessageId: 'input-one',
      inputRevision: 1,
      status: 'completed',
      terminalEventId: 8,
      completedAt: '2026-10-22T00:02:00Z',
      runnerAttempts: [1],
      callIds: ['call-one'],
      outcomes: [
        {
          id: 'outcome-one',
          status: 'completed',
          terminalEventId: 8,
          completedAt: '2026-10-22T00:02:00Z',
          runnerAttempt: 1,
          callIds: ['call-one'],
        },
      ],
    },
    {
      id: 'turn-two',
      inputMessageId: 'input-two',
      inputRevision: 2,
      status: 'failed',
      terminalEventId: 12,
      completedAt: '2026-10-22T00:03:00Z',
      runnerAttempts: [2],
      callIds: [],
      outcomes: [
        {
          id: 'outcome-two',
          status: 'failed',
          terminalEventId: 12,
          completedAt: '2026-10-22T00:03:00Z',
          runnerAttempt: 2,
          callIds: [],
        },
      ],
    },
  ],
});

function MenuHarness({ id = conversationId }: { id?: string }) {
  const [sequence, setSequence] = useState(0);
  const opener = useRef<HTMLButtonElement>(null);
  return (
    <>
      <button ref={opener} onClick={() => setSequence((value) => value + 1)}>
        More context
      </button>
      <ContextUsagePanel
        conversationId={id}
        openRequest={sequence}
        returnFocus={opener.current}
        triggerVisible={false}
      />
    </>
  );
}

beforeEach(() => {
  resetRendererAccountScopeForTest();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify({ status: 'unavailable', inspection: inspection() })))
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetRendererAccountScopeForTest();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

async function openMenu() {
  fireEvent.click(screen.getByRole('button', { name: 'More context' }));
  return screen.findByRole('dialog', { name: 'Context window' });
}

describe('Transcript-bound context inspector', () => {
  it.each([513, 4000, 4001])(
    'matches the backend outcomes projection bound for %s retained terminal records',
    (size) => {
      const data = inspection();
      data.calls = [];
      data.coverage.retainedCallCount = 0;
      data.turns = [
        {
          ...data.turns[1],
          outcomes: Array.from({ length: size }, (_, index) => ({
            id: 'retained-outcome-' + index,
            status: index < 2000 ? 'failed' : 'reclaimed',
            terminalEventId: index + 1,
            completedAt: '2026-10-22T00:03:00Z',
            runnerAttempt: (index % 2000) + 1,
            callIds: [],
          })),
        },
      ];
      const parse = () => parseContextUsage({ status: 'unavailable', inspection: data }, conversationId);
      if (size > 4000) expect(parse).toThrow('Invalid context inspection turn');
      else expect(parse().inspection?.turns[0].outcomes).toHaveLength(size);
    }
  );
  it('retains every real failed or cancelled run plus only the latest completed run for each logical turn', async () => {
    const data = inspection();
    data.turns[0].runnerAttempts = [1, 3, 4, 5];
    data.turns[0].outcomes = [
      {
        id: 'failed-run',
        status: 'failed',
        terminalEventId: 4,
        completedAt: '2026-10-22T00:01:00Z',
        runnerAttempt: 1,
        callIds: ['call-one'],
      },
      {
        id: 'old-completed-run',
        status: 'completed',
        terminalEventId: 6,
        completedAt: '2026-10-22T00:01:10Z',
        runnerAttempt: 3,
        callIds: [],
      },
      {
        id: 'cancelled-run',
        status: 'cancelled',
        terminalEventId: 7,
        completedAt: '2026-10-22T00:01:20Z',
        runnerAttempt: 4,
        callIds: [],
      },
      {
        id: 'latest-completed-run',
        status: 'completed',
        terminalEventId: 8,
        completedAt: '2026-10-22T00:02:00Z',
        runnerAttempt: 5,
        callIds: [],
      },
    ];
    data.turns.push({
      ...data.turns[1],
      id: 'pending-turn',
      inputMessageId: 'input-pending',
      inputRevision: 3,
      status: 'running',
      runnerAttempts: [6],
      callIds: [],
      outcomes: [],
    });
    vi.mocked(fetch).mockImplementation(
      async () => new Response(JSON.stringify({ status: 'unavailable', inspection: data }))
    );
    await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    const points = screen.getAllByTestId('context-inspector-turn-point');
    expect(points).toHaveLength(4);
    expect(screen.getByTestId('context-inspector-in-progress')).toHaveTextContent('Input revision 3');
    expect(screen.getByTestId('context-inspector-in-progress')).toHaveTextContent('Running');
    fireEvent.click(points[0]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Failed');
    fireEvent.click(points[1]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Cancelled');
    fireEvent.click(points[2]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Completed');
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    expect(screen.getAllByTestId('context-inspector-call-point')).toHaveLength(1);
    expect(screen.getAllByTestId('context-inspector-call-band')).toHaveLength(1);
  });
  it('retains real terminal turns when the latest usage is unavailable, without making history rows into turns', () => {
    const payload = { status: 'unavailable', inspection: inspection() };
    expect(parseContextUsage(payload, conversationId)).toHaveProperty('inspection', payload.inspection);
  });

  it('opens a separate Turns/Calls inspector from the menu and preserves the ring compact entry', async () => {
    await renderWithI18n(<MenuHarness />, 'en-US');
    const opener = screen.getByRole('button', { name: 'More context' });
    opener.focus();
    fireEvent.click(opener);
    const dialog = await screen.findByRole('dialog', { name: 'Context window' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByRole('tab', { name: 'Turns' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'Calls' })).toBeInTheDocument();
    expect(screen.queryByTestId('context-usage-panel')).not.toBeInTheDocument();
    expect(screen.queryByTestId('x-science-context-usage-trigger')).not.toBeInTheDocument();
    expect(screen.getAllByTestId('context-inspector-turn-point')).toHaveLength(2);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Failed');
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Usage unavailable');
    expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).endsWith('/context-usage?inspect=1'))).toBe(true);
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Context window' })).not.toBeInTheDocument());
    expect(opener).toHaveFocus();
  });

  it('shows semantic call detail, one gray lane per real turn, and unknown cache presence without fake zeroes', async () => {
    const data = inspection();
    data.calls.push({ ...data.calls[0], id: 'call-two' });
    data.turns[0].callIds.push('call-two');
    data.coverage.retainedCallCount = 2;
    vi.mocked(fetch).mockImplementation(
      async () => new Response(JSON.stringify({ status: 'unavailable', inspection: data }))
    );
    await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    expect(screen.getAllByTestId('context-inspector-call-point')).toHaveLength(2);
    expect(screen.getAllByTestId('context-inspector-call-band')).toHaveLength(1);
    expect(screen.getByTestId('context-inspector-call-band')).toHaveTextContent('Input revision 1');
    expect(screen.getByTestId('context-inspector-token-input')).toHaveTextContent('11');
    expect(screen.getByTestId('context-inspector-token-cache_read')).toHaveTextContent('Not reported');
    expect(screen.getByTestId('context-inspector-coverage')).toHaveTextContent('3 historical context samples');
    expect(screen.getByTestId('context-inspector-coverage')).toHaveTextContent(
      'historical model-call count is unknown'
    );
    expect(screen.getByLabelText('Call summary')).toHaveTextContent('2 of 2 calls reported a valid total');
  });

  it.each(['en-US', 'zh-CN'] as const)('shows only drawn call-segment legends in %s', async (locale) => {
    const normalized = parseContextUsage(
      { status: 'unavailable', inspection: inspection() },
      conversationId
    ).inspection!;
    const call = normalized.calls[0];
    const renderCurrent = (callUsage: ContextCallUsage | undefined) => (
      <ContextWindowInspector
        open
        ownerScope='legend-fixture'
        state={{
          conversationId,
          status: 'unavailable',
          inspection: { ...normalized, calls: [{ ...call, callUsage }] },
        }}
        returnFocus={null}
        retry={() => undefined}
        onClose={() => undefined}
      />
    );
    const view = await renderWithI18n(renderCurrent(call.callUsage), locale);
    const expectFields = (fields: Array<keyof typeof CALL_COLORS>) => {
      const items = within(screen.getByTestId('context-inspector-call-legend')).getAllByRole('listitem');
      expect(items.map((item) => item.textContent)).toEqual(
        fields.map((field) => view.i18n.t('conversation.contextUsage.call_' + field))
      );
      items.forEach((item, index) =>
        expect(item.querySelector('i')).toHaveStyle({ background: CALL_COLORS[fields[index]] })
      );
    };
    expect(screen.queryByTestId('context-inspector-call-legend')).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: view.i18n.t('conversation.contextInspector.calls') }));
    expectFields(['input', 'output']);

    view.rerender(
      renderCurrent({
        input_tokens: 11,
        output_tokens: 4,
        total_tokens: 15,
        cache_read_tokens: 3,
        cache_write_tokens: 2,
        reported: { input: true, output: true, total: true, cache_read: true, cache_write: true },
      })
    );
    expectFields(['input', 'cache_read', 'cache_write', 'output']);

    view.rerender(
      renderCurrent({
        input_tokens: 11,
        output_tokens: 4,
        total_tokens: 15,
        cache_read_tokens: 3,
        cache_write_tokens: 0,
        reported: { input: true, output: true, total: true, cache_read: true, cache_write: true },
        invalid_fields: { input: false, output: false, total: false, cache_read: true, cache_write: false },
      })
    );
    expectFields(['input', 'output']);
    expect(screen.getByTestId('context-inspector-token-cache_read')).toHaveTextContent(
      view.i18n.t('conversation.contextUsage.callInvalid')
    );
    expect(screen.getByTestId('context-inspector-token-cache_write')).toHaveTextContent(/^0$/);

    view.rerender(
      renderCurrent({
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 9,
        reported: { input: false, output: false, total: true, cache_read: false, cache_write: false },
      })
    );
    expectFields(['total']);

    view.rerender(renderCurrent(undefined));
    expect(screen.queryByTestId('context-inspector-call-legend')).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: view.i18n.t('conversation.contextInspector.turns') }));
    expect(screen.queryByTestId('context-inspector-call-legend')).toBeNull();
  });

  it('keeps an explicit turn pin across appended records and resets it on a true branch incarnation change', async () => {
    const state = (data: ReturnType<typeof inspection>) => ({
      ...parseContextUsage({ status: 'unavailable', inspection: data }, conversationId),
      conversationId,
    });
    const view = await renderWithI18n(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={state(inspection())}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />,
      'en-US'
    );
    const first = screen.getAllByTestId('context-inspector-turn-point')[0];
    fireEvent.click(first);
    fireEvent.blur(first);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Completed');
    const appended = inspection();
    appended.scope.throughPublicationSequence += 1;
    appended.turns.push({
      ...appended.turns[1],
      id: 'turn-three',
      inputMessageId: 'input-three',
      status: 'cancelled',
      terminalEventId: 13,
      callIds: [],
      outcomes: [
        {
          id: 'outcome-three',
          status: 'cancelled',
          terminalEventId: 13,
          completedAt: '2026-10-22T00:04:00Z',
          runnerAttempt: 3,
          callIds: [],
        },
      ],
    });
    view.rerender(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={state(appended)}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Completed');
    fireEvent.pointerEnter(screen.getAllByTestId('context-inspector-turn-point')[2]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Cancelled');
    fireEvent.pointerLeave(screen.getAllByTestId('context-inspector-turn-point')[2]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Completed');
    appended.scope.branchGeneration += 1;
    appended.scope.branchId = 'branch-two';
    view.rerender(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={state(appended)}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Cancelled');
  });

  it('supports keyboard tab and chart navigation, traps dialog focus, and preserves bilingual interaction', async () => {
    const view = await renderWithI18n(<MenuHarness />, 'en-US');
    const dialog = await openMenu();
    const turns = screen.getByRole('tab', { name: 'Turns' });
    fireEvent.keyDown(turns, { key: 'ArrowRight' });
    expect(screen.getByRole('tab', { name: 'Calls' })).toHaveFocus();
    expect(screen.getByRole('tab', { name: 'Calls' })).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Calls' }), { key: 'Home' });
    const points = screen.getAllByTestId('context-inspector-turn-point');
    fireEvent.keyDown(points[1], { key: 'Home' });
    expect(points[0]).toHaveFocus();
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Completed');
    fireEvent.keyDown(points[0], { key: 'End' });
    expect(points[1]).toHaveFocus();
    fireEvent.keyDown(points[1], { key: 'Tab' });
    expect(within(dialog).getByRole('button', { name: 'Close context window' })).toHaveFocus();
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('dialog', { name: '上下文窗口' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '轮次' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('用量不可用');
  });

  it('closes synchronously on conversation replacement and never resurrects the old open request', async () => {
    const pending = deferred<Response>();
    vi.mocked(fetch).mockImplementation((_url, options) =>
      options && String(_url).includes('?inspect=1')
        ? pending.promise
        : Promise.resolve(new Response(JSON.stringify({ status: 'unavailable' })))
    );
    const view = await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    expect(screen.getByText('Loading context records…')).toBeInTheDocument();
    const oldSignal = vi.mocked(fetch).mock.calls.find(([url]) => String(url).includes('?inspect=1'))?.[1]?.signal;
    view.rerender(<MenuHarness id='replacement' />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => {
      pending.resolve(new Response(JSON.stringify({ status: 'unavailable', inspection: inspection() })));
    });
    view.rerender(<MenuHarness />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
  });

  it('fences old-account replies and does not restore focus to a prior-account opener', async () => {
    setRendererAccountOwner('first-owner');
    const pending = deferred<Response>();
    vi.mocked(fetch).mockImplementation((url) =>
      String(url).includes('?inspect=1')
        ? pending.promise
        : Promise.resolve(new Response(JSON.stringify({ status: 'unavailable' })))
    );
    await renderWithI18n(
      <>
        <MenuHarness />
        <button>New account action</button>
      </>,
      'en-US'
    );
    await openMenu();
    const outside = screen.getByRole('button', { name: 'New account action' });
    act(() => {
      outside.focus();
      setRendererAccountOwner('second-owner');
    });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(outside).toHaveFocus();
    await act(async () => {
      pending.resolve(new Response(JSON.stringify({ status: 'unavailable', inspection: inspection() })));
    });
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
  });

  it('clears stale branch evidence on 409 and allows a fresh explicit read without manufacturing turns', async () => {
    let attempt = 0;
    vi.mocked(fetch).mockImplementation(async (url) => {
      if (!String(url).includes('?inspect=1')) return new Response(JSON.stringify({ status: 'unavailable' }));
      return ++attempt === 1
        ? new Response('{}', { status: 409 })
        : new Response(JSON.stringify({ status: 'unavailable', inspection: inspection() }));
    });
    await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    expect(await screen.findByText('Could not load context records')).toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
    expect(attempt).toBe(1);
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0]);
    await screen.findAllByTestId('context-inspector-turn-point');
    expect(attempt).toBe(2);
  });

  it('has explicit unavailable and empty states rather than converting old flat samples into turns', async () => {
    const state = { conversationId, status: 'unavailable' as const };
    const view = await renderWithI18n(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={state}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />,
      'en-US'
    );
    expect(screen.getByText('Context inspection is unavailable')).toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
    const empty = inspection();
    empty.calls = [];
    empty.turns = [];
    empty.coverage.retainedCallCount = 0;
    view.rerender(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={{
          ...state,
          inspection: parseContextUsage({ status: 'unavailable', inspection: empty }, conversationId).inspection,
        }}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.getByText('No terminal runs have been retained for this branch.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    expect(screen.getByText('No verified model calls have been retained for this branch.')).toBeInTheDocument();
    expect(screen.getByLabelText('Call summary')).toHaveTextContent('—');
  });

  it('validates exact identities and call associations, leaving unknown historical counts absent', () => {
    const data = inspection();
    const parsed = parseContextUsage({ status: 'unavailable', inspection: data }, conversationId);
    expect(parsed.inspection?.coverage.observedCallCount).toBeUndefined();
    expect(() => parseContextUsage({ status: 'unavailable', inspection: data }, 'other')).toThrow();
    for (const broken of [
      { ...data, calls: [{ ...data.calls[0], inputRevision: 3 }] },
      { ...data, calls: [{ ...data.calls[0], inputMessageId: 'wrong-input' }] },
      { ...data, turns: data.turns.map((turn) => ({ ...turn, callIds: [] })) },
      { ...data, turns: data.turns.map((turn) => ({ ...turn, outcomes: undefined })) },
      { ...data, coverage: { ...data.coverage, observedCallCount: null } },
      { ...data, scope: { ...data.scope, branchGeneration: Number.MAX_SAFE_INTEGER + 1 } },
    ])
      expect(() => parseContextUsage({ status: 'unavailable', inspection: broken }, conversationId)).toThrow();
    expect(parseContextUsage({ status: 'unavailable', inspection: null }, conversationId).inspection).toBeNull();
  });

  it('separates reported zero, missing provider fields and additive cache without double counting', () => {
    const parsed = parseContextUsage({ status: 'unavailable', inspection: inspection() }, conversationId).inspection!;
    const call = parsed.calls[0];
    expect(reportedCallToken(call, 'cache_read')).toBeUndefined();
    call.callUsage!.reported!.cache_read = true;
    expect(reportedCallToken(call, 'cache_read')).toBe(0);
    call.callUsage!.cache_read_tokens = 3;
    expect(callSegments(call).reduce((sum, row) => sum + row.tokens, 0)).toBe(15);
    call.callUsage!.cache_tokens_additive = true;
    expect(callSegments(call).reduce((sum, row) => sum + row.tokens, 0)).toBe(18);
    call.callUsage!.reported!.total = false;
    expect(summarizeInspectionCalls(parsed).knownTotal).toBeUndefined();
    delete call.callUsage;
    expect(callSegments(call)).toEqual([]);
  });

  it('keeps ordinary ring requests projection-free and uses the same typed route for inspection', async () => {
    const signal = new AbortController().signal;
    await fetchContextUsage(conversationId, signal);
    await fetchContextUsage(conversationId, signal, true);
    expect(String(vi.mocked(fetch).mock.calls[0][0])).toMatch(/\/context-usage$/);
    expect(String(vi.mocked(fetch).mock.calls[1][0])).toMatch(/\/context-usage\?inspect=1$/);
  });

  it('does not relabel a different branch session-latest snapshot as current composition', async () => {
    const data = inspection();
    const snapshot: ContextUsageSnapshot = {
      sessionId: conversationId,
      requestId: 'other-branch-call',
      model: 'other-branch-model',
      observedAt: '2026-10-22T00:05:00Z',
      state: 'complete',
      source: 'provider',
      usedTokens: 88,
      limitTokens: 100,
      limitSource: 'configured',
      outputTokens: 4,
      hasMedia: false,
      inputEstimates: [
        { key: 'systemPrompt', tokens: 10 },
        { key: 'tools', tokens: 10 },
        { key: 'messages', tokens: 10 },
        { key: 'mcp', tokens: 0 },
        { key: 'skills', tokens: 0 },
      ],
    };
    const state = {
      ...parseContextUsage({ status: 'available', snapshot, inspection: data }, conversationId),
      conversationId,
    };
    const view = await renderWithI18n(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={state}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />,
      'en-US'
    );
    expect(screen.queryByLabelText('Current composition')).not.toBeInTheDocument();
    expect(screen.queryByText('other-branch-model')).not.toBeInTheDocument();
    const linked = {
      ...snapshot,
      requestId: 'call-one',
      model: 'fixture-model',
      usedTokens: 40,
      observedAt: data.calls[0].observedAt,
    };
    const branchData = {
      ...data,
      turns: data.turns.map((turn, index) => (index === 0 ? { ...turn, sample: linked } : turn)),
    };
    const branchState = {
      ...parseContextUsage({ status: 'available', snapshot, inspection: branchData }, conversationId),
      conversationId,
    };
    view.rerender(
      <ContextWindowInspector
        open
        ownerScope='owner'
        state={branchState}
        returnFocus={null}
        retry={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.getByLabelText('Latest retained branch sample')).toHaveTextContent('fixture-model');
    expect(screen.queryByLabelText('Current composition')).not.toBeInTheDocument();
    const unrelated = {
      ...data,
      turns: data.turns.map((turn, index) => (index === 0 ? { ...turn, sample: snapshot } : turn)),
    };
    expect(() => parseContextUsage({ status: 'unavailable', inspection: unrelated }, conversationId)).toThrow(
      'Invalid context inspection sample association'
    );
  });

  it('uses durable input revisions and attempts instead of retained turn positions', async () => {
    const data = inspection();
    data.turns[0].inputRevision = 25;
    data.turns[0].runnerAttempts = [77];
    data.turns[0].outcomes[0].runnerAttempt = 77;
    data.calls[0].inputRevision = 25;
    data.calls[0].runnerAttempt = 77;
    data.turns[1].inputRevision = 33;
    const parsed = parseContextUsage({ status: 'unavailable', inspection: data }, conversationId).inspection!;
    expect(inspectionTurnPoints(parsed)[0]).toMatchObject({ inputRevision: 25, runnerAttempt: 77 });
    expect(inspectionTurnPoints({ ...parsed, turns: parsed.turns.slice(0, 1) })[0]).toMatchObject({
      inputRevision: 25,
      runnerAttempt: 77,
    });
    vi.mocked(fetch).mockImplementation(
      async () => new Response(JSON.stringify({ status: 'unavailable', inspection: data }))
    );
    await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    fireEvent.click(screen.getAllByTestId('context-inspector-turn-point')[0]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Input revision 25 · Attempt 77');
    expect(screen.getByTestId('context-inspector-turn-details')).not.toHaveTextContent('Turn 1');
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    expect(screen.getByTestId('context-inspector-call-band')).toHaveTextContent('Input revision 25');
    expect(screen.getByTestId('context-inspector-call-details')).toHaveTextContent('Input revision 25 · Attempt 77');
  });

  it.each([
    ['cancelled', 'Call interrupted'],
    ['deadline', 'Call timed out'],
    [undefined, 'Failed or interrupted'],
  ] as const)('uses only recorded call failure evidence for %s', async (kind, label) => {
    const data = inspection();
    data.turns[0].status = 'cancelled';
    data.turns[0].outcomes[0].status = 'cancelled';
    const payload = {
      ...data,
      calls: data.calls.map((call) => ({
        ...call,
        state: 'failed',
        callUsage: undefined,
        ...(kind ? { failureKind: kind } : {}),
      })),
    };
    vi.mocked(fetch).mockImplementation(
      async () => new Response(JSON.stringify({ status: 'unavailable', inspection: payload }))
    );
    await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    const details = screen.getByTestId('context-inspector-call-details');
    expect(details).toHaveTextContent(label);
    if (!kind) expect(details).toHaveTextContent('The failure cause was not recorded.');
    else expect(details).not.toHaveTextContent('The failure cause was not recorded.');
  });

  it('rejects malformed failure evidence and never gives missing identities a chart index', () => {
    const data = inspection();
    const parsed = parseContextUsage({ status: 'unavailable', inspection: data }, conversationId).inspection!;
    parsed.turns[0].inputRevision = 0;
    expect(inspectionTurnPoints(parsed)[0].inputRevision).toBeUndefined();
    for (const kind of [null, '', 'other', true, { kind: 'cancelled' }]) {
      const payload = {
        ...data,
        calls: data.calls.map((call) => ({ ...call, state: 'failed', callUsage: undefined, failureKind: kind })),
      };
      expect(() => parseContextUsage({ status: 'unavailable', inspection: payload }, conversationId)).toThrow(
        'Invalid context inspection call'
      );
    }
  });

  it('keeps reclaimed usage unknown and distinguishes consumed-but-unassociated inputs from pending or completed runs', async () => {
    const data = inspection();
    data.turns[0].outcomes.unshift({
      id: 'reclaimed-run',
      status: 'reclaimed',
      terminalEventId: 3,
      completedAt: '2026-10-22T00:00:30Z',
      runnerAttempt: 1,
      callIds: [],
    });
    data.turns.push({
      ...data.turns[1],
      id: 'unassociated-input',
      inputMessageId: 'input-unknown',
      inputRevision: 3,
      status: 'unknown',
      runnerAttempts: [],
      callIds: [],
      outcomes: [],
    });
    vi.mocked(fetch).mockImplementation(
      async () => new Response(JSON.stringify({ status: 'unavailable', inspection: data }))
    );
    await renderWithI18n(<MenuHarness />, 'en-US');
    await openMenu();
    expect(screen.getAllByTestId('context-inspector-turn-point')).toHaveLength(3);
    expect(screen.queryByTestId('context-inspector-in-progress')).not.toBeInTheDocument();
    expect(screen.getByTestId('context-inspector-unresolved-turns')).toHaveTextContent('Input revision 3');
    expect(screen.getByTestId('context-inspector-unresolved-turns')).toHaveTextContent('Status unavailable');
    fireEvent.click(screen.getAllByTestId('context-inspector-turn-point')[0]);
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Reclaimed');
    expect(screen.getByTestId('context-inspector-turn-details')).toHaveTextContent('Usage unavailable');
  });
});
