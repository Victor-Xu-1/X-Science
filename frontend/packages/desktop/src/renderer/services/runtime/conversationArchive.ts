import { fromApiConversation } from '@/common/adapter/apiModelMapper';
import { httpRequest, isBackendHttpError } from '@/common/adapter/httpBridge';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken } from '../rendererAccountScope';
import { conversationPresentationAuthority, isConversationPresentationUnavailable } from './conversationPresentation';

export type ArchiveConversation = Extract<TChatConversation, { type: 'acp' }>;
export type ArchivedConversation = ArchiveConversation & {
  extra: ArchiveConversation['extra'] & { archived_at: number };
};

export type ConversationArchiveResult = {
  conversation: ArchiveConversation;
  changed: boolean;
  acknowledgement: 'confirmed' | 'reconciled';
};

export function isConversationArchived(conversation: TChatConversation): conversation is ArchivedConversation {
  if (conversation.type !== 'acp') return false;
  const value = conversation.extra.archived_at;
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

export function isConversationArchiveUnavailable(conversation: TChatConversation, isGenerating = false): boolean {
  const runtime = conversation.runtime;
  // Historical status alone must not strand a durably paused task. The server
  // still checks the complete execution tree atomically with the mutation.
  return (
    conversation.type !== 'acp' ||
    isConversationPresentationUnavailable(conversation) ||
    isGenerating ||
    Boolean(runtime?.is_processing) ||
    (runtime?.pending_confirmations ?? 0) > 0 ||
    ['starting', 'running', 'cancelling', 'waiting_approval', 'waiting_confirmation', 'waiting_input'].includes(
      runtime?.state ?? ''
    )
  );
}

function readArchiveConversation(
  raw: unknown,
  expected: TChatConversation,
  archived: boolean,
  revision: number
): ArchiveConversation {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('Invalid archive conversation');
  const value = raw as TChatConversation;
  if (
    value.id !== expected.id ||
    value.type !== 'acp' ||
    value.type !== expected.type ||
    !value.extra ||
    typeof value.extra !== 'object'
  )
    throw new Error('Archive identity changed');
  const authority = conversationPresentationAuthority(value);
  if (
    authority.expected_incarnation_id !== conversationPresentationAuthority(expected).expected_incarnation_id ||
    authority.expected_presentation_revision !== revision
  )
    throw new Error('Archive presentation changed');
  if (archived ? !isConversationArchived(value) : Object.hasOwn(value.extra, 'archived_at'))
    throw new Error('Archive state was not confirmed');
  return fromApiConversation(value);
}

export async function setConversationArchived(
  conversation: TChatConversation,
  archived: boolean
): Promise<ConversationArchiveResult> {
  if (conversation.type !== 'acp') throw new Error('Archive requires a real conversation');
  conversation = structuredClone(conversation);
  const authority = conversationPresentationAuthority(conversation);
  const scope = getRendererAccountScopeToken();
  const path = `/api/conversations/${encodeURIComponent(conversation.id)}`;
  const current = () => scope === getRendererAccountScopeToken();
  try {
    const raw = await httpRequest<unknown>(
      'POST',
      `${path}/archive`,
      { archived, ...authority },
      { timeoutMs: 15_000 }
    );
    if (!current()) throw new Error('Archive account changed');
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('Invalid archive response');
    const result = raw as Record<string, unknown>;
    if (typeof result.changed !== 'boolean') throw new Error('Archive acknowledgement is unavailable');
    return {
      conversation: readArchiveConversation(
        result.conversation,
        conversation,
        archived,
        authority.expected_presentation_revision + Number(result.changed)
      ),
      changed: result.changed,
      acknowledgement: 'confirmed',
    };
  } catch (error) {
    if (!current() || (isBackendHttpError(error) && error.status < 500)) throw error;
    // A lost reply may be reconciled read-only. It never authorizes retrying
    // the mutation with a refreshed revision, nor creates a fabricated Undo.
    const raw = await httpRequest<unknown>('GET', path, undefined, { timeoutMs: 15_000 }).catch((): null => null);
    if (!current() || !raw) throw error;
    const observedRevision = typeof raw === 'object' && (raw as ArchiveConversation).extra?.presentation_revision;
    const wasAlreadyDesired = isConversationArchived(conversation) === archived;
    const revision =
      wasAlreadyDesired && observedRevision === authority.expected_presentation_revision
        ? authority.expected_presentation_revision
        : authority.expected_presentation_revision + 1;
    return {
      conversation: readArchiveConversation(raw, conversation, archived, revision),
      changed: false,
      acknowledgement: 'reconciled',
    };
  }
}
