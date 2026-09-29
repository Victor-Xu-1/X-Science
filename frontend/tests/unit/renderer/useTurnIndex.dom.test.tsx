import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { TMessage } from '@/common/chat/chatLib';
import { buildTurnPreview } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/minimapUtils';
import { loadConversationTurnIndex } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/turnIndex';
import { useTurnIndex } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/useTurnIndex';
import { selectSynonBiomedBranch } from '@/renderer/services/synonBiomedConversationBranches';

vi.mock('@/common/adapter/ipcBridge', () => ({
  conversation: { historyRebased: { on: vi.fn(() => vi.fn()) } },
}));
vi.mock('@/renderer/pages/conversation/components/ConversationTitleMinimap/turnIndex', async (original) => ({
  ...(await original<object>()),
  loadConversationTurnIndex: vi.fn(),
}));
const load = vi.mocked(loadConversationTurnIndex);
const index = (messages: TMessage[], branchId?: string) => ({ items: buildTurnPreview(messages), branchId });
const message = (id: string, position: 'left' | 'right' = 'right'): TMessage => ({
  id,
  conversation_id: 'c',
  type: 'text',
  position,
  content: { content: id },
});

beforeEach(() => load.mockReset());
describe('live turn index lifetime', () => {
  it('does not repeat state updates for equivalent newly allocated windows', async () => {
    load.mockResolvedValue(index([message('q1')]));
    let renders = 0;
    const { result } = renderHook(() => {
      renders++;
      return useTurnIndex('c', [message('q1')]);
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(renders).toBeLessThan(10);
    expect(load).toHaveBeenCalledTimes(1);
  });
  it('does not append the stale visible window after a branch index arrives first', async () => {
    load.mockResolvedValueOnce(index([message('old-branch')], 'br_00000000'));
    const { result, rerender } = renderHook(({ messages, branchId }) => useTurnIndex('c', messages, branchId), {
      initialProps: { messages: [message('old-branch')], branchId: 'br_00000000' },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    load.mockResolvedValueOnce(index([message('new-branch')], 'br_12345678'));
    act(() => selectSynonBiomedBranch('c', 'br_12345678'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items.map((item) => item.messageId)).toEqual(['new-branch']);
    rerender({ messages: [message('new-branch'), message('new-turn')], branchId: 'br_12345678' });
    expect(result.current.items.map((item) => item.messageId)).toEqual(['new-branch', 'new-turn']);
  });
  it('retains accepted new turns and their answers when navigation replaces the visible window', async () => {
    const q1 = message('q1');
    const q2 = message('q2');
    const a2 = message('a2', 'left');
    load.mockResolvedValue(index([q1]));
    const { result, rerender } = renderHook(({ messages }) => useTurnIndex('c', messages), {
      initialProps: { messages: [q1] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    rerender({ messages: [q1, q2, a2] });
    expect(result.current.items.map((item) => item.messageId)).toEqual(['q1', 'q2']);
    rerender({ messages: [q1] });
    expect(result.current.items.map((item) => item.messageId)).toEqual(['q1', 'q2']);
    expect(result.current.items[1].answer).toBe('a2');
    expect(load).toHaveBeenCalledTimes(1);
  });
  it('does not carry retained turns into another conversation', async () => {
    load.mockResolvedValueOnce(index([message('q1')]));
    const { result, rerender } = renderHook(({ owner, messages }) => useTurnIndex(owner, messages), {
      initialProps: { owner: 'c', messages: [message('q1'), message('q2')] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items).toHaveLength(2);
    load.mockResolvedValueOnce(index([]));
    rerender({ owner: 'other', messages: [] });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items).toEqual([]);
  });
  it('replaces the retained index on a new history revision', async () => {
    load.mockResolvedValueOnce(index([message('q1')]));
    const { result, rerender } = renderHook(({ messages }) => useTurnIndex('c', messages), {
      initialProps: { messages: [message('q1'), message('q2')] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items).toHaveLength(2);
    load.mockResolvedValueOnce(index([message('replacement')]));
    act(() => result.current.retry());
    rerender({ messages: [message('replacement')] });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items.map((item) => item.messageId)).toEqual(['replacement']);
  });
  it('retains compact previews rather than full streamed answers', async () => {
    const answer = { ...message('a1', 'left'), content: { content: 'x'.repeat(100_000) } } as TMessage;
    load.mockResolvedValue(index([message('q1')]));
    const { result, rerender } = renderHook(({ messages }) => useTurnIndex('c', messages), {
      initialProps: { messages: [message('q1'), answer] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    rerender({ messages: [] });
    expect(result.current.items[0].answerRaw).toHaveLength(512);
  });
  it('ignores an aborted previous conversation response that arrives late', async () => {
    let finish!: (value: Awaited<ReturnType<typeof loadConversationTurnIndex>>) => void;
    load.mockReturnValueOnce(new Promise((resolve) => (finish = resolve)));
    const { result, rerender } = renderHook(({ owner }) => useTurnIndex(owner, []), {
      initialProps: { owner: 'c' },
    });
    const signal = load.mock.calls[0][1]?.signal;
    load.mockResolvedValueOnce(index([message('new-conversation')]));
    rerender({ owner: 'other' });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(signal?.aborted).toBe(true);
    await act(async () => finish(index([message('obsolete')])));
    expect(result.current.items.map((item) => item.messageId)).toEqual(['new-conversation']);
  });
});
