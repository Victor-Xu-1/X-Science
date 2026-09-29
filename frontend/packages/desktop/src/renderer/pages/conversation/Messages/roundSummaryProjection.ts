import { transformMessage, type TMessage } from '@/common/chat/chatLib';
import type { IResponseMessage } from '@/common/adapter/messageStreamProtocol';
import { decodeRoundSummary } from '@/common/chat/roundSummary';

/** Terminal metadata updates accepted text in place; it is never another delta. */
export function applyRoundSummaryPublication(messages: TMessage[], event: IResponseMessage): TMessage[] {
  const summary = decodeRoundSummary(event.round_summary);
  if (event.terminal_status !== 'completed' || !summary) return messages;
  const index = messages.findIndex((item) => item.type === 'text' && item.msg_id === event.msg_id);
  if (index < 0) {
    const terminal = transformMessage({
      ...event,
      type: 'content',
      data: typeof event.data === 'string' ? event.data : '',
      status: 'finish',
    });
    return terminal ? [...messages, terminal] : messages;
  }
  const next = messages.slice();
  next[index] = { ...messages[index], status: 'finish', terminal_status: 'completed', round_summary: summary };
  return next;
}
