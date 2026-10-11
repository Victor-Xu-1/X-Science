/**
 * @vitest-environment node
 */

import type { IMessageText } from '@/common/chat/chatLib';
import {
  areMessageItemPropsEqual,
  areMessageTextPropsEqual,
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

  it('invalidates a stable row when artifact references arrive after content', () => {
    const next = {
      ...message,
      artifact_refs: [
        {
          artifact_id: 'artifact-1',
          version_id: 'version-1',
          relation: 'produced' as const,
          availability: 'available' as const,
        },
      ],
    };
    expect(areMessageItemPropsEqual(props, { ...props, message: next })).toBe(false);
    expect(areMessageTextPropsEqual({ message, showCopyRow: true }, { message: next, showCopyRow: true })).toBe(false);
  });

  it('keeps MessageText memoized when only unused sibling fields change', () => {
    expect(
      areMessageTextPropsEqual(
        { message, showCopyRow: true, messageIndex: 2 },
        { message: { ...message, type: 'text' }, showCopyRow: true, messageIndex: 2 }
      )
    ).toBe(true);
  });
});
