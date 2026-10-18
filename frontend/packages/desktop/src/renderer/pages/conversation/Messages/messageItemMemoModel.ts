import type { IMessageText, TMessage } from '@/common/chat/chatLib';

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
  prev.message.msg_id === next.message.msg_id &&
  prev.message.conversation_id === next.message.conversation_id &&
  prev.message.content === next.message.content &&
  prev.message.artifact_refs === next.message.artifact_refs &&
  prev.message.pdf_reading_context === next.message.pdf_reading_context &&
  prev.message.pdf_reading_context_unavailable === next.message.pdf_reading_context_unavailable &&
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

export type MessageTextMemoProps = {
  message: IMessageText;
  messageIndex?: number;
  showCopyRow?: boolean;
};

/** MessageText renders artifact links from refs that can arrive after content. */
export const areMessageTextPropsEqual = (prev: MessageTextMemoProps, next: MessageTextMemoProps): boolean =>
  prev.showCopyRow === next.showCopyRow &&
  prev.messageIndex === next.messageIndex &&
  (prev.message === next.message ||
    (prev.message.id === next.message.id &&
      prev.message.msg_id === next.message.msg_id &&
      prev.message.conversation_id === next.message.conversation_id &&
      prev.message.content === next.message.content &&
      prev.message.artifact_refs === next.message.artifact_refs &&
      prev.message.pdf_reading_context === next.message.pdf_reading_context &&
      prev.message.pdf_reading_context_unavailable === next.message.pdf_reading_context_unavailable &&
      prev.message.position === next.message.position &&
      prev.message.status === next.message.status &&
      prev.message.terminal_status === next.message.terminal_status &&
      prev.message.terminal_superseded === next.message.terminal_superseded &&
      prev.message.round_summary === next.message.round_summary));
