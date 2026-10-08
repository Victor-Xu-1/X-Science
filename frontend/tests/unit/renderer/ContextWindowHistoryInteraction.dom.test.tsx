/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, fireEvent, screen, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ContextWindowHistory from '@/renderer/components/synonBiomed/runtime/ContextWindowHistory';
import type { ContextUsageHistory, ContextUsageSnapshot } from '@/renderer/services/contextUsage';
import { renderWithI18n } from '../i18nTestUtils';

function history(count = 128): ContextUsageHistory {
  const samples: ContextUsageSnapshot[] = Array.from({ length: count }, (_, index) => ({
    sessionId: 'history-interaction-fixture',
    requestId: `request-${index}`,
    model: 'fixture-model',
    observedAt: new Date(Date.UTC(2026, 9, 8, 0, index)).toISOString(),
    state: 'complete',
    source: 'provider',
    usedTokens: index + 1,
    limitTokens: 1000,
    limitSource: 'configured',
    outputTokens: 0,
    hasMedia: false,
    inputEstimates: [],
  }));
  return { sessionId: samples[0].sessionId, totalObserved: count, coverage: 'recorded', samples, peak: samples.at(-1) };
}
beforeEach(() =>
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
);

describe('Retained context-history keyboard navigation', () => {
  it('keeps every dense record but exposes only one chart Tab stop and arrow/Home/End selection', async () => {
    await renderWithI18n(<ContextWindowHistory history={history()} />, 'en-US');
    const disclosure = screen.getByTestId('context-window-history-toggle');
    disclosure.focus();
    fireEvent.click(disclosure);
    const chart = screen.getByTestId('context-window-history-chart');
    const points = within(chart).getAllByRole('button');
    expect(points).toHaveLength(128);
    expect(points.filter((point) => point.tabIndex === 0)).toHaveLength(1);
    expect(disclosure).toHaveFocus();
    act(() => points.at(-1)!.focus());
    fireEvent.keyDown(points.at(-1)!, { key: 'Home' });
    expect(points[0]).toHaveFocus();
    expect(points[0]).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('context-window-selected')).toHaveTextContent('1 / 1.0K · 0.1%');
    fireEvent.keyDown(points[0], { key: 'ArrowRight' });
    expect(points[1]).toHaveFocus();
    expect(points[1]).toHaveAttribute('aria-pressed', 'true');
    fireEvent.keyDown(points[1], { key: 'End' });
    expect(points.at(-1)).toHaveFocus();
    expect(points.at(-1)?.scrollIntoView).toHaveBeenCalledWith({ block: 'nearest', inline: 'nearest' });
  });

  it('keeps an inspected receipt selected when new samples arrive', async () => {
    const initial = history(3);
    const view = await renderWithI18n(<ContextWindowHistory history={initial} />, 'en-US');
    fireEvent.click(screen.getByTestId('context-window-history-toggle'));
    const point = within(screen.getByTestId('context-window-history-chart')).getAllByRole('button')[1];
    act(() => point.focus());
    fireEvent.click(point);
    view.rerender(<ContextWindowHistory history={history(4)} />);
    expect(point).toHaveFocus();
    expect(point).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('context-window-selected')).toHaveTextContent('2 / 1.0K · 0.2%');
  });

  it('recovers chart focus only when a focused retained receipt is removed', async () => {
    const initial = history(3);
    const view = await renderWithI18n(
      <>
        <ContextWindowHistory history={initial} />
        <button>Outside action</button>
      </>,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('context-window-history-toggle'));
    const points = within(screen.getByTestId('context-window-history-chart')).getAllByRole('button');
    act(() => points[0].focus());
    fireEvent.click(points[0]);
    const next = { ...initial, samples: initial.samples.slice(1) };
    view.rerender(
      <>
        <ContextWindowHistory history={next} />
        <button>Outside action</button>
      </>
    );
    const retained = within(screen.getByTestId('context-window-history-chart')).getAllByRole('button');
    expect(retained.at(-1)).toHaveFocus();
    const outside = screen.getByRole('button', { name: 'Outside action' });
    outside.focus();
    view.rerender(
      <>
        <ContextWindowHistory history={history(4)} />
        <button>Outside action</button>
      </>
    );
    expect(outside).toHaveFocus();
  });

  it('does not move document focus or scroll an inactive history while new data arrives', async () => {
    const initial = history(3);
    const view = await renderWithI18n(<ContextWindowHistory history={initial} />, 'en-US');
    fireEvent.click(screen.getByTestId('context-window-history-toggle'));
    const point = within(screen.getByTestId('context-window-history-chart')).getAllByRole('button')[0];
    act(() => point.focus());
    fireEvent.click(point);
    view.rerender(
      <ContextWindowHistory history={{ ...initial, samples: initial.samples.slice(1) }} interactive={false} />
    );
    expect(document.body).toHaveFocus();
    const calls = vi.mocked(HTMLElement.prototype.scrollIntoView).mock.calls.length;
    view.rerender(<ContextWindowHistory history={history(4)} interactive={false} />);
    expect(document.body).toHaveFocus();
    expect(vi.mocked(HTMLElement.prototype.scrollIntoView).mock.calls).toHaveLength(calls);
  });
});
