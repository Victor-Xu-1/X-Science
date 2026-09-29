/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { TMessage } from '@/common/chat/chatLib';
import { SYNON_BIOMED_OPTIMISTIC_USER_MESSAGE_PREFIX } from '@/renderer/pages/conversation/Messages/optimisticUserMessage';
import { useConversationScrollController } from '@/renderer/pages/conversation/Messages/useConversationScrollController';

function textMessage(id: string, position: 'left' | 'right', blockIndex?: number): TMessage {
  return {
    id,
    msg_id: id,
    conversation_id: 'conversation-1',
    type: 'text',
    position,
    content: {
      content: id,
      ...(blockIndex === undefined ? {} : { synonBiomed: { blockIndex } }),
    },
    created_at: 1,
  } as TMessage;
}

describe('useConversationScrollController', () => {
  it('retains an explicit anchor across late content sizing and releases it on a reading gesture', () => {
    const frames: FrameRequestCallback[] = [];
    const requestFrame = vi
      .spyOn(window, 'requestAnimationFrame')
      .mockImplementation((callback) => (frames.push(callback), frames.length));
    const cancelFrame = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    const flush = () => {
      while (frames.length) frames.shift()?.(0);
    };
    const messages = [textMessage('question', 'right'), textMessage('answer', 'left')];
    const seek = vi.fn(() => true);
    const tail = vi.fn(() => true);
    const { result, unmount } = renderHook(() =>
      useConversationScrollController({
        conversationId: 'conversation-1',
        messages,
        itemCount: 2,
        lastUserMessageId: 'question',
        lastUserRowIndex: 0,
        scrollMessageIntoView: seek,
        scrollToBottomItem: tail,
      })
    );
    act(flush);
    tail.mockClear();
    act(() => {
      result.current.retainMessageAnchor('question', 'center');
      result.current.handleTotalListHeightChanged(900);
      flush();
    });
    expect(seek).toHaveBeenLastCalledWith('question', { behavior: 'auto', block: 'center' });
    expect(tail).not.toHaveBeenCalled();
    const count = seek.mock.calls.length;
    act(() => {
      result.current.handleTotalListHeightChanged(900);
      flush();
    });
    expect(seek).toHaveBeenCalledTimes(count);
    act(() => {
      result.current.handleUserScrollIntent('away-from-tail');
      result.current.handleTotalListHeightChanged(1700);
      flush();
    });
    expect(seek).toHaveBeenCalledTimes(count);
    expect(result.current.canLoadNextPage()).toBe(true);
    unmount();
    requestFrame.mockRestore();
    cancelFrame.mockRestore();
  });

  it('requires a fresh reading gesture after an explicit anchor jump before paging history', () => {
    const { result, rerender } = renderHook(
      ({ conversationId }) =>
        useConversationScrollController({
          conversationId,
          messages: [textMessage('question', 'right')],
          itemCount: 1,
          lastUserMessageId: 'question',
          lastUserRowIndex: 0,
          scrollMessageIntoView: vi.fn(() => true),
          scrollToBottomItem: vi.fn(() => true),
        }),
      { initialProps: { conversationId: 'conversation-1' } }
    );
    expect(result.current.canLoadPreviousPage()).toBe(false);
    expect(result.current.canLoadNextPage()).toBe(false);
    act(() => result.current.handleUserScrollIntent('away-from-tail'));
    expect(result.current.canLoadPreviousPage()).toBe(true);
    expect(result.current.canLoadNextPage()).toBe(true);
    act(() => result.current.retainMessageAnchor('question'));
    act(() => result.current.handleAtBottomStateChange(true));
    expect(result.current.canLoadPreviousPage()).toBe(false);
    expect(result.current.canLoadNextPage()).toBe(false);
    expect(result.current.followOutput(true)).toBe(false);
    act(() => result.current.handleUserScrollIntent('toward-tail'));
    expect(result.current.canLoadNextPage()).toBe(true);
    act(() => result.current.scrollToLastUser());
    expect(result.current.canLoadNextPage()).toBe(false);
    act(() => result.current.scrollToBottom());
    expect(result.current.canLoadNextPage()).toBe(true);
    rerender({ conversationId: 'conversation-2' });
    expect(result.current.canLoadNextPage()).toBe(false);
  });

  it('derives the two Claude-style controls from Virtuoso bottom and range state', () => {
    const messages = [textMessage('question', 'right'), textMessage('answer', 'left')];
    const { result } = renderHook(() =>
      useConversationScrollController({
        conversationId: 'conversation-1',
        messages,
        itemCount: messages.length,
        lastUserMessageId: 'question',
        lastUserRowIndex: 0,
        scrollMessageIntoView: vi.fn(() => true),
        scrollToBottomItem: vi.fn(() => true),
      })
    );

    expect(result.current.showScrollButton).toBe(false);
    expect(result.current.showLastUserButton).toBe(false);

    act(() => {
      result.current.handleRangeChanged(1, 1);
      result.current.handleAtBottomStateChange(false);
    });

    expect(result.current.showScrollButton).toBe(true);
    expect(result.current.showLastUserButton).toBe(true);
  });

  it('lets Virtuoso follow output only while the user remains attached to the bottom', () => {
    const { result } = renderHook(() =>
      useConversationScrollController({
        conversationId: 'conversation-1',
        messages: [textMessage('answer', 'left')],
        itemCount: 1,
        lastUserMessageId: null,
        lastUserRowIndex: -1,
        scrollMessageIntoView: vi.fn(() => true),
        scrollToBottomItem: vi.fn(() => true),
      })
    );

    expect(result.current.followOutput(true)).toBe('auto');
    expect(result.current.followOutput(false)).toBe('auto');
    act(() => result.current.handleUserScrollIntent());
    expect(result.current.followOutput(true)).toBe(false);
    // A Virtuoso geometry report must not override an explicit user detach.
    act(() => result.current.handleAtBottomStateChange(true));
    expect(result.current.followOutput(true)).toBe(false);
    act(() => {
      result.current.handleUserScrollIntent('toward-tail');
      result.current.handleAtBottomStateChange(true);
    });
    expect(result.current.followOutput(true)).toBe('auto');
  });

  it('does not move the viewport when terminal content arrives after the user detached', () => {
    const scrollToBottomItem = vi.fn(() => true);
    const initial = [textMessage('question', 'right'), textMessage('answer-partial', 'left')];
    const { result, rerender } = renderHook(
      ({ messages }) =>
        useConversationScrollController({
          conversationId: 'conversation-1',
          messages,
          itemCount: messages.length,
          lastUserMessageId: 'question',
          lastUserRowIndex: 0,
          scrollMessageIntoView: vi.fn(() => true),
          scrollToBottomItem,
        }),
      { initialProps: { messages: initial } }
    );

    act(() => result.current.handleUserScrollIntent());
    scrollToBottomItem.mockClear();
    rerender({ messages: [...initial, textMessage('answer-final', 'left')] });

    expect(scrollToBottomItem).not.toHaveBeenCalled();
    expect(result.current.followOutput(false)).toBe(false);
  });

  it('realigns late list-height growth only while the user remains attached to the tail', () => {
    const frames: FrameRequestCallback[] = [];
    const requestFrame = vi
      .spyOn(window, 'requestAnimationFrame')
      .mockImplementation((callback) => (frames.push(callback), frames.length));
    const cancelFrame = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    const flushFrames = () => {
      while (frames.length > 0) frames.shift()?.(0);
    };
    const scrollToBottomItem = vi.fn(() => true);
    const messages = [textMessage('question', 'right'), textMessage('answer', 'left')];
    const { result, unmount } = renderHook(() =>
      useConversationScrollController({
        conversationId: 'conversation-1',
        messages,
        itemCount: messages.length,
        lastUserMessageId: 'question',
        lastUserRowIndex: 0,
        scrollMessageIntoView: vi.fn(() => true),
        scrollToBottomItem,
      })
    );
    act(flushFrames);
    scrollToBottomItem.mockClear();

    act(() => {
      result.current.handleTotalListHeightChanged(600);
      flushFrames();
    });
    expect(scrollToBottomItem).toHaveBeenCalledWith('auto');

    act(() => result.current.handleUserScrollIntent('away-from-tail'));
    scrollToBottomItem.mockClear();
    act(() => {
      result.current.handleTotalListHeightChanged(680);
      flushFrames();
    });
    expect(scrollToBottomItem).not.toHaveBeenCalled();

    unmount();
    requestFrame.mockRestore();
    cancelFrame.mockRestore();
  });

  it('jumps to the latest logical user task instead of walking upward through previous turns', () => {
    const scrollMessageIntoView = vi.fn(() => true);
    const messages = [
      textMessage('question-1', 'right'),
      textMessage('answer-1', 'left'),
      textMessage('question-2', 'right'),
      textMessage('question-2-continuation', 'right', 1),
      textMessage('answer-2', 'left'),
    ];
    const { result } = renderHook(() =>
      useConversationScrollController({
        conversationId: 'conversation-1',
        messages,
        itemCount: messages.length,
        lastUserMessageId: 'question-2',
        lastUserRowIndex: 2,
        scrollMessageIntoView,
        scrollToBottomItem: vi.fn(() => true),
      })
    );

    act(() => result.current.scrollToLastUser());
    expect(scrollMessageIntoView).toHaveBeenCalledWith('question-2', { behavior: 'smooth', block: 'start' });
  });

  it('reattaches immediately when this browser submits a new optimistic user turn', () => {
    const scrollToBottomItem = vi.fn(() => true);
    const initial = [textMessage('answer', 'left')];
    const { result, rerender } = renderHook(
      ({ messages }) =>
        useConversationScrollController({
          conversationId: 'conversation-1',
          messages,
          itemCount: messages.length,
          lastUserMessageId: messages.at(-1)?.id ?? null,
          lastUserRowIndex: messages.length - 1,
          scrollMessageIntoView: vi.fn(() => true),
          scrollToBottomItem,
        }),
      { initialProps: { messages: initial } }
    );

    act(() => result.current.handleUserScrollIntent());
    const optimistic = textMessage(`${SYNON_BIOMED_OPTIMISTIC_USER_MESSAGE_PREFIX}1`, 'right');
    rerender({ messages: [...initial, optimistic] });

    expect(scrollToBottomItem).toHaveBeenCalledWith('auto');
    expect(result.current.showScrollButton).toBe(false);
  });
});
