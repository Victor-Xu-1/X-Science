import type { TChatConversation } from '@/common/config/storage';

export type ConversationListSection = 'pinned' | 'recent';
export type ConversationSectionPage = {
  cursor: string | null;
  loading: boolean;
  error: boolean;
  request: number;
  retryCursor?: string;
  retryAppend: boolean;
};
export const emptyConversationSectionPage = (): ConversationSectionPage => ({
  cursor: null,
  loading: false,
  error: false,
  request: 0,
  retryAppend: false,
});

/** Merge into the existing list store, without a second cache or pin authority. */
export function mergeConversationSection(
  current: TChatConversation[],
  incoming: TChatConversation[],
  section: ConversationListSection,
  append: boolean
): TChatConversation[] {
  const retained = append ? current : current.filter((item) => Boolean(item.extra.pinned) !== (section === 'pinned'));
  const byId = new Map(retained.map((item) => [item.id, item]));
  for (const item of incoming) {
    const previous = byId.get(item.id);
    const oldIncarnation = (previous?.extra as Record<string, unknown> | undefined)?.frame_incarnation_id;
    const incarnation = (item.extra as Record<string, unknown>).frame_incarnation_id;
    const oldRevision = Number((previous?.extra as Record<string, unknown> | undefined)?.presentation_revision ?? 0);
    const revision = Number((item.extra as Record<string, unknown>)?.presentation_revision ?? 0);
    if (!previous || incarnation !== oldIncarnation || revision >= oldRevision) byId.set(item.id, item);
  }
  return [...byId.values()];
}
