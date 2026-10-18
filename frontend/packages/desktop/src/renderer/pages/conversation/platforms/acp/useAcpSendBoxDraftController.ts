import { useCallback } from 'react';
import type {
  PdfReadingPendingAccess,
  PdfReadingPendingResult,
} from '@/renderer/services/runtime/pdfReadingPendingState';
import { samePdfReadingPending, type PdfReadingSelection } from '@/common/adapter/pdfReadingContext';
import type { StagedSessionOptions } from '@/renderer/hooks/chat/sendBoxDraftPersistence';
import { getSendBoxDraftHook, type FileOrFolderItem } from '@/renderer/hooks/chat/useSendBoxDraft';
import { createSetUploadFile } from '@/renderer/hooks/chat/useSendBoxFiles';
import {
  normalizeComposerContextItems,
  type ComposerContextItem,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';

const useAcpSendBoxDraft = getSendBoxDraftHook('acp', {
  _type: 'acp',
  atPath: [],
  content: '',
  uploadFile: [],
  contextItems: [],
});

const EMPTY_AT_PATH: Array<string | FileOrFolderItem> = [];
const EMPTY_UPLOAD_FILES: string[] = [];
const EMPTY_CONTEXT_ITEMS: ComposerContextItem[] = [];

export const useAcpSendBoxDraftController = (conversationId: string, ownerId?: string) => {
  const { data, mutate, mutatePersistedIf, replaceContentIfUnchanged, saveFailed, retrySave } = useAcpSendBoxDraft(
    conversationId,
    ownerId
  );
  const atPath = data?.atPath ?? EMPTY_AT_PATH;
  const uploadFile = data?.uploadFile ?? EMPTY_UPLOAD_FILES;
  const content = data?.content ?? '';
  const contextItems = normalizeComposerContextItems(data?.contextItems ?? EMPTY_CONTEXT_ITEMS);
  const stagedPlanMode = data?.planMode === true;

  const setAtPath = useCallback(
    (nextAtPath: Array<string | FileOrFolderItem>) => {
      mutate((previous) => ({ ...previous, atPath: nextAtPath }));
    },
    [data, mutate]
  );
  const setUploadFile = createSetUploadFile(mutate, data);
  const setContent = useCallback(
    (nextContent: string) => {
      mutate((previous) => ({ ...previous, content: nextContent }));
    },
    [data, mutate]
  );
  const setContextItems = useCallback(
    (nextItems: ComposerContextItem[] | ((current: ComposerContextItem[]) => ComposerContextItem[])) => {
      mutate((previous) => {
        const current = normalizeComposerContextItems(previous.contextItems);
        const next = typeof nextItems === 'function' ? nextItems(current) : nextItems;
        return {
          ...previous,
          contextItems: normalizeComposerContextItems(next),
          ...(next.length === 0 ? { pdfReadingAttachmentsDisabled: undefined } : {}),
        };
      });
    },
    [data, mutate]
  );
  const setStagedPlanMode = useCallback(
    (enabled: boolean) => {
      mutate((previous) => {
        if (enabled === (previous.planMode === true)) return previous;
        return { ...previous, ...(enabled ? { planMode: true as const } : { planMode: undefined }) };
      });
    },
    [data, mutate]
  );
  const setStagedSessionOptions = useCallback(
    (options: StagedSessionOptions | null) => {
      mutate((previous) => ({ ...previous, stagedSessionOptions: options ?? undefined }));
    },
    [data, mutate]
  );
  const setPdfReadingMutation = useCallback<PdfReadingPendingAccess>(
    (pending, expected): PdfReadingPendingResult => {
      let status: 'current' | 'applied' | 'conflict' = 'current';
      const result = mutatePersistedIf((previous) => {
        const current = previous.pdfReadingMutation;
        const supplied = pending ?? expected;
        if (supplied && (supplied.ownerId !== ownerId || supplied.frameId !== conversationId)) {
          status = 'conflict';
          return undefined;
        }
        if (!supplied) return undefined;
        if (current && !samePdfReadingPending(current, expected ?? pending)) {
          status = 'conflict';
          return undefined;
        }
        status = 'applied';
        if ((!pending && !current) || samePdfReadingPending(current, pending)) return undefined;
        return { ...previous, pdfReadingMutation: pending };
      });
      return result.available ? { status, pending: result.draft.pdfReadingMutation } : { status: 'unavailable' };
    },
    [conversationId, ownerId, mutatePersistedIf]
  );
  const keepPdfAttachments = useCallback(() => {
    mutate((previous) => ({ ...previous, pdfReadingAttachmentsDisabled: true }));
  }, [mutate]);
  const setQueuedPdfSelection = useCallback(
    (selection?: PdfReadingSelection, invalid = false) => {
      mutate((previous) => ({
        ...previous,
        pdfReadingQueuedSelection: selection ? structuredClone(selection) : undefined,
        pdfReadingQueuedInvalid: invalid ? true : undefined,
      }));
    },
    [mutate]
  );

  return {
    atPath,
    uploadFile,
    setAtPath,
    setUploadFile,
    content,
    setContent,
    replaceContentIfUnchanged,
    draftSaveFailed: saveFailed,
    retryDraftSave: retrySave,
    contextItems,
    setContextItems,
    stagedPlanMode,
    setStagedPlanMode,
    stagedSessionOptions: data?.stagedSessionOptions ?? null,
    setStagedSessionOptions,
    pdfReadingMutation: data?.pdfReadingMutation,
    setPdfReadingMutation,
    pdfReadingAttachmentsDisabled: data?.pdfReadingAttachmentsDisabled === true,
    keepPdfAttachments,
    queuedPdfSelection: data?.pdfReadingQueuedSelection,
    queuedPdfInvalid: data?.pdfReadingQueuedInvalid === true,
    setQueuedPdfSelection,
  };
};
