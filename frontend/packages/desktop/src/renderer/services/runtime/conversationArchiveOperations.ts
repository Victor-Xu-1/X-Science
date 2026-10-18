import type { TChatConversation } from '@/common/config/storage';
import { emitter } from '@/renderer/utils/emitter';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '../rendererAccountScope';
import { setConversationArchived, type ConversationArchiveResult } from './conversationArchive';
import { conversationPresentationAuthority } from './conversationPresentation';

export const ARCHIVE_UNDO_DURATION_MS = 8_000;
export type ArchiveUndoNotice = { key: string; conversation: TChatConversation; expiresAt: number; failed: boolean };
type Operation = { archived: boolean; revision: number; promise: Promise<ConversationArchiveResult> };
type ArchiveOperationsSnapshot = {
  pending: ReadonlySet<string>;
  undoing: ReadonlySet<string>;
  notices: readonly ArchiveUndoNotice[];
};
const operations = new Map<string, Operation>();
const undoing = new Set<string>();
const listeners = new Set<() => void>();
let snapshot: ArchiveOperationsSnapshot = { pending: new Set(), undoing: new Set(), notices: [] };
const notify = () => {
  for (const listener of listeners) listener();
};
export const getArchiveOperationsSnapshot = () => snapshot;
export const subscribeArchiveOperations = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
const keyFor = (conversation: TChatConversation) =>
  JSON.stringify([
    getRendererAccountScopeToken(),
    conversation.id,
    conversationPresentationAuthority(conversation).expected_incarnation_id,
  ]);

function update(notices = snapshot.notices) {
  snapshot = {
    pending: new Set(operations.keys()),
    undoing: new Set(undoing),
    notices: notices.filter((notice) => notice.expiresAt > Date.now() || undoing.has(notice.key)),
  };
  notify();
}

registerRendererAccountReset('conversation-archive-operations', () => {
  operations.clear();
  undoing.clear();
  update([]);
});

export function isArchiveOperationPending(conversation: TChatConversation, state = snapshot): boolean {
  try {
    return state.pending.has(keyFor(conversation));
  } catch {
    return false;
  }
}

/** Shared across sidebar/settings consumers; never a second conversation list. */
export function runConversationArchive(
  conversation: TChatConversation,
  archived: boolean
): Promise<ConversationArchiveResult> {
  const key = keyFor(conversation);
  const revision = conversationPresentationAuthority(conversation).expected_presentation_revision;
  const active = operations.get(key);
  if (active) {
    if (active.archived === archived && active.revision === revision) return active.promise;
    return Promise.reject(new Error('An archive operation is already pending'));
  }
  const scope = getRendererAccountScopeToken();
  const frozen = structuredClone(conversation);
  const promise = Promise.resolve()
    .then(() => {
      if (scope !== getRendererAccountScopeToken()) throw new Error('Archive account changed');
      return setConversationArchived(frozen, archived);
    })
    .then((result) => {
      if (scope !== getRendererAccountScopeToken()) throw new Error('Archive account changed');
      const notices = snapshot.notices.filter((notice) => notice.conversation.id !== conversation.id);
      if (archived && result.changed && result.acknowledgement === 'confirmed') {
        const canonical = structuredClone(result.conversation);
        notices.unshift({
          key: `${key}:${canonical.extra.presentation_revision}`,
          conversation: canonical,
          expiresAt: Date.now() + ARCHIVE_UNDO_DURATION_MS,
          failed: false,
        });
      }
      update(notices.slice(0, 16));
      emitter.emit('chat.history.refresh');
      emitter.emit('xscience.projects.refresh');
      return result;
    })
    .finally(() => {
      if (operations.get(key)?.promise === promise) operations.delete(key);
      if (scope === getRendererAccountScopeToken()) update();
    });
  operations.set(key, { archived, revision, promise });
  update();
  return promise;
}

export function dismissArchiveUndo(key: string): void {
  update(snapshot.notices.filter((notice) => notice.key !== key));
}

export function expireArchiveUndo(): void {
  update();
}

export async function undoConversationArchive(key: string): Promise<void> {
  const notice = snapshot.notices.find((item) => item.key === key && item.expiresAt > Date.now());
  if (!notice || undoing.has(key)) return;
  const scope = getRendererAccountScopeToken();
  undoing.add(key);
  update();
  try {
    await runConversationArchive(notice.conversation, false);
    if (scope === getRendererAccountScopeToken()) dismissArchiveUndo(key);
  } catch (error) {
    if (scope === getRendererAccountScopeToken())
      update(
        snapshot.notices.map((item) =>
          item.key === key
            ? // Reference behavior keeps a failed Undo visible for an explicit retry,
              // but retains its original incarnation/revision; it never grants a new CAS.
              { ...item, failed: true, expiresAt: Date.now() + ARCHIVE_UNDO_DURATION_MS }
            : item
        )
      );
    throw error;
  } finally {
    undoing.delete(key);
    if (scope === getRendererAccountScopeToken()) update();
  }
}
