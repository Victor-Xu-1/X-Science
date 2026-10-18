import { useCallback, useEffect } from 'react';
import useSWR from 'swr';
import {
  loadSendBoxDraft,
  readSendBoxDraftForMutation,
  saveSendBoxDraft,
  sendBoxDraftStorageKey,
  type AcpSendBoxDraft,
} from './sendBoxDraftPersistence';
import {
  changedOrdinaryDraftFields,
  ordinaryDraftValues,
  sameDraftSnapshot,
  type OrdinaryDraftEdits,
} from './sendBoxDraftEdits';
export type { FileOrFolderItem } from '@/renderer/utils/file/fileTypes';

type Draft = AcpSendBoxDraft;
type DraftConversationType = Draft['_type'];
type DraftRecord = {
  draft: Draft;
  // Unsaved ordinary edits belong to this existing owner. They are overlaid
  // for display, never passed off as a durable pending-mutation receipt.
  unsaved?: OrdinaryDraftEdits;
  saveFailed?: boolean;
};
const store: Record<DraftConversationType, Map<string, DraftRecord>> = { acp: new Map() };

const draftIdentity = (ownerId: string, conversationId: string): string =>
  sendBoxDraftStorageKey(ownerId, conversationId) ?? `${ownerId.trim() || 'local'}:${conversationId}`;

function getRecord(type: DraftConversationType, ownerId: string, conversationId: string): DraftRecord | undefined {
  const identity = draftIdentity(ownerId, conversationId);
  const cached = store[type].get(identity);
  if (cached) return cached;
  const draft = loadSendBoxDraft(ownerId, conversationId);
  const record = draft ? { draft } : undefined;
  if (record) store[type].set(identity, record);
  return record;
}

/** One owner for ordinary edits and typed conditional pending-mutation writes. */
export const getSendBoxDraftHook = <K extends DraftConversationType>(
  type: K,
  initialValue: Extract<Draft, { _type: K }>
) => {
  type TypedDraft = Extract<Draft, { _type: K }>;
  function useDraft(conversation_id: string, ownerId = 'local') {
    const identity = draftIdentity(ownerId, conversation_id);
    const storageKey = sendBoxDraftStorageKey(ownerId, conversation_id);
    const swrRet = useSWR(
      [`/send-box/${type}/draft`, ownerId, conversation_id],
      ([_, owner, id]) => getRecord(type, owner, id) ?? null
    );
    const publish = useCallback(
      (record: DraftRecord | undefined) => {
        if (record) store[type].set(identity, record);
        else store[type].delete(identity);
        void swrRet.mutate(record ?? null, { revalidate: false }).catch((error) => {
          console.error('Failed to update draft view:', error);
        });
      },
      [identity, swrRet, type]
    );

    useEffect(() => {
      if (!storageKey || typeof window === 'undefined') return;
      const handleStorage = (event: StorageEvent) => {
        if (event.key !== storageKey) return;
        const cached = store[type].get(identity);
        if (cached?.unsaved) {
          const read = readSendBoxDraftForMutation(ownerId, conversation_id);
          if (!read.available) return;
          publish({ ...cached, draft: { ...(read.draft ?? initialValue), ...cached.unsaved } });
        } else {
          store[type].delete(identity);
          publish(getRecord(type, ownerId, conversation_id));
        }
      };
      window.addEventListener('storage', handleStorage);
      return () => window.removeEventListener('storage', handleStorage);
    }, [conversation_id, ownerId, storageKey, identity, publish, type]);

    const mutateDraft = useCallback(
      (update: (draft: TypedDraft) => TypedDraft | undefined): void => {
        // Storage events can arrive late. Every ordinary write, including clear,
        // reads the latest owner and cannot replace or resurrect its reserved hint.
        const cached = store[type].get(identity);
        const read = readSendBoxDraftForMutation(ownerId, conversation_id);
        const fallback = cached?.draft ?? initialValue;
        const durable = read.available ? (read.draft ?? { ...fallback, pdfReadingMutation: undefined }) : fallback;
        const reservedPending = durable.pdfReadingMutation ? structuredClone(durable.pdfReadingMutation) : undefined;
        const current = { ...durable, ...cached?.unsaved } as TypedDraft;
        const proposed = update(current);
        const next = { ...(proposed ?? initialValue), pdfReadingMutation: reservedPending } as TypedDraft;
        if (read.available && saveSendBoxDraft(ownerId, conversation_id, next)) {
          publish(!proposed && !next.pdfReadingMutation ? undefined : { draft: next });
        } else {
          // Editing remains available, but unknown storage is never overwritten.
          // Remember only changed ordinary fields so a later typed ACK cannot
          // erase this tab's unsaved edit or copy stale unrelated fields to disk.
          publish({
            draft: next,
            unsaved: {
              ...cached?.unsaved,
              ...(proposed ? changedOrdinaryDraftFields(current, next) : ordinaryDraftValues(initialValue)),
            },
            saveFailed: true,
          });
        }
      },
      [conversation_id, ownerId, identity, publish, type]
    );

    const mutatePersistedIf = useCallback(
      (
        update: (draft: TypedDraft) => TypedDraft | undefined
      ): { available: true; draft: TypedDraft } | { available: false } => {
        // This conditional read/write is local, not a cross-process transaction.
        const read = readSendBoxDraftForMutation(ownerId, conversation_id);
        if (!read.available) return { available: false };
        const cached = store[type].get(identity);
        const persisted = (read.draft ?? {
          ...(cached?.unsaved ? initialValue : (cached?.draft ?? initialValue)),
          pdfReadingMutation: undefined,
        }) as TypedDraft;
        const next = update(persisted);
        if (next && !saveSendBoxDraft(ownerId, conversation_id, next)) return { available: false };
        const current = next ?? persisted;
        const displayed = { ...current, ...cached?.unsaved };
        if (!cached || !sameDraftSnapshot(cached.draft, displayed)) {
          publish({ draft: displayed, unsaved: cached?.unsaved, saveFailed: cached?.saveFailed });
        }
        return { available: true, draft: current };
      },
      [conversation_id, ownerId, identity, publish, type]
    );

    const replaceContentIfUnchanged = useCallback(
      (expected: string, replacement: string): boolean => {
        const cached = store[type].get(identity);
        const read = readSendBoxDraftForMutation(ownerId, conversation_id);
        const persisted = read.available ? read.draft : undefined;
        if (
          !cached ||
          cached.saveFailed ||
          !persisted ||
          cached.draft.content !== expected ||
          persisted.content !== expected ||
          !sameDraftSnapshot(cached.draft, persisted)
        )
          return false;
        const updated = { ...persisted, content: replacement };
        if (!saveSendBoxDraft(ownerId, conversation_id, updated)) return false;
        publish({ draft: updated });
        return true;
      },
      [conversation_id, ownerId, identity, publish, type]
    );

    const retrySave = useCallback(() => mutateDraft((draft) => draft), [mutateDraft]);
    return {
      get data() {
        return swrRet.data?.draft as TypedDraft | undefined;
      },
      saveFailed: swrRet.data?.saveFailed === true,
      retrySave,
      mutate: mutateDraft,
      mutatePersistedIf,
      replaceContentIfUnchanged,
    };
  }
  return useDraft;
};
