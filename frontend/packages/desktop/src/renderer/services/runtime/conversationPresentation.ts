import { httpRequest } from '@/common/adapter/httpBridge';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

type PresentationAuthority = { expected_incarnation_id: string; expected_presentation_revision: number };

export function conversationPresentationAuthority(conversation: TChatConversation): PresentationAuthority {
  const extra = conversation.extra as Record<string, unknown>;
  const incarnation = extra.frame_incarnation_id;
  const revision = extra.presentation_revision;
  if (
    typeof incarnation !== 'string' ||
    !incarnation ||
    typeof revision !== 'number' ||
    !Number.isSafeInteger(revision) ||
    revision < 0
  )
    throw new Error('Conversation presentation authority is unavailable');
  return { expected_incarnation_id: incarnation, expected_presentation_revision: revision };
}

/** The frozen desired state and CAS are never replaced after a conflict. */
export async function setConversationPinned(conversation: TChatConversation, pinned: boolean): Promise<void> {
  const authority = conversationPresentationAuthority(conversation);
  const scope = getRendererAccountScopeToken();
  const path = `/api/conversations/${encodeURIComponent(conversation.id)}`;
  try {
    if ((await httpRequest<boolean>('PATCH', path, { extra: { pinned }, merge_extra: true, ...authority })) !== true)
      throw new Error('Conversation pin was not accepted');
  } catch (error) {
    if (scope !== getRendererAccountScopeToken()) throw error;
    // Read-only reconciliation covers a lost response; it never submits a
    // second mutation with a newer revision or an inverted desired state.
    const current = await httpRequest<TChatConversation>('GET', path).catch((): null => null);
    if (scope !== getRendererAccountScopeToken() || !current) throw error;
    const extra = current.extra as Record<string, unknown>;
    if (extra.frame_incarnation_id !== authority.expected_incarnation_id || extra.pinned !== pinned) throw error;
  }
}

export async function reorderPinnedConversations(conversations: TChatConversation[]): Promise<void> {
  if (!conversations.length || conversations.length > 1000) throw new Error('Pinned order exceeds the supported batch');
  const pinned_order = conversations.map((conversation, index) => ({
    id: conversation.id,
    ...conversationPresentationAuthority(conversation),
    sortOrder: (index + 1) * 1000,
  }));
  if (
    (await httpRequest<boolean>('PATCH', `/api/conversations/${encodeURIComponent(conversations[0].id)}`, {
      pinned_order,
    })) !== true
  )
    throw new Error('Pinned order was not accepted');
}
