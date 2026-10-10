import type { IMessageAgentStatus, IMessageTips, IMessageToolCall, TMessage } from '@/common/chat/chatLib';

/** Shared fields for memoized leaf renderers under a memoized MessageItem row. */
const areSameTranscriptMessageSnapshot = (prev: TMessage, next: TMessage): boolean =>
  prev.id === next.id &&
  prev.msg_id === next.msg_id &&
  prev.conversation_id === next.conversation_id &&
  prev.content === next.content &&
  prev.artifact_refs === next.artifact_refs &&
  prev.status === next.status &&
  prev.terminal_status === next.terminal_status &&
  prev.terminal_superseded === next.terminal_superseded &&
  prev.round_summary === next.round_summary;

export type MessageItemMemoProps = {
  message: TMessage;
  branchState?: unknown;
  highlighted?: boolean;
  rowWidthClass: string;
  showCopyRow?: boolean;
  messageIndex?: number;
  isLastUserMessage?: boolean;
  showReadCursorDivider?: boolean;
  readCursorDividerFaded?: boolean;
  readCursorAgo?: string | null;
};

export const areMessageItemPropsEqual = (prev: MessageItemMemoProps, next: MessageItemMemoProps): boolean =>
  prev.message.id === next.message.id &&
  prev.message.content === next.message.content &&
  prev.message.position === next.message.position &&
  prev.message.type === next.message.type &&
  prev.message.status === next.message.status &&
  prev.message.terminal_status === next.message.terminal_status &&
  prev.message.terminal_superseded === next.message.terminal_superseded &&
  prev.message.round_summary === next.message.round_summary &&
  prev.branchState === next.branchState &&
  prev.highlighted === next.highlighted &&
  prev.rowWidthClass === next.rowWidthClass &&
  prev.showCopyRow === next.showCopyRow &&
  prev.messageIndex === next.messageIndex &&
  prev.isLastUserMessage === next.isLastUserMessage &&
  prev.showReadCursorDivider === next.showReadCursorDivider &&
  prev.readCursorDividerFaded === next.readCursorDividerFaded &&
  prev.readCursorAgo === next.readCursorAgo;

export const areMessageToolCallPropsEqual = (
  prev: { message: IMessageToolCall },
  next: { message: IMessageToolCall }
): boolean => prev.message === next.message || areSameTranscriptMessageSnapshot(prev.message, next.message);

export const areMessageAgentStatusPropsEqual = (
  prev: { message: IMessageAgentStatus },
  next: { message: IMessageAgentStatus }
): boolean => prev.message === next.message || areSameTranscriptMessageSnapshot(prev.message, next.message);

export const areMessageTipsPropsEqual = (
  prev: { message: IMessageTips },
  next: { message: IMessageTips }
): boolean => prev.message === next.message || areSameTranscriptMessageSnapshot(prev.message, next.message);
