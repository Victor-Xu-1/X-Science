import { act, renderHook, waitFor } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { TMessage } from '@/common/chat/chatLib';
import { normalizeToolMessages, type ResourceAdmissionProgress } from '@/common/chat/normalizeToolCall';
import { buildToolProgressPublicPresentation } from '@/renderer/pages/conversation/Messages/components/toolProgressPresentation';
import {
  MessageListProvider,
  MessagePaginationProvider,
  useMergeLiveMessage,
  useMessageList,
  useMessagePaginationState,
} from '@/renderer/pages/conversation/Messages/hooks';

const wrapper = ({ children }: PropsWithChildren) => (
  <MessageListProvider value={[]}>
    <MessagePaginationProvider
      value={{
        oldestCursor: 'opaque-tail-oldest',
        newestCursor: 'opaque-tail-newest',
        hasMoreBefore: false,
        hasMoreAfter: false,
        isLoadingBefore: false,
        isLoadingAfter: false,
        isLoadingAnchor: false,
      }}
    >
      {children}
    </MessagePaginationProvider>
  </MessageListProvider>
);

describe('live message window', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('paints the first event immediately and batches the same-frame tail', () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    const { result } = renderHook(() => ({ merge: useMergeLiveMessage(), messages: useMessageList() }), { wrapper });

    act(() => {
      result.current.merge(
        {
          id: 'first',
          msg_id: 'first',
          conversation_id: 'conversation-live-window',
          type: 'text',
          position: 'left',
          status: 'pending',
          content: { content: '首字' },
        } as TMessage,
        true
      );
    });
    expect(result.current.messages.map((message) => message.id)).toEqual(['first']);

    act(() => {
      result.current.merge(
        {
          id: 'tail',
          msg_id: 'tail',
          conversation_id: 'conversation-live-window',
          type: 'text',
          position: 'left',
          status: 'pending',
          content: { content: '后续' },
        } as TMessage,
        true
      );
    });
    expect(result.current.messages.map((message) => message.id)).toEqual(['first']);
    act(() => frames.shift()?.(16));
    expect(result.current.messages.map((message) => message.id)).toEqual(['first', 'tail']);
  });

  it('batches streaming updates even when the previous paint was more than one frame ago', () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    const { result } = renderHook(() => ({ merge: useMergeLiveMessage(), messages: useMessageList() }), { wrapper });

    act(() => {
      result.current.merge({
        id: 'streaming-delta',
        msg_id: 'streaming-delta',
        conversation_id: 'conversation-live-window',
        type: 'text',
        position: 'left',
        status: 'pending',
        content: { content: '增量' },
      } as TMessage);
    });

    expect(result.current.messages).toEqual([]);
    expect(frames).toHaveLength(1);

    act(() => frames.shift()?.(16));
    expect(result.current.messages.map((message) => message.id)).toEqual(['streaming-delta']);
  });

  it('keeps every message already received in the active conversation', async () => {
    const { result } = renderHook(
      () => ({
        merge: useMergeLiveMessage(),
        messages: useMessageList(),
        pagination: useMessagePaginationState(),
      }),
      { wrapper }
    );

    act(() => {
      for (let index = 0; index < 205; index += 1) {
        result.current.merge(
          {
            id: `live-${index}`,
            msg_id: `live-${index}`,
            conversation_id: 'conversation-live-window',
            type: 'text',
            position: 'left',
            status: 'finish',
            content: { content: `message ${index}` },
          } as TMessage,
          true
        );
      }
    });

    await waitFor(() => expect(result.current.messages).toHaveLength(205));
    expect(result.current.messages[0]?.id).toBe('live-0');
    expect(result.current.messages.at(-1)?.id).toBe('live-204');
    expect(result.current.pagination.oldestCursor).toBe('opaque-tail-oldest');
    expect(result.current.pagination.hasMoreBefore).toBe(false);
  });

  it('settles a live tool in place and removes auxiliary streaming state', () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    const { result } = renderHook(() => ({ merge: useMergeLiveMessage(), messages: useMessageList() }), { wrapper });

    act(() => {
      result.current.merge(
        {
          id: 'tool-start-event',
          msg_id: 'tool-start-event',
          conversation_id: 'conversation-live-window',
          type: 'tool_call',
          position: 'left',
          status: 'work',
          hidden: true,
          content: {
            call_id: 'tool-call-1',
            operation_id: 'tool-operation-1',
            attempt: 1,
            name: 'python',
            status: 'running',
            output: 'partial',
            streaming: true,
            streamingRecoveryError: true,
          },
        } as TMessage,
        true
      );
    });
    expect(result.current.messages).toHaveLength(1);

    act(() => {
      result.current.merge({
        id: 'tool-terminal-event',
        msg_id: 'tool-terminal-event',
        conversation_id: 'conversation-live-window',
        type: 'tool_call',
        position: 'left',
        status: 'finish',
        content: {
          call_id: 'tool-call-1',
          operation_id: 'tool-operation-1',
          attempt: 1,
          name: 'python',
          status: 'completed',
          output: 'final',
        },
      } as TMessage);
    });
    act(() => frames.shift()?.(16));

    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0]).toMatchObject({
      id: 'tool-start-event',
      hidden: false,
      content: { status: 'completed', output: 'final' },
    });
    expect(result.current.messages[0]).not.toHaveProperty('content.streaming');
    expect(result.current.messages[0]).not.toHaveProperty('content.streamingRecoveryError');
  });

  it('keeps the first terminal tool receipt authoritative over late lifecycle updates', async () => {
    const { result } = renderHook(() => ({ merge: useMergeLiveMessage(), messages: useMessageList() }), { wrapper });

    for (const [index, content] of [
      { status: 'running', output: 'partial', streaming: true },
      { status: 'error', output: 'first terminal evidence' },
      { status: 'completed', output: 'conflicting late success' },
      { status: 'running', output: 'late retry fragment', streaming: true },
    ].entries()) {
      act(() => {
        result.current.merge({
          id: `tool-receipt-${index}`,
          msg_id: `tool-receipt-${index}`,
          conversation_id: 'conversation-live-window',
          type: 'tool_call',
          position: 'left',
          status: content.status === 'running' ? 'work' : 'finish',
          hidden: content.status === 'running',
          content: {
            call_id: 'tool-call-authoritative',
            operation_id: 'tool-operation-authoritative',
            attempt: 1,
            name: 'python',
            ...content,
          },
        } as TMessage);
      });
    }

    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    expect(result.current.messages[0]).toMatchObject({
      id: 'tool-receipt-0',
      msg_id: 'tool-receipt-0',
      hidden: false,
      content: {
        status: 'error',
        output: 'first terminal evidence',
      },
    });
    expect(result.current.messages[0]).not.toHaveProperty('content.streaming');
  });

  it('settles the unique active operation across a recovered runner attempt without adding a row', async () => {
    const { result } = renderHook(() => ({ merge: useMergeLiveMessage(), messages: useMessageList() }), { wrapper });

    act(() => {
      result.current.merge({
        id: 'tool-waiting-event',
        msg_id: 'tool-waiting-event',
        conversation_id: 'conversation-live-window',
        type: 'tool_call',
        position: 'left',
        status: 'work',
        content: {
          call_id: 'tool-call-recovered',
          operation_id: 'tool-operation-recovered',
          attempt: 1,
          name: 'python',
          input: { code: 'print(42)' },
          status: 'waiting',
        },
      } as TMessage);
    });
    act(() => {
      result.current.merge({
        id: 'tool-completed-event',
        msg_id: 'tool-completed-event',
        conversation_id: 'conversation-live-window',
        type: 'tool_call',
        position: 'left',
        status: 'finish',
        content: {
          call_id: 'tool-call-recovered',
          operation_id: 'tool-operation-recovered',
          attempt: 2,
          revision: 9,
          name: 'python',
          status: 'completed',
          output: '42',
        },
      } as TMessage);
    });

    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    expect(result.current.messages[0]).toMatchObject({
      id: 'tool-waiting-event',
      msg_id: 'tool-waiting-event',
      content: {
        attempt: 1,
        revision: 9,
        input: { code: 'print(42)' },
        status: 'completed',
        output: '42',
      },
    });
  });
});

describe('live resource admission progress', () => {
  type ToolMessage = Extract<TMessage, { type: 'tool_call' }>;
  const snapshot = (): ResourceAdmissionProgress => ({
    decisionAt: '2026-01-02T03:04:05Z',
    reason: 'insufficient_capacity',
    capacityAvailableBytes: 900,
    unusedPromisedBytes: 600,
    netAvailableBytes: 300,
    requestedBytes: 400,
    requiredBytes: 400,
    reservedBytes: 0,
    precedingWaiters: 0,
  });
  const packet = (revision: number | undefined, phase: string, admission?: ResourceAdmissionProgress): ToolMessage => ({
    // Every update deliberately has the same canonical message identity.
    id: 'resource-live-message',
    msg_id: 'resource-live-message',
    conversation_id: 'resource-live-conversation',
    type: 'tool_call',
    position: 'left',
    status: 'work',
    content: {
      call_id: 'resource-live-call',
      operation_id: 'resource-live-operation',
      attempt: 1,
      name: 'bash',
      args: { purpose: 'synthetic resource observation' },
      input: { purpose: 'synthetic resource observation' },
      status: 'running',
      ...(revision === undefined ? {} : { revision }),
      xScience: { messageIndex: 0, blockIndex: 0, branchId: 'br_00000001' },
      progress: { phase, ...(admission ? { resourceAdmission: admission } : {}) },
    },
  });
  const setup = () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    const { result } = renderHook(() => ({ merge: useMergeLiveMessage(), messages: useMessageList() }), { wrapper });
    const deliver = (message: ToolMessage, add = false) => {
      act(() => result.current.merge(message, add));
      if (!add) act(() => frames.shift()?.(16));
    };
    const tools = () =>
      normalizeToolMessages(
        result.current.messages.filter((message): message is ToolMessage => message.type === 'tool_call')
      );
    return { result, deliver, tools };
  };
  afterEach(() => vi.restoreAllMocks());

  it('retains the omitted DTO in the real same-ID upsert, then fences an unseen older revision', () => {
    const { result, deliver, tools } = setup();
    deliver(packet(1, 'waiting_for_resources', snapshot()), true);
    deliver(packet(2, 'waiting_for_resources'));

    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0]).toMatchObject({
      content: { revision: 2, progress: { resourceAdmission: snapshot() } },
    });
    const [waiting] = tools();
    expect(buildToolProgressPublicPresentation(waiting.progress!, 'en-US').rows).toContainEqual({
      label: 'Required / unreserved at decision',
      value: '400 B / 300 B',
    });
    expect(buildToolProgressPublicPresentation(waiting.progress!, 'zh-CN').rows).toContainEqual({
      label: '当次准入快照（非实时）',
      value: '2026-01-02 03:04:05 UTC',
    });

    deliver(packet(4, 'executing'));
    const executing = result.current.messages[0];
    // This revision was not previously delivered, so transport dedup alone cannot fence it.
    deliver(packet(3, 'waiting_for_resources', snapshot()));
    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0]).toBe(executing);
    expect(result.current.messages[0]).toMatchObject({ content: { revision: 4, progress: { phase: 'executing' } } });
    expect(tools()[0].progress?.resourceAdmission).toBeUndefined();
    expect(JSON.stringify(buildToolProgressPublicPresentation(tools()[0].progress!, 'en-US'))).not.toContain(
      'Admission snapshot'
    );
  });

  it('clears resource facts at the actual terminal upsert and preserves its output over later activity', () => {
    const { result, deliver, tools } = setup();
    const first = packet(1, 'waiting_for_resources', snapshot());
    first.content.streaming = true;
    first.content.streamingRecoveryError = true;
    deliver(first, true);
    const terminal = packet(3, 'operation_completed');
    terminal.status = 'finish';
    terminal.content.status = 'completed';
    terminal.content.output = 'synthetic terminal evidence';
    deliver(terminal);
    deliver(packet(4, 'waiting_for_resources', snapshot()));

    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0]).toMatchObject({
      content: { status: 'completed', output: 'synthetic terminal evidence' },
    });
    expect(result.current.messages[0]).not.toHaveProperty('content.streaming');
    expect(result.current.messages[0]).not.toHaveProperty('content.streamingRecoveryError');
    expect(tools()[0].status).toBe('completed');
    expect(tools()[0].progress?.resourceAdmission).toBeUndefined();
  });

  it.each(['branch', 'conversation', 'attempt', 'input'] as const)(
    'does not lend a snapshot across a changed %s owner',
    (boundary) => {
      const { result, deliver, tools } = setup();
      deliver(packet(1, 'waiting_for_resources', snapshot()), true);
      const other = packet(2, 'waiting_for_resources');
      if (boundary === 'branch') other.content.xScience = { messageIndex: 0, blockIndex: 0, branchId: 'br_00000002' };
      if (boundary === 'conversation') other.conversation_id = 'other-resource-conversation';
      if (boundary === 'attempt') other.content.attempt = 2;
      if (boundary === 'input') other.content.input = { purpose: 'different synthetic input' };
      deliver(other);

      expect(result.current.messages).toHaveLength(boundary === 'input' ? 1 : 2);
      expect(tools().at(-1)?.progress?.resourceAdmission).toBeUndefined();
    }
  );

  it('keeps legacy no-revision updates compatible without manufacturing a determinate flag', () => {
    const { result, deliver, tools } = setup();
    deliver(packet(undefined, 'waiting_for_resources', snapshot()), true);
    deliver(packet(undefined, 'waiting_for_resources'));
    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0]).not.toHaveProperty('content.revision');
    expect(result.current.messages[0]).not.toHaveProperty('content.progress.indeterminate');
    expect(tools()[0].progress?.resourceAdmission).toEqual(snapshot());
    expect(tools()[0].status).toBe('running');
  });
});
