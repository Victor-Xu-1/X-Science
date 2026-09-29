import React from 'react';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import MessageRoundFooter, {
  formatRoundDuration,
} from '@/renderer/pages/conversation/Messages/components/MessageRoundFooter';
import type { RoundSummary } from '@/common/chat/roundSummary';

const summary: RoundSummary = {
  attempt: 3,
  input_revision: 2,
  completed_at: 1790610800000,
  elapsed_ms: 10000,
  call_count: 2,
  reported_call_count: 2,
  usage_state: 'complete',
  tokens: { input: 1251, cache_read: 52024, cache_write: 0, output: 377, total: 53652 },
  models: ['actual-model'],
};

describe('completed round footer', () => {
  it('opens real categories and actual model without changing completion information', async () => {
    await renderWithI18n(<MessageRoundFooter summary={summary} />);
    expect(screen.getByTestId('message-round-footer')).toHaveTextContent('10s');
    const before = screen.getByTestId('message-round-footer').textContent;
    fireEvent.click(screen.getByRole('button', { name: /调用|Calls/ }));
    const panel = await screen.findByTestId('round-usage-panel');
    expect(panel).toHaveTextContent('53,275');
    expect(panel.querySelector('[data-usage-row="input_total"]')).toHaveTextContent('53,275');
    const breakdown = panel.querySelector('[data-testid="round-input-breakdown"]');
    expect(breakdown).toHaveTextContent('1,251');
    expect(breakdown).toHaveTextContent('52,024');
    expect(panel.querySelectorAll('[data-testid="round-usage-bar"] > [data-category]')).toHaveLength(2);
    for (const text of ['1,251', '52,024', '377', '53,652', 'actual-model']) expect(panel).toHaveTextContent(text);
    expect(panel).not.toHaveTextContent('智能体');
    fireEvent.keyDown(panel, { key: 'Escape' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /调用|Calls/ })).toHaveAttribute('aria-expanded', 'false')
    );
    expect(screen.getByTestId('message-round-footer').textContent).toBe(before);
  });
  it('shows missing usage instead of fictitious zero counts', async () => {
    await renderWithI18n(
      <MessageRoundFooter
        summary={{ ...summary, tokens: null, usage_state: 'unavailable', reported_call_count: 0, models: [] }}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: /调用|Calls/ }));
    expect(await screen.findByTestId('round-usage-panel')).toHaveTextContent(/未记录|Not recorded/);
    expect(screen.getByTestId('round-usage-panel')).not.toHaveTextContent('53,652');
    expect(screen.queryByTestId('round-usage-bar')).not.toBeInTheDocument();
  });
  it('does not normalize inconsistent reported totals into a misleading full bar', async () => {
    await renderWithI18n(<MessageRoundFooter summary={{ ...summary, tokens: { ...summary.tokens!, total: 99999 } }} />);
    fireEvent.click(screen.getByRole('button', { name: /调用|Calls/ }));
    const panel = await screen.findByTestId('round-usage-panel');
    expect(panel).toHaveTextContent('99,999');
    expect(panel).toHaveTextContent(/不一致|differs/);
    expect(screen.queryByTestId('round-usage-bar')).not.toBeInTheDocument();
  });
  it('labels a partial provider report and preserves long durations', async () => {
    await renderWithI18n(
      <MessageRoundFooter summary={{ ...summary, usage_state: 'partial', reported_call_count: 1 }} />
    );
    fireEvent.click(screen.getByRole('button', { name: /调用|Calls/ }));
    expect(await screen.findByTestId('round-usage-panel')).toHaveTextContent('1 / 2');
    expect(formatRoundDuration(90061000)).toBe('25h 1m 1s');
  });
});
