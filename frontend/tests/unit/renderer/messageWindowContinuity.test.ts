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
  history_coverage_through: 1000,
  content: { content, assistantAttemptId: 'attempt-a' },
});

describe('long-task published message continuity', () => {
  it('keeps one richer live row across repeated durable prefix refreshes', () => {
    const snapshot = { ...text('durable-segment', 'Observed result.', 'attempt-a'), status: 'work' as const };
    const live = {
      ...snapshot,
      id: 'live-attempt',
      history_coverage_through: undefined,
      content: { ...snapshot.content, content: 'Observed result. Still investigating.' },
    };
    let current: IMessageText[] = [live];
    for (let index = 0; index < 5; index++) {
      current = mergeLoadedPageWithCurrent('long-task', [snapshot], current, true) as IMessageText[];
    }
    expect(current).toHaveLength(1);
    expect(current[0].id).toBe(snapshot.id);
    expect(current[0].content.content).toBe(live.content.content);
    expect(current[0].history_coverage_through).toBe(snapshot.history_coverage_through);
    expect(prependHistoryMessages(current, [snapshot])).toEqual(current);
    expect(appendHistoryMessages(current, [snapshot])).toEqual(current);
  });

  it('keeps distinct durable segments even when their text shares a prefix', () => {
    const first = text('segment-1', 'Observed result.', 'attempt-a');
    const second = text('segment-2', 'Observed result. Now verifying.', 'attempt-a');
    expect(prependHistoryMessages([second], [first])).toEqual([first, second]);
    expect(appendHistoryMessages([first], [second])).toEqual([first, second]);
    expect(mergeLoadedPageWithCurrent('long-task', [second], [first, second], true)).toEqual([first, second]);
  });

  it('never consumes two live rows while resolving one recovered history row', () => {
    const first = { ...text('live-1', 'Observed result.', 'attempt-a'), history_coverage_through: undefined };
    const second = {
      ...text('live-2', 'Observed result. Next operation.', 'attempt-a'),
      history_coverage_through: undefined,
    };
    const history = text('history-1', 'Observed result.', 'attempt-a');
    const merged = mergeLoadedPageWithCurrent('long-task', [history], [first, second], true);
    expect(merged).toContainEqual(second);
    expect(
      merged.filter((message) => message.type === 'text' && message.content.content === first.content.content)
    ).toHaveLength(1);
  });

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
