/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, renderHook, waitFor } from '@testing-library/react';
import { createElement, type PropsWithChildren } from 'react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  type ConversationCommandQueueRuntimeGate,
  type ConversationCommandQueueItem,
  useConversationCommandQueue,
} from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { pdfSelection } from '../../renderer/conversation/pdfReadingFixtures';
import {
  clearConversationReadCaches,
  getConversationOrNullShared,
} from '@/renderer/pages/conversation/utils/conversationCache';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, options?: { defaultValue?: string }) => options?.defaultValue ?? _key,
  }),
}));

vi.mock('@arco-design/web-react', () => ({
  Message: {
    info: vi.fn(),
    warning: vi.fn(),
  },
}));

const createSwrWrapper = () => {
  const cache = new Map();

  return function SwrTestWrapper({ children }: PropsWithChildren) {
    return createElement(
      SWRConfig,
      {
        value: {
          provider: () => cache,
          dedupingInterval: 0,
          revalidateOnFocus: false,
          revalidateOnReconnect: false,
        },
      },
      children
    );
  };
};

const processingGate: ConversationCommandQueueRuntimeGate = {
  hydrated: true,
  canSendMessage: true,
  isProcessing: true,
};

const idleGate: ConversationCommandQueueRuntimeGate = {
  hydrated: true,
  canSendMessage: true,
  isProcessing: false,
};

const renderQueue = ({
  conversation_id,
  runtimeGate,
  isBusy = false,
  onExecute = vi.fn().mockResolvedValue(undefined),
  isItemBlocked,
}: {
  conversation_id: string;
  runtimeGate: ConversationCommandQueueRuntimeGate;
  isBusy?: boolean;
  onExecute?: (item: Parameters<Parameters<typeof useConversationCommandQueue>[0]['onExecute']>[0]) => Promise<void>;
  isItemBlocked?: (item: ConversationCommandQueueItem) => boolean;
}) =>
  renderHook(
    ({ gate, busy }) =>
      useConversationCommandQueue({
        conversation_id,
        enabled: true,
        isBusy: busy,
        runtimeGate: gate,
        onExecute,
        isItemBlocked,
      }),
    {
      initialProps: { gate: runtimeGate, busy: isBusy },
      wrapper: createSwrWrapper(),
    }
  );

describe('useConversationCommandQueue drain', () => {
  it('C12 freezes reading selection in the existing local queue and retains it on real admission conflict', async () => {
    setRendererAccountOwner('pdf-owner');
    const original = pdfSelection();
    const onExecute = vi.fn().mockRejectedValue(new Error('PDF_READING_REVISION_STALE'));
    const { result, rerender } = renderQueue({ conversation_id: 'pdf-queued', runtimeGate: processingGate, onExecute });
    act(() => result.current.enqueue({ input: 'read page four', files: [], readingContext: original }));
    await waitFor(() => expect(result.current.items).toHaveLength(1));
    original.expected_revision = 99;
    original.page_number = 8;
    rerender({ gate: idleGate, busy: false });
    await waitFor(() => expect(onExecute).toHaveBeenCalledTimes(1));
    expect(onExecute.mock.calls[0][0].readingContext).toEqual(pdfSelection());
    await waitFor(() => expect(result.current.items[0]?.pausedReason).toBe('send_failed'));
    expect(result.current.items[0].readingContext).toEqual(pdfSelection());
    expect(result.current.items).toHaveLength(1);
  });

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    resetRendererAccountScopeForTest();
    vi.spyOn(console, 'info').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
    localStorage.clear();
    resetRendererAccountScopeForTest();
  });

  it('drains a queued command when the runtime becomes idle', async () => {
    const onExecute = vi.fn().mockResolvedValue(undefined);
    const { result, rerender } = renderQueue({
      conversation_id: 'conv-1',
      runtimeGate: processingGate,
      onExecute,
    });

    act(() => {
      result.current.enqueue({ input: 'queued follow-up', files: [] });
    });
    await waitFor(() => expect(result.current.items).toHaveLength(1));

    rerender({ gate: idleGate, busy: false });

    await waitFor(() => expect(onExecute).toHaveBeenCalledTimes(1));
    expect(onExecute).toHaveBeenCalledWith(expect.objectContaining({ input: 'queued follow-up' }));
  });

  it('keeps persisted queues isolated by authenticated owner', async () => {
    setRendererAccountOwner('owner-a');
    const ownerA = renderQueue({ conversation_id: 'conv-shared', runtimeGate: processingGate });
    act(() => ownerA.result.current.enqueue({ input: 'owner-a draft', files: [] }));
    await waitFor(() => expect(ownerA.result.current.items).toHaveLength(1));
    ownerA.unmount();

    setRendererAccountOwner('owner-b');
    const ownerB = renderQueue({ conversation_id: 'conv-shared', runtimeGate: processingGate });
    await waitFor(() => expect(ownerB.result.current.items).toHaveLength(0));
    ownerB.unmount();

    setRendererAccountOwner('owner-a');
    const ownerAReturn = renderQueue({ conversation_id: 'conv-shared', runtimeGate: processingGate });
    await waitFor(() =>
      expect(ownerAReturn.result.current.items).toEqual([expect.objectContaining({ input: 'owner-a draft' })])
    );
    expect([...Array(localStorage.length)].map((_, index) => localStorage.key(index))).toEqual([
      expect.stringContaining('/owner-a/conv-shared'),
    ]);
  });

  it('preserves an actual queued draft and persisted bytes after a read-only detail404', async () => {
    setRendererAccountOwner('owner-a');
    const id = 'queue-preserved-on-missing';
    const queue = renderQueue({ conversation_id: id, runtimeGate: processingGate });
    act(() => queue.result.current.enqueue({ input: 'queued draft fixture', files: [] }));
    await waitFor(() => expect(queue.result.current.items).toHaveLength(1));
    const key = `conversation-command-queue/v2/owner-a/${id}`;
    const persisted = localStorage.getItem(key);
    expect(persisted).toContain('queued draft fixture');
    vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 404 })));
    try {
      await act(async () => {
        await expect(getConversationOrNullShared(id)).resolves.toBeNull();
      });

      expect(queue.result.current.items).toEqual([expect.objectContaining({ input: 'queued draft fixture' })]);
      expect(localStorage.getItem(key)).toBe(persisted);
    } finally {
      vi.unstubAllGlobals();
      clearConversationReadCaches();
    }
  });

  it('keeps an unresolved Aside head in order and drains it after the exact reservation is released', async () => {
    const execute = vi.fn().mockResolvedValue(undefined);
    const queue = renderHook(
      ({ blocked, gate }) =>
        useConversationCommandQueue({
          conversation_id: 'aside-head',
          isBusy: false,
          runtimeGate: gate,
          onExecute: execute,
          isItemBlocked: (item) => blocked && item.input === 'Transferred question',
        }),
      { initialProps: { blocked: true, gate: processingGate }, wrapper: createSwrWrapper() }
    );
    act(() => {
      queue.result.current.enqueue({ input: 'Transferred question', files: [] });
      queue.result.current.enqueue({ input: 'Next normal question', files: [] });
    });
    await waitFor(() => expect(queue.result.current.items).toHaveLength(2));
    queue.rerender({ blocked: true, gate: idleGate });
    await act(async () => {
      await Promise.resolve();
    });
    expect(execute).not.toHaveBeenCalled();
    expect(queue.result.current.items.map((item) => item.input)).toEqual([
      'Transferred question',
      'Next normal question',
    ]);
    queue.rerender({ blocked: false, gate: idleGate });
    await waitFor(() => expect(execute).toHaveBeenCalledOnce());
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({ input: 'Transferred question' }));
  });

  it('blocks sendNow and retry only for the matching pending item, not every explicit queue action', async () => {
    const execute = vi.fn().mockResolvedValue(undefined);
    const queue = renderQueue({
      conversation_id: 'aside-explicit',
      runtimeGate: processingGate,
      onExecute: execute,
      isItemBlocked: (item) => item.input === 'Transferred question',
    });
    act(() => {
      queue.result.current.enqueue({ input: 'Transferred question', files: [] });
      queue.result.current.enqueue({ input: 'Other question', files: [] });
    });
    await waitFor(() => expect(queue.result.current.items).toHaveLength(2));
    const [pending, other] = queue.result.current.items;
    await act(async () => {
      expect(await queue.result.current.sendNow(pending.id)).toBe(false);
      expect(await queue.result.current.retry(pending.id)).toBe(false);
      expect(await queue.result.current.sendNow(other.id)).toBe(true);
    });
    expect(execute).toHaveBeenCalledOnce();
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({ id: other.id }));
    expect(queue.result.current.getQueuedItem(pending.id)).toEqual(pending);
  });

  it('uses the current reservation for an older sendNow callback after reordering or rerender', async () => {
    const execute = vi.fn().mockResolvedValue(undefined);
    const queue = renderHook(
      ({ blocked }) =>
        useConversationCommandQueue({
          conversation_id: 'aside-current-ref',
          isBusy: true,
          runtimeGate: processingGate,
          onExecute: execute,
          isItemBlocked: (item) => blocked && item.input === 'Transferred question',
        }),
      { initialProps: { blocked: false }, wrapper: createSwrWrapper() }
    );
    act(() => {
      queue.result.current.enqueue({ input: 'Transferred question', files: [] });
      queue.result.current.enqueue({ input: 'Other question', files: [] });
    });
    await waitFor(() => expect(queue.result.current.items).toHaveLength(2));
    const [pending, other] = queue.result.current.items;
    const oldSendNow = queue.result.current.sendNow;
    act(() => queue.result.current.reorder(pending.id, other.id));
    queue.rerender({ blocked: true });
    await act(async () => {
      expect(await oldSendNow(pending.id)).toBe(false);
    });
    expect(execute).not.toHaveBeenCalled();
    expect(queue.result.current.getQueuedItem(pending.id)?.input).toBe('Transferred question');
  });

  it('restores the original position without a fake send_failed flag when reservation appears at dispatch', async () => {
    const execute = vi.fn().mockResolvedValue(undefined);
    let checks = 0;
    const queue = renderQueue({
      conversation_id: 'aside-dispatch',
      runtimeGate: processingGate,
      onExecute: execute,
      isItemBlocked: () => ++checks >= 2,
    });
    act(() => {
      queue.result.current.enqueue({ input: 'First normal question', files: [] });
      queue.result.current.enqueue({ input: 'Transferred question', files: [] });
    });
    await waitFor(() => expect(queue.result.current.items).toHaveLength(2));
    const original = queue.result.current.items;
    await act(async () => {
      expect(await queue.result.current.sendNow(original[1].id)).toBe(false);
    });
    expect(execute).not.toHaveBeenCalled();
    expect(queue.result.current.items).toEqual(original);
    expect(queue.result.current.items[1].pausedReason).toBeUndefined();
  });

  it('removes accepted transfers from both current and durable queue before releasing a reservation', async () => {
    setRendererAccountOwner('owner-1');
    const queue = renderQueue({ conversation_id: 'aside-consume', runtimeGate: processingGate });
    act(() => queue.result.current.enqueue({ input: 'Transferred question', files: [] }));
    await waitFor(() => expect(queue.result.current.items).toHaveLength(1));
    const item = queue.result.current.items[0];
    act(() => {
      expect(queue.result.current.remove(item.id)).toBe(true);
      expect(queue.result.current.getQueuedItem(item.id)).toBeNull();
      expect(localStorage.getItem('conversation-command-queue/v2/owner-1/aside-consume')).toBeNull();
    });
  });
});
