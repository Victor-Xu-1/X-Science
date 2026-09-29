import { describe, expect, it } from 'vitest';
import { decodeRoundSummary } from '@/common/chat/roundSummary';
import { decodeMessageStreamPayload } from '@/common/adapter/messageStreamProtocol';
import { preferTextMessageVersion, transformMessage, type IMessageText } from '@/common/chat/chatLib';
import { hasRenderableMessageText } from '@/renderer/pages/conversation/Messages/components/messageTextVisibility';
import { areMessageItemPropsEqual } from '@/renderer/pages/conversation/Messages/messageItemMemoModel';
import { applyRoundSummaryPublication } from '@/renderer/pages/conversation/Messages/roundSummaryProjection';

const summary = {
  attempt: 1,
  input_revision: 1,
  completed_at: 1790610800000,
  elapsed_ms: 12000,
  call_count: 1,
  reported_call_count: 1,
  usage_state: 'complete',
  tokens: { input: 20, cache_read: 80, cache_write: 0, output: 10, total: 110 },
  models: ['model'],
} as const;
const message = {
  id: 'answer',
  msg_id: 'answer',
  conversation_id: 'frame',
  type: 'text',
  position: 'left',
  content: { content: 'accepted text' },
  terminal_status: 'completed',
  round_summary: summary,
} as unknown as IMessageText;

describe('round summary transport', () => {
  it('applies a finish exactly once without replacing text, or creates an empty result footer', () => {
    const event = {
      type: 'finish',
      data: 'must not append',
      msg_id: 'answer',
      conversation_id: 'frame',
      terminal_status: 'completed',
      round_summary: summary,
    } as const;
    const list = [{ ...message, round_summary: undefined, terminal_status: undefined }];
    const once = applyRoundSummaryPublication(list, event as never);
    expect(once[0].content).toEqual({ content: 'accepted text' });
    expect(applyRoundSummaryPublication(once, event as never)).toEqual(once);
    expect(list[0].round_summary).toBeUndefined();
    const empty = applyRoundSummaryPublication([], { ...event, data: '' } as never);
    expect(hasRenderableMessageText(empty[0] as IMessageText)).toBe(true);
  });
  it('retains metadata across terminal decode, transformation and history/live reconciliation', () => {
    const decoded = decodeMessageStreamPayload({
      type: 'message.stream',
      stream_type: 'finish',
      data: '',
      msg_id: 'answer',
      conversation_id: 'frame',
      terminal_status: 'completed',
      round_summary: summary,
    });
    expect(decoded?.round_summary).toEqual(summary);
    const terminal = transformMessage({ ...decoded!, type: 'content' }) as IMessageText;
    const merged = preferTextMessageVersion(terminal, {
      ...message,
      terminal_status: undefined,
      round_summary: undefined,
    });
    expect(merged.content.content).toBe('accepted text');
    expect(merged.round_summary).toEqual(summary);
  });
  it.each([
    { elapsed_ms: -1 },
    { completed_at: 9e15 },
    { tokens: { ...summary.tokens, input: NaN } },
    { models: ['bad\nname'] },
    { reported_call_count: 9 },
    { usage_state: 'complete', tokens: null },
  ])('rejects invalid optional statistics without rejecting the answer: %j', (change) => {
    expect(decodeRoundSummary({ ...summary, ...change })).toBeUndefined();
    expect(
      decodeMessageStreamPayload({
        type: 'message.stream',
        stream_type: 'finish',
        data: 'answer',
        msg_id: 'answer',
        conversation_id: 'frame',
        terminal_status: 'completed',
        round_summary: { ...summary, ...change },
      })
    ).toMatchObject({ data: 'answer' });
  });
  it('keeps an empty completed answer visible only when it has a real summary', () => {
    expect(hasRenderableMessageText({ ...message, content: { content: '' } })).toBe(true);
    expect(hasRenderableMessageText({ ...message, content: { content: '' }, terminal_status: undefined })).toBe(false);
    expect(hasRenderableMessageText({ ...message, content: { content: '' }, terminal_superseded: true })).toBe(false);
  });
  it('invalidates row memoization when summary arrives', () => {
    expect(
      areMessageItemPropsEqual(
        { message: { ...message, round_summary: undefined }, rowWidthClass: '' },
        { message, rowWidthClass: '' }
      )
    ).toBe(false);
  });
});
