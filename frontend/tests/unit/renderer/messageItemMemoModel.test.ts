/**
 * @vitest-environment node
 */

import type { IMessageText } from '@/common/chat/chatLib';
import {
  areMessageAgentStatusPropsEqual,
  areMessageItemPropsEqual,
  areMessageTipsPropsEqual,
  areMessageToolCallPropsEqual,
} from '@/renderer/pages/conversation/Messages/messageItemMemoModel';
import { describe, expect, it } from 'vitest';

const message: IMessageText = {
  id: 'assistant-1',
  msg_id: 'assistant-1',
  conversation_id: 'conversation-1',
  type: 'text',
  position: 'left',
  status: 'work',
  content: { content: 'partial scientific response' },
};

const props = {
  message,
  rowWidthClass: 'chat-surface-fluid',
};

describe('areMessageItemPropsEqual', () => {
  it('invalidates a stable row when its durable terminal state changes', () => {
    expect(
      areMessageItemPropsEqual(props, {
        ...props,
        message: { ...message, status: 'error', terminal_status: 'failed' },
      })
    ).toBe(false);
  });

  it('keeps an unchanged row memoized', () => {
    expect(areMessageItemPropsEqual(props, { ...props })).toBe(true);
  });
});

describe('leaf message memo comparators', () => {
  it('skips tool and tip rerenders when only the parent highlight state would change', () => {
    const tool = {
      id: 'tool-1',
      msg_id: 'tool-1',
      conversation_id: 'conversation-1',
      type: 'tool_call' as const,
      position: 'left' as const,
      status: 'work' as const,
      content: { name: 'read', call_id: 'call-1', status: 'running' as const },
    };
    expect(areMessageToolCallPropsEqual({ message: tool }, { message: { ...tool } })).toBe(true);

    const tip = {
      id: 'tip-1',
      msg_id: 'tip-1',
      conversation_id: 'conversation-1',
      type: 'tips' as const,
      position: 'center' as const,
      status: 'finish' as const,
      content: { content: 'note', type: 'warning' as const },
    };
    expect(areMessageTipsPropsEqual({ message: tip }, { message: { ...tip } })).toBe(true);

    const status = {
      id: 'status-1',
      msg_id: 'status-1',
      conversation_id: 'conversation-1',
      type: 'agent_status' as const,
      position: 'center' as const,
      status: 'finish' as const,
      content: { backend: 'synonbiomed', status: 'connected' as const },
    };
    expect(areMessageAgentStatusPropsEqual({ message: status }, { message: { ...status } })).toBe(true);
    expect(
      areMessageAgentStatusPropsEqual(
        { message: status },
        { message: { ...status, content: { ...status.content, status: 'session_active' } } }
      )
    ).toBe(false);
  });
});
