import React from 'react';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { InspectionCallDetails } from '@/renderer/components/xScience/runtime/ContextInspectionDetails';
import { parseContextInspection } from '@/renderer/services/runtime/contextInspection';
import { renderWithI18n } from '../i18nTestUtils';

afterEach(cleanup);
const timing = () => ({
  version: 1,
  origin: 'observer_entry',
  offsetsUs: { firstPrivateActivity: 0, firstNativeContent: 1200, delegateFinished: 9000 },
  preparation: [{ stage: 'resolve', beginUs: 0, endUs: 400 }],
  transportAttempts: [
    { attempt: 1, protocol: 'openai-compatible', offsetsUs: { attemptEntry: 500, doEntered: 600, headersReady: 800 } },
    {
      attempt: 3,
      protocol: 'openai-compatible',
      offsetsUs: {
        attemptEntry: 900,
        doEntered: 950,
        headersReady: 1000,
        firstBodyRead: 1100,
        firstDecodedGrowth: 1150,
      },
    },
  ],
  truncated: false,
  saturated: false,
});

function parsedCall(phaseTiming?: unknown) {
  const value = {
    version: 1,
    scope: {
      conversationId: 'fixture',
      frameIncarnationId: 'incarnation',
      streamUid: 'stream',
      streamEpoch: 1,
      branchId: 'branch',
      branchGeneration: 1,
      throughPublicationSequence: 3,
    },
    coverage: {
      association: 'partial',
      retainedCallCount: 1,
      observedSampleCount: 1,
      unassociatedCallCount: 0,
      truncated: false,
    },
    calls: [
      {
        id: 'call',
        model: 'fixture',
        state: 'complete',
        observedAt: '2026-11-11T00:00:00Z',
        runnerAttempt: 1,
        usedTokens: 3,
        limitTokens: 0,
        limitSource: 'unknown',
        outputTokens: 1,
        ...(phaseTiming === undefined ? {} : { phaseTiming }),
      },
    ],
    turns: [
      {
        id: 'turn',
        inputMessageId: 'input',
        inputRevision: 1,
        status: 'running',
        runnerAttempts: [1],
        callIds: ['call'],
        outcomes: [],
      },
    ],
  };
  return parseContextInspection(
    value,
    'fixture',
    () => {
      throw new Error('No sample fixture');
    },
    () => true
  )!.calls[0];
}

describe('Completed call phase facts in the existing Calls detail', () => {
  it.each(['en-US', 'zh-CN'] as const)('keeps the optional detail compact and localizes %s', async (language) => {
    await renderWithI18n(<InspectionCallDetails call={parsedCall(timing())} index={0} />, language);
    const summary = screen.getByText(language === 'en-US' ? 'Recorded call phases' : '已记录调用时序');
    const details = summary.closest('details')!;
    expect(details).not.toHaveAttribute('open');
    fireEvent.click(summary);
    expect(details).toHaveAttribute('open');
    expect(
      screen.getByText(language === 'en-US' ? 'First decoded private activity' : '首个已解码私有活动')
    ).toBeInTheDocument();
    expect(screen.getAllByText(/0 ms/).length).toBeGreaterThan(0);
    expect(screen.getByText(/HTTP.*1/)).toBeInTheDocument();
    expect(screen.getByText(/HTTP.*3/)).toBeInTheDocument();
    expect(screen.queryByText(/HTTP.*2/)).not.toBeInTheDocument();
    expect(screen.queryByText(/TTFB|TTFT/)).not.toBeInTheDocument();
  });

  it('preserves a legacy call and unknown capacity when no timing was recorded', async () => {
    const call = parsedCall();
    await renderWithI18n(<InspectionCallDetails call={call} index={0} />, 'en-US');
    expect(call.limitTokens).toBe(0);
    expect(call.limitSource).toBe('unknown');
    expect(screen.getByText('Not recorded')).toBeInTheDocument();
    expect(screen.getByText(/fixture/)).toBeInTheDocument();
  });

  it.each([
    null,
    'damaged',
    { ...timing(), version: 2 },
    { ...timing(), offsetsUs: { firstNativeContent: -1 } },
    { ...timing(), offsetsUs: { delegateFinished: Number.MAX_SAFE_INTEGER + 1 } },
    {
      ...timing(),
      transportAttempts: [
        { attempt: 1, protocol: 'openai-compatible', offsetsUs: { attemptEntry: 3, headersReady: 2 } },
      ],
    },
  ])('drops only malformed optional metadata: %j', (bad) => {
    const call = parsedCall(bad);
    expect(call.id).toBe('call');
    expect(call.usedTokens).toBe(3);
    expect(call.phaseTiming).toBeUndefined();
  });
});
