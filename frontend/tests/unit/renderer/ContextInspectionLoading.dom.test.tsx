import React, { useLayoutEffect, useRef, useState } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ContextWindowInspector from '@/renderer/components/xScience/runtime/ContextWindowInspector';
import { useContextUsage } from '@/renderer/hooks/xScience/useContextUsage';
import { type ContextUsageSnapshot } from '@/renderer/services/contextUsage';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';

const conversationId = 'first-inspection-fixture';
const snapshot = (id = conversationId): ContextUsageSnapshot => ({
  sessionId: id,
  requestId: 'call-one',
  model: 'fixture-model',
  observedAt: '2026-10-22T00:01:00Z',
  state: 'complete',
  source: 'provider',
  usedTokens: 40,
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
});
const inspection = (id = conversationId, model = 'fixture-model') => ({
  version: 1,
  scope: {
    conversationId: id,
    frameIncarnationId: 'incarnation-one',
    streamUid: 'frame:' + id,
    streamEpoch: 1,
    branchId: 'branch-one',
    branchGeneration: 1,
    throughPublicationSequence: 8,
  },
  coverage: {
    association: 'complete',
    retainedCallCount: 1,
    observedSampleCount: 1,
    unassociatedCallCount: 0,
    truncated: false,
  },
  calls: [
    {
      id: 'call-one',
      inputMessageId: 'input-one',
      inputEventId: 2,
      inputRevision: 1,
      runnerAttempt: 1,
      model,
      state: 'complete',
      observedAt: '2026-10-22T00:01:00Z',
      usedTokens: 40,
      limitTokens: 100,
      limitSource: 'configured',
      outputTokens: 4,
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
  ],
});

type Commit = { loading: boolean; unavailable: boolean; failed: boolean };
function Harness({ id = conversationId, onCommit }: { id?: string; onCommit?: (value: Commit) => void }) {
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  const { state, scopeKey, retry } = useContextUsage(id, open, false, open);
  useLayoutEffect(() => {
    if (!open) return;
    onCommit?.({
      loading: Boolean(screen.queryByText('Loading context records…')),
      unavailable: Boolean(screen.queryByRole('heading', { name: 'Context inspection is unavailable' })),
      failed: Boolean(screen.queryByRole('heading', { name: 'Could not load context records' })),
    });
  });
  return (
    <>
      <button ref={opener} onClick={() => setOpen(true)}>
        Inspect context
      </button>
      <button onClick={retry}>Refresh records</button>
      <output aria-label='Summary state'>{state.status}</output>
      <ContextWindowInspector
        open={open}
        ownerScope={scopeKey}
        state={state}
        returnFocus={opener.current}
        retry={retry}
        onClose={() => setOpen(false)}
      />
    </>
  );
}

type Pending = { url: string; signal?: AbortSignal | null; resolve: (response: Response) => void };
let requests: Pending[];
beforeEach(() => {
  resetRendererAccountScopeForTest();
  requests = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (url: RequestInfo | URL, options?: RequestInit) =>
        new Promise<Response>((resolve) => {
          requests.push({ url: String(url), signal: options?.signal, resolve });
        })
    )
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetRendererAccountScopeForTest();
});
async function reply(index: number, payload: unknown) {
  await act(async () => requests[index].resolve(new Response(JSON.stringify(payload))));
}
async function warmSummary() {
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0].url).not.toContain('?inspect=1');
  await reply(0, { status: 'available', snapshot: snapshot() });
  expect(screen.getByLabelText('Summary state')).toHaveTextContent('available');
  fireEvent.click(screen.getByRole('button', { name: 'Inspect context' }));
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[1].url).toContain('?inspect=1');
}

describe('First context inspection request state', () => {
  it('keeps a warm summary while the first inspection is pending without a false unavailable commit', async () => {
    const commits: Commit[] = [];
    await renderWithI18n(<Harness onCommit={(value) => commits.push(value)} />, 'en-US');
    await warmSummary();
    expect(screen.getByText('Loading context records…')).toBeInTheDocument();
    expect(screen.getByLabelText('Summary state')).toHaveTextContent('available');
    expect(commits.length).toBeGreaterThan(0);
    expect(commits.every((value) => value.loading && !value.unavailable && !value.failed)).toBe(true);
    await reply(1, { status: 'available', snapshot: snapshot(), inspection: inspection() });
    expect(screen.getAllByTestId('context-inspector-turn-point')).toHaveLength(1);
    expect(screen.queryByText('Loading context records…')).not.toBeInTheDocument();
    expect(commits.some((value) => value.unavailable || value.failed)).toBe(false);
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    expect(screen.getAllByTestId('context-inspector-call-point')).toHaveLength(1);
    expect(requests).toHaveLength(2);
  });

  it('settles first inspection failures and unavailable replies into truthful retry states', async () => {
    await renderWithI18n(<Harness />, 'en-US');
    await warmSummary();
    // A fulfilled but invalid read is a real, non-retryable parse failure.
    await reply(1, { status: 'available' });
    expect(screen.getByRole('heading', { name: 'Could not load context records' })).toBeInTheDocument();
    expect(screen.getByLabelText('Summary state')).toHaveTextContent('available');
    expect(screen.queryByText('Loading context records…')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Context inspection is unavailable' })).not.toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0]);
    await waitFor(() => expect(requests).toHaveLength(3));
    expect(screen.getByText('Loading context records…')).toBeInTheDocument();
    await reply(2, { status: 'unavailable', inspection: null });
    expect(screen.getByRole('heading', { name: 'Context inspection is unavailable' })).toBeInTheDocument();
    expect(screen.queryByText('Loading context records…')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Could not load context records' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
  });

  it('retains history during refresh and fences superseded conversation account and close replies', async () => {
    setRendererAccountOwner('first-owner');
    const view = await renderWithI18n(<Harness />, 'en-US');
    await warmSummary();
    await reply(1, { status: 'unavailable', inspection: inspection() });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh records' }));
    await waitFor(() => expect(requests).toHaveLength(3));
    expect(screen.getAllByTestId('context-inspector-turn-point')).toHaveLength(1);
    expect(screen.queryByText('Loading context records…')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Context inspection is unavailable' })).not.toBeInTheDocument();
    view.rerender(<Harness id='replacement' />);
    await waitFor(() => expect(requests).toHaveLength(4));
    expect(requests[2].signal?.aborted).toBe(true);
    expect(screen.getByText('Loading context records…')).toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
    act(() => {
      setRendererAccountOwner('second-owner');
    });
    await waitFor(() => expect(requests).toHaveLength(5));
    expect(requests[3].signal?.aborted).toBe(true);
    await reply(2, { status: 'unavailable', inspection: inspection() });
    await reply(3, { status: 'unavailable', inspection: inspection('replacement', 'old-account-model') });
    expect(screen.getByText('Loading context records…')).toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-turn-point')).not.toBeInTheDocument();
    await reply(4, { status: 'unavailable', inspection: inspection('replacement', 'current-owner-model') });
    fireEvent.click(screen.getByRole('tab', { name: 'Calls' }));
    expect(screen.getByTestId('context-inspector-call-details')).toHaveTextContent('current-owner-model');
    expect(screen.queryByText('old-account-model')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh records' }));
    await waitFor(() => expect(requests).toHaveLength(6));
    fireEvent.click(screen.getByRole('button', { name: 'Close context window' }));
    expect(requests[5].signal?.aborted).toBe(true);
    await reply(5, { status: 'unavailable', inspection: inspection('replacement', 'closed-model') });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByTestId('context-inspector-call-point')).not.toBeInTheDocument();
  });
});
