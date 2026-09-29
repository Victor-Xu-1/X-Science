import type { TMessage } from '@/common/chat/chatLib';
import { loadConversationMessagePage } from '@/renderer/utils/chat/messagePagination';
import { getSynonBiomedBranchSelectionRevision } from '@/renderer/services/synonBiomedConversationBranches';
import { buildTurnPreview } from './minimapUtils';
import type { TurnPreviewItem } from './minimapTypes';

export type ConversationTurnIndex = { items: TurnPreviewItem[]; branchId?: string };

/** The rail and title search use the same branch-fenced, paged projection. */
export async function loadConversationTurnIndex(
  conversationId: string,
  options: { signal?: AbortSignal; fullText?: boolean } = {}
): Promise<ConversationTurnIndex> {
  const revision = getSynonBiomedBranchSelectionRevision(conversationId);
  const pages: TMessage[][] = [];
  const cursors = new Set<string>();
  let before: string | undefined;
  let branchId: string | undefined;
  do {
    options.signal?.throwIfAborted();
    // Cursor pagination is intentionally sequential: the next cursor is returned by this request.
    // eslint-disable-next-line no-await-in-loop
    const page = await loadConversationMessagePage(conversationId, {
      limit: 200,
      before,
      branchId,
      signal: options.signal,
      contentMode: options.fullText ? 'full' : 'compact',
    });
    options.signal?.throwIfAborted();
    if (revision !== getSynonBiomedBranchSelectionRevision(conversationId)) {
      throw new Error('conversation_branch_selection_changed');
    }
    branchId = page.branch_id;
    // Keep source identity, not large tool results, while building the index.
    pages.push(
      page.items
        .filter((message) => !message.hidden)
        .map((message) => ({
          id: message.id,
          msg_id: message.msg_id,
          conversation_id: conversationId,
          position: message.type === 'text' ? message.position : 'center',
          type: 'text',
          content:
            message.type === 'text'
              ? {
                  ...message.content,
                  content: options.fullText ? message.content.content : message.content.content.slice(0, 512),
                }
              : { content: '' },
        }))
    );
    if (!page.has_more_before) break;
    before = page.oldest_cursor ?? undefined;
    if (!before || cursors.has(before)) throw new Error('conversation_history_cursor_not_advancing');
    cursors.add(before);
  } while (before);
  return { items: buildTurnPreview(pages.toReversed().flat()), branchId };
}

export function mergeLiveTurnPreviews(history: TurnPreviewItem[], messages: TMessage[]): TurnPreviewItem[] {
  const live = buildTurnPreview(
    messages.map((message) =>
      message.type === 'text'
        ? { ...message, content: { ...message.content, content: message.content.content.slice(0, 512) } }
        : message
    )
  );
  const merged = history.slice();
  const positions = new Map(history.map((item, index) => [item.messageId, index]));
  for (const item of live) {
    const position = positions.get(item.messageId);
    if (position === undefined) {
      merged.push({ ...item, index: merged.length + 1 });
    } else {
      const previous = merged[position];
      const next = {
        ...previous,
        ...item,
        index: previous.index,
        answer: item.answer || previous.answer,
        answerRaw: item.answerRaw || previous.answerRaw,
        messageIds: [...new Set([...previous.messageIds, ...item.messageIds])],
      };
      if (
        next.question !== previous.question ||
        next.questionRaw !== previous.questionRaw ||
        next.answer !== previous.answer ||
        next.answerRaw !== previous.answerRaw ||
        next.msgId !== previous.msgId ||
        next.messageIds.length !== previous.messageIds.length ||
        next.messageIds.some((id, index) => id !== previous.messageIds[index])
      )
        merged[position] = next;
    }
  }
  return merged.length === history.length && merged.every((item, index) => item === history[index]) ? history : merged;
}
