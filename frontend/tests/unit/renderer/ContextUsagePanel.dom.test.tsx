/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@arco-design/web-react', () => ({
  Dropdown: ({
    children,
    droplist,
    popupVisible,
    onVisibleChange,
  }: {
    children: React.ReactNode;
    droplist: React.ReactNode;
    popupVisible?: boolean;
    onVisibleChange?: (visible: boolean) => void;
  }) => (
    <div>
      <div onClick={() => onVisibleChange?.(!popupVisible)}>{children}</div>
      {popupVisible ? droplist : null}
    </div>
  ),
  Spin: () => <span aria-hidden='true' />,
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock('@icon-park/react', () => ({
  Close: () => <span aria-hidden='true'>×</span>,
}));

import ContextUsagePanel from '@/renderer/components/synonBiomed/runtime/ContextUsagePanel';

const usage = (usedTokens = 20, sessionId = 'conversation-1') => ({
  status: 'available',
  snapshot: {
    sessionId,
    requestId: 'request-one',
    model: 'model',
    observedAt: '2026-01-01T00:00:00Z',
    state: 'complete',
    source: 'provider',
    usedTokens,
    limitTokens: 100,
    limitSource: 'configured',
    outputTokens: 3,
    hasMedia: false,
    inputEstimates: [
      { key: 'systemPrompt', tokens: 4 },
      { key: 'tools', tokens: 2 },
      { key: 'messages', tokens: 8 },
      { key: 'mcp', tokens: 1 },
      { key: 'skills', tokens: 1 },
    ],
  },
});

describe('ContextUsagePanel', () => {
  it('exposes unscaled estimates, receipt delta and selectable near-full history', async () => {
    const current = usage(20);
    const peak = {
      ...current.snapshot,
      requestId: 'peak',
      usedTokens: 95,
      model: 'earlier-model',
    };
    vi.mocked(fetch).mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            ...current,
            history: {
              sessionId: current.snapshot.sessionId,
              totalObserved: 2,
              coverage: 'recorded',
              samples: [peak, current.snapshot],
              peak,
            },
          })
        )
      )
    );
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await screen.findByTestId('context-usage-percent');
    const disclosure = screen.getByTestId('context-window-history-toggle');
    expect(disclosure).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByTestId('context-window-history-chart')).not.toBeInTheDocument();
    const summary = screen.getByRole('group', { name: 'Context usage' });
    const description = document.getElementById(summary.getAttribute('aria-describedby') ?? '');
    expect(description).toHaveTextContent('Local composition estimate: ≈ 19');
    expect(description).toHaveTextContent('Provider minus local estimate: +1');
    fireEvent.click(disclosure);
    expect(await screen.findByTestId('context-window-peak')).toHaveTextContent('95 / 100 · 95.0%');
    expect(screen.getByTestId('synon-biomed-context-usage-trigger')).toHaveTextContent(/^$/);
    expect(screen.queryByTestId('context-usage-trigger-value')).not.toBeInTheDocument();
    expect(screen.getByTestId('context-usage-legend')).toHaveTextContent('≈ 4');
    fireEvent.click(screen.getByRole('button', { name: /^1 · earlier-model/ }));
    expect(screen.getByTestId('context-window-selected')).toHaveTextContent('earlier-model');
    expect(screen.getByTestId('context-window-selected')).toHaveTextContent('95.0%');
    expect(screen.getByTestId('context-window-history-chart')).toHaveAttribute(
      'title',
      expect.stringContaining('not cumulative billing')
    );
    fireEvent.click(disclosure);
    expect(disclosure).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByTestId('context-window-history-chart')).not.toBeInTheDocument();
  });

  it('keeps live local and last-confirmed provider windows separate', async () => {
    const current = usage(25);
    const confirmed = {
      ...current.snapshot,
      requestId: 'confirmed',
      usedTokens: 20,
    };
    const pending = {
      ...current.snapshot,
      source: 'estimated',
      state: 'request',
      outputTokens: 0,
    };
    vi.mocked(fetch).mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            ...current,
            snapshot: pending,
            history: {
              sessionId: pending.sessionId,
              totalObserved: 2,
              coverage: 'recorded',
              samples: [confirmed, pending],
              peak: confirmed,
            },
          })
        )
      )
    );
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' active />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await screen.findByTestId('context-usage-percent');
    const summary = screen.getByRole('group', { name: 'Context usage' });
    expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent(
      'Last confirmed provider window: 20'
    );
    expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('25.0%');
    expect(screen.getByTestId('synon-biomed-context-usage-trigger')).toHaveAttribute(
      'title',
      expect.stringContaining('≈ 25.0%')
    );
    expect(screen.queryByTestId('context-usage-trigger-value')).not.toBeInTheDocument();
  });

  beforeEach(() => {
    vi.restoreAllMocks();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(usage()))))
    );
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('keeps the default card focused and omits the duplicate single-record chart', async () => {
    const current = usage(52_700);
    current.snapshot.limitTokens = 0;
    current.snapshot.limitSource = 'unknown';
    vi.mocked(fetch).mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            ...current,
            history: {
              sessionId: current.snapshot.sessionId,
              totalObserved: 1,
              coverage: 'latest_only',
              samples: [current.snapshot],
              peak: current.snapshot,
            },
          })
        )
      )
    );
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('5.3%');
    expect(screen.queryByTestId('context-usage-diagnostics')).not.toBeInTheDocument();
    expect(screen.queryByTestId('context-usage-policy')).not.toBeInTheDocument();
    expect(screen.queryByTestId('context-window-peak')).not.toBeInTheDocument();
    expect(screen.queryByTestId('context-window-history-toggle')).not.toBeInTheDocument();
    expect(screen.getByTestId('context-window-history')).toHaveTextContent('Only the latest record is available');
    const summary = screen.getByRole('group');
    expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent(
      'Provider minus local estimate'
    );
  });

  it('loads the real context contract without ACP props or message sampling', async () => {
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    const trigger = screen.getByTestId('synon-biomed-context-usage-trigger');
    expect(trigger.tagName).toBe('BUTTON');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).toHaveAttribute('aria-haspopup', 'dialog');
    fireEvent.click(trigger);
    expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('20.0%');
    const summary = screen.getByRole('group');
    expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent(
      'provider-reported usage'
    );
    expect(screen.getByTestId('context-usage-legend')).toHaveTextContent('Tools & subagents');
    expect(screen.getByTestId('context-usage-legend')).toHaveTextContent('Connectors & MCP');
    expect(screen.getByTestId('context-usage-legend')).toHaveTextContent('Skills');
    expect(screen.getByTestId('context-usage-legend').querySelectorAll('div')).toHaveLength(5);
    expect(vi.mocked(fetch).mock.calls.every(([input]) => String(input).endsWith('/context-usage'))).toBe(true);
    await act(async () => {
      fireEvent.click(screen.getByTestId('context-usage-close'));
    });
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).toHaveFocus();
  });

  it.each(['unknown', 'runner_default'])(
    'uses the explicit 1M display default for %s without relabelling it as verified',
    async (source) => {
      const defaultBudget = usage(52_700);
      defaultBudget.snapshot.limitTokens = source === 'unknown' ? 0 : 123_456;
      defaultBudget.snapshot.limitSource = source;
      vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(defaultBudget))));
      await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
      fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
      expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('5.3%');
      const trigger = screen.getByTestId('synon-biomed-context-usage-trigger');
      expect(trigger).toHaveTextContent(/^$/);
      expect(screen.queryByTestId('context-usage-trigger-value')).not.toBeInTheDocument();
      const ring = trigger.querySelectorAll('circle')[1];
      expect(ring).toBeDefined();
      expect(
        Number(ring.getAttribute('stroke-dashoffset')) / Number(ring.getAttribute('stroke-dasharray'))
      ).toBeCloseTo(0.9473, 4);
      expect(screen.getByTestId('context-usage-panel')).toHaveTextContent('52.7K / 1.0M');
      const summary = screen.getByRole('group');
      expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent(
        'Default display budget: 1.0M tokens; not a verified model limit'
      );
    }
  );

  it('shows the declared current-model capacity and correct ring fraction', async () => {
    const profile = usage(64000);
    profile.snapshot.limitTokens = 128000;
    profile.snapshot.limitSource = 'model_profile';
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(profile))));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    const trigger = screen.getByTestId('synon-biomed-context-usage-trigger');
    fireEvent.click(trigger);
    expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('50.0%');
    const ring = trigger.querySelectorAll('circle')[1];
    expect(Number(ring.getAttribute('stroke-dashoffset')) / Number(ring.getAttribute('stroke-dasharray'))).toBeCloseTo(
      0.5
    );
    expect(screen.getByTestId('context-usage-observation')).toHaveTextContent('Model profile window');
    expect(document.getElementById(screen.getByRole('group').getAttribute('aria-describedby') ?? '')).toHaveTextContent(
      'not provider-verified'
    );
  });

  it('shows a timestamp cleanly when the provider model name is absent', async () => {
    const unnamedModel = usage();
    unnamedModel.snapshot.model = '';
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(unnamedModel))));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    const summary = await screen.findByRole('group');
    const description = document.getElementById(summary.getAttribute('aria-describedby') ?? '');
    expect(description).toHaveTextContent(/1\/1\/2026/);
    expect(description?.textContent?.includes(' ·  · ')).toBe(false);
  });

  it('renders a valid zero rather than an endless spinner', async () => {
    const zero = usage(0);
    zero.snapshot.source = 'estimated';
    zero.snapshot.outputTokens = 0;
    zero.snapshot.inputEstimates.forEach((row) => {
      row.tokens = 0;
    });
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(zero))));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'zh-CN');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('0.0%');
    expect(screen.queryByTestId('context-usage-loading')).not.toBeInTheDocument();
    expect(screen.getByTestId('context-usage-legend')).toHaveTextContent('≈ 0');
    expect(screen.getByTestId('context-usage-legend')).not.toHaveTextContent('响应 token');
  });

  it('does not report zero response tokens before a response exists', async () => {
    const pending = usage();
    pending.snapshot.state = 'request';
    pending.snapshot.source = 'estimated';
    pending.snapshot.outputTokens = 0;
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(pending))));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    const summary = await screen.findByRole('group');
    expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent(
      'response usage is not yet available'
    );
    expect(screen.getByTestId('context-usage-legend')).not.toHaveTextContent('Response tokens');
  });

  it('marks a failed request and keeps unknown response usage hidden', async () => {
    const failed = usage();
    failed.snapshot.state = 'failed';
    failed.snapshot.source = 'estimated';
    failed.snapshot.outputTokens = 0;
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(failed))));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    const summary = await screen.findByRole('group');
    expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent(
      'last request failed'
    );
    expect(screen.getByTestId('context-usage-legend')).not.toHaveTextContent('Response tokens');
  });

  it('shows missing records explicitly and allows retry', async () => {
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'unavailable' }))));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    expect(await screen.findByText(/No saved context record yet/)).toBeInTheDocument();
    expect(screen.queryByTestId('context-usage-loading')).not.toBeInTheDocument();
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(usage(30)))));
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByTestId('context-usage-percent')).toHaveTextContent('30.0%');
  });

  it('shows failures without silently substituting old usage', async () => {
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response('{}', { status: 503 })));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    expect(await screen.findByText(/Could not load context usage/)).toBeInTheDocument();
    expect(screen.queryByTestId('context-usage-percent')).not.toBeInTheDocument();
  });

  it('bounds a stalled request and exposes retry instead of spinning forever', async () => {
    // This transport never settles, even after abort. The UI deadline must.
    vi.mocked(fetch).mockImplementation(() => new Promise(() => {}));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    vi.useFakeTimers();
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    expect(screen.getByTestId('context-usage-loading')).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });
    expect(screen.getByText(/Could not load context usage/)).toBeInTheDocument();
    expect(screen.queryByTestId('context-usage-loading')).not.toBeInTheDocument();
    const attempts = vi.mocked(fetch).mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(vi.mocked(fetch).mock.calls.length).toBe(attempts + 3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(vi.mocked(fetch).mock.calls.length).toBe(attempts + 3);
    expect(screen.queryByTestId('context-usage-loading')).not.toBeInTheDocument();
  });

  it('polls live usage without overlapping requests and cancels on unmount', async () => {
    const rendered = await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' active />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await screen.findByTestId('context-usage-percent');
    vi.useFakeTimers();
    // Reopening creates a fresh polling timer under the controlled clock.
    fireEvent.click(screen.getByTestId('context-usage-close'));
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    const before = vi.mocked(fetch).mock.calls.length;
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(usage(40)))));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(vi.mocked(fetch).mock.calls.length).toBe(before + 1);
    expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('40.0%');
    rendered.unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(vi.mocked(fetch).mock.calls.length).toBe(before + 1);
  });

  it('does not show a late result from a different conversation', async () => {
    const pending: Array<(response: Response) => void> = [];
    vi.mocked(fetch).mockImplementation(() => new Promise((resolve) => pending.push(resolve)));
    const rendered = await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    rendered.rerender(<ContextUsagePanel conversationId='conversation-2' />);
    await act(async () => {
      for (const resolve of pending.slice(0, -1)) resolve(new Response(JSON.stringify(usage(99))));
      pending.at(-1)?.(new Response(JSON.stringify(usage(5, 'conversation-2'))));
    });
    await waitFor(() => expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('5.0%'));
    expect(screen.getByTestId('context-usage-percent')).not.toHaveTextContent('99.0%');
  });

  it('updates the ring during a stream, retains an explicit stale value, and reconciles the provider final', async () => {
    let record: unknown = {
      ...usage(),
      autoCompaction: {
        enabled: true,
        windowTokens: 100,
        thresholdTokens: 80,
        percent: 80,
        source: 'window_percent',
      },
      snapshot: {
        ...usage().snapshot,
        state: 'request',
        source: 'estimated',
        outputTokens: 0,
        progress: {
          phase: 'generating',
          observedAt: '2026-01-01T00:00:01Z',
          usedTokens: 28,
          outputTokens: 8,
        },
      },
    };
    let failed = false;
    vi.mocked(fetch).mockImplementation(() =>
      Promise.resolve(new Response(JSON.stringify(record), { status: failed ? 503 : 200 }))
    );
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' active />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await waitFor(() => expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('28.0%'));
    expect(screen.getByTestId('context-usage-phase')).toHaveTextContent('Generating');
    const summary = screen.getByRole('group', { name: 'Context usage' });
    expect(document.getElementById(summary.getAttribute('aria-describedby') ?? '')).toHaveTextContent('80%');
    expect(screen.getByTestId('synon-biomed-context-usage-trigger')).toHaveAttribute(
      'title',
      expect.stringContaining('≈ 28.0%')
    );
    vi.useFakeTimers();
    fireEvent.click(screen.getByTestId('context-usage-close'));
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    failed = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(screen.getByText(/Retrying update/)).toBeInTheDocument();
    expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('28.0%');
    failed = false;
    record = usage(31);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('31.0%');
    expect(screen.getByTestId('context-usage-phase')).toHaveTextContent('Latest request');
    expect(screen.queryByText(/Retrying update/)).not.toBeInTheDocument();
  });

  it('stops bounded network retries and resumes on a real online event, but does not retry authorization errors', async () => {
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response('{}', { status: 401 })));
    await renderWithI18n(<ContextUsagePanel conversationId='conversation-1' active />, 'en-US');
    fireEvent.click(screen.getByTestId('synon-biomed-context-usage-trigger'));
    await screen.findByText(/Could not load context usage/);
    vi.useFakeTimers();
    const before = vi.mocked(fetch).mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(vi.mocked(fetch).mock.calls.length).toBe(before);
    vi.mocked(fetch).mockImplementation(() => Promise.resolve(new Response(JSON.stringify(usage(40)))));
    await act(async () => {
      window.dispatchEvent(new Event('online'));
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('40.0%');
  });
});
