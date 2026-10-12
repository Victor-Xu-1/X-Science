import { act, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { XScienceDelegateDock } from '@/renderer/pages/conversation/components/XScienceDelegateDock';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({ loadLineage: vi.fn(), wake: null as null | (() => void) }));

vi.mock('@/renderer/services/xScienceDelegateLineage', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/services/xScienceDelegateLineage')>();
  return { ...actual, loadXScienceDelegateLineage: mocks.loadLineage };
});

vi.mock('@/renderer/pages/conversation/components/XScienceLineageMessagesDrawer', () => ({
  default: () => null,
}));

vi.mock('@/renderer/services/xScienceConversationWake', () => ({
  subscribeXScienceConversationWake: (_conversationId: string, wake: () => void) => {
    mocks.wake = wake;
    return () => {
      if (mocks.wake === wake) mocks.wake = null;
    };
  },
}));

const lineage = {
  rootFrameId: 'root',
  currentFrameId: 'root',
  current: {
    frameId: 'root',
    rootFrameId: 'root',
    parentFrameId: null,
    ordinal: 0,
    label: 'Root',
    agentName: 'OPERON',
    status: 'running' as const,
    statusDescription: null,
    taskSummary: null,
    messageCount: 1,
    directChildCount: 1,
  },
  ancestors: [],
  directChildren: [
    {
      frameId: 'child-1',
      rootFrameId: 'root',
      parentFrameId: 'root',
      ordinal: 1,
      label: 'Evidence review',
      agentName: 'REVIEWER',
      status: 'running' as const,
      statusDescription: null,
      taskSummary: 'Review evidence',
      messageCount: 2,
      directChildCount: 0,
    },
  ],
  rootChildren: [],
  totals: { needsInput: 0, running: 1, failed: 0, completed: 0, stopped: 0 },
};

describe('XScienceDelegateDock recovery', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    mocks.loadLineage.mockReset();
    mocks.wake = null;
  });

  it('hides transient lineage failures and recovers in the background', async () => {
    vi.useFakeTimers();
    mocks.loadLineage.mockRejectedValueOnce(new Error('temporary failure')).mockResolvedValueOnce(lineage);

    try {
      await renderWithI18n(
        <MemoryRouter>
          <XScienceDelegateDock conversationId='root' polling={false} />
        </MemoryRouter>,
        'en-US'
      );
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(mocks.loadLineage).toHaveBeenCalledTimes(1);
      expect(screen.queryByTestId('xscience-delegate-dock-error')).not.toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_999);
      });
      expect(mocks.loadLineage).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1);
      });

      expect(screen.getByRole('button', { name: 'Open subagent 1 Evidence review' })).toBeInTheDocument();
      expect(screen.queryByTestId('xscience-delegate-dock-error')).not.toBeInTheDocument();
      expect(mocks.loadLineage).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('refreshes active lineage on durable wakes without periodic polling', async () => {
    vi.useFakeTimers();
    mocks.loadLineage.mockResolvedValue(lineage);
    try {
      await renderWithI18n(
        <MemoryRouter>
          <XScienceDelegateDock conversationId='root' polling />
        </MemoryRouter>,
        'en-US'
      );
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(mocks.loadLineage).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(12 * 60 * 60 * 1_000);
      });
      expect(mocks.loadLineage).toHaveBeenCalledTimes(1);

      await act(async () => {
        mocks.wake?.();
        mocks.wake?.();
        await vi.advanceTimersByTimeAsync(250);
      });
      expect(mocks.loadLineage).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
