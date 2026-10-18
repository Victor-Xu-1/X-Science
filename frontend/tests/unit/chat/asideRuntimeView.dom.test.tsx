import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { TMessage } from '@/common/chat/chatLib';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

const wire = vi.hoisted(() => ({
  messages: vi.fn(),
  snapshot: vi.fn(),
  user: null as null | ((event: { conversation_id: string }) => void),
  stream: vi.fn(() => () => {}),
  stopUsers: vi.fn(),
  stopInvalidation: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    acpConversation: { responseStream: { on: wire.stream } },
    conversation: {
      userCreated: {
        on: (callback: (event: { conversation_id: string }) => void) => {
          wire.user = callback;
          return wire.stopUsers;
        },
      },
    },
  },
}));
vi.mock('@/common/adapter/ipcDatabaseClient', () => ({ requestConversationMessages: wire.messages }));
vi.mock('@/renderer/pages/conversation/Messages/hooks', () => ({
  normalizeDbMessagePage: (items: TMessage[]) => items,
}));
vi.mock('@/renderer/pages/conversation/Messages/messageWindowReconciliation', () => ({
  mergeLoadedPageWithCurrent: (_frame: string, page: TMessage[]) => page,
}));
vi.mock('@/renderer/services/xScienceFrameReads', () => ({ invalidateXScienceFrameReads: vi.fn() }));
vi.mock('@/renderer/services/xScienceRuntimeOperations', () => ({
  loadXScienceRuntimeSnapshot: wire.snapshot,
  subscribeXScienceRuntimeInvalidation: () => wire.stopInvalidation,
}));

import { useAsideRuntimeView } from '@/renderer/components/chat/BtwOverlay/useAsideRuntimeView';

describe('aside durable runtime subscriptions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner-1');
    wire.user = null;
    wire.messages.mockResolvedValue({
      items: [
        {
          id: 'input:0',
          msg_id: 'input',
          conversation_id: 'child',
          type: 'text',
          position: 'right',
          content: { content: 'Question' },
        },
      ],
      branch_id: 'br_12345678',
      through_publication_sequence: 1,
    });
    wire.snapshot.mockResolvedValue(normalizeXScienceRuntimeSnapshot({ id: 'child', status: 'completed' }));
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('observes canonical user-created events only for the owned child and releases all subscriptions', async () => {
    const receipt = {
      frameId: 'child',
      intentId: 'input',
      inputMessageId: 'input',
      sourceBranchId: 'br_12345678',
      sourceGeneration: 1,
      throughPublicationSequence: 0,
      asSession: false,
      replayed: false,
    };
    const view = renderHook(() => useAsideRuntimeView(receipt, true, 1));
    await act(async () => {});
    expect(view.result.current.entries).toEqual([{ id: 'input:0', kind: 'message', role: 'user', text: 'Question' }]);
    expect(wire.messages).toHaveBeenCalledOnce();
    expect(wire.user).not.toBeNull();
    act(() => wire.user!({ conversation_id: 'other-child' }));
    await act(async () => vi.advanceTimersByTimeAsync(750));
    expect(wire.messages).toHaveBeenCalledOnce();
    act(() => wire.user!({ conversation_id: 'child' }));
    await act(async () => vi.advanceTimersByTimeAsync(750));
    expect(wire.messages).toHaveBeenCalledTimes(2);
    view.unmount();
    expect(wire.stopUsers).toHaveBeenCalledOnce();
    expect(wire.stopInvalidation).toHaveBeenCalledOnce();
  });
});
