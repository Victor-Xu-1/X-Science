import { describe, expect, it } from 'vitest';
import type { IMessageText } from '@/common/chat/chatLib';
import {
  appendHistoryMessages,
  mergeLoadedPageWithCurrent,
  prependHistoryMessages,
} from '@/renderer/pages/conversation/Messages/messageWindowReconciliation';

const text = (id: string, content: string, msgId = id): IMessageText => ({
  id,
  msg_id: msgId,
  conversation_id: 'long-task',
  type: 'text',
  position: 'left',
  status: 'finish',
  content: { content, assistantAttemptId: 'attempt-a' },
});

describe('long-task published message continuity', () => {
  it('keeps distinct text segments across both pagination directions', () => {
    const first = text('segment-1', 'First observed finding.', 'attempt-a');
    const second = text('segment-2', 'Second observed finding.', 'attempt-a');
    expect(prependHistoryMessages([second], [first, second])).toEqual([first, second]);
    expect(appendHistoryMessages([first], [first, second])).toEqual([first, second]);
  });

  it('keeps previously published content and order during repeated bounded tail refreshes', () => {
    const original = Array.from({ length: 1000 }, (_, index) => text(`message-${index}`, `Published finding ${index}`));
    let current = original;
    for (let i = 0; i < 5; i++) {
      current = mergeLoadedPageWithCurrent('long-task', original.slice(-20), current, true) as IMessageText[];
    }
    expect(current).toEqual(original);
  });

  it('places newly recovered text between its existing history neighbors', () => {
    const first = text('first', 'First finding.');
    const missing = text('missing', 'Finding recovered from persistent history.');
    const last = text('last', 'Later finding.');
    expect(mergeLoadedPageWithCurrent('long-task', [first, missing, last], [first, last], true, true)).toEqual([
      first,
      missing,
      last,
    ]);
  });
});
