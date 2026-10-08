/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ContextUsagePanel from '@/renderer/components/synonBiomed/runtime/ContextUsagePanel';
import { renderWithI18n } from '../i18nTestUtils';

const record = {
  status: 'available',
  snapshot: {
    sessionId: 'context-interaction-fixture',
    requestId: 'fixture-request',
    model: 'fixture-model',
    observedAt: '2026-10-08T00:00:00Z',
    state: 'complete',
    source: 'provider',
    usedTokens: 20,
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
};

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(record))))
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Real context dropdown interaction', () => {
  it('moves focus into the labelled non-modal card when opened', async () => {
    await renderWithI18n(<ContextUsagePanel conversationId={record.snapshot.sessionId} />, 'en-US');
    const trigger = screen.getByRole('button', { name: 'Context usage' });
    trigger.focus();
    fireEvent.click(trigger);
    const card = await screen.findByRole('dialog', { name: 'Context usage' });
    await screen.findByTestId('context-usage-percent');
    expect(trigger.querySelector('svg')).toHaveAttribute('width', '16');
    const close = screen.getByRole('button', { name: 'Close' });
    await waitFor(() => expect(close).toHaveFocus());
    expect(card).not.toHaveAttribute('aria-modal', 'true');
    expect(trigger.getAttribute('aria-controls')).toBe(card.id);
  });

  it('closes with Escape and returns to the connected opener without changing usage', async () => {
    await renderWithI18n(<ContextUsagePanel conversationId={record.snapshot.sessionId} />, 'en-US');
    const trigger = screen.getByRole('button', { name: 'Context usage' });
    trigger.focus();
    fireEvent.click(trigger);
    await screen.findByTestId('context-usage-percent');
    expect(screen.getByTestId('context-usage-percent')).toHaveTextContent('20.0%');
    const close = screen.getByRole('button', { name: 'Close' });
    close.focus();
    fireEvent.keyDown(close, { key: 'Escape' });
    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    expect(trigger).toHaveFocus();
  });

  it('does not steal focus from a deliberate outside destination on dismissal', async () => {
    await renderWithI18n(
      <>
        <ContextUsagePanel conversationId={record.snapshot.sessionId} />
        <button>Outside action</button>
      </>,
      'en-US'
    );
    const trigger = screen.getByRole('button', { name: 'Context usage' });
    trigger.focus();
    fireEvent.click(trigger);
    await screen.findByRole('dialog', { name: 'Context usage' });
    const outside = screen.getByRole('button', { name: 'Outside action' });
    outside.focus();
    fireEvent.mouseDown(outside);
    fireEvent.click(outside);
    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    expect(outside).toHaveFocus();
  });

  it('closes the card from its focused details without trapping the tooltip or changing the receipt', async () => {
    await renderWithI18n(<ContextUsagePanel conversationId={record.snapshot.sessionId} />, 'en-US');
    const trigger = screen.getByRole('button', { name: 'Context usage' });
    trigger.focus();
    fireEvent.click(trigger);
    const summary = await screen.findByRole('group', { name: 'Context usage' });
    act(() => summary.focus());
    fireEvent.keyDown(summary, { key: 'Escape' });
    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    expect(trigger).toHaveFocus();
  });
});
