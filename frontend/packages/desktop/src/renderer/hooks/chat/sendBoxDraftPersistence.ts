import {
  normalizeComposerContextItems,
  type ComposerContextItem,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';
import type { FileOrFolderItem } from '@/renderer/utils/file/fileTypes';
import { decodeSpecialistSendAuthority, type SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import {
  decodePdfReadingPending,
  decodePdfReadingSelection,
  type PdfReadingPendingMutation,
  type PdfReadingSelection,
} from '@/common/adapter/pdfReadingContext';

const DRAFT_VERSION = 1;
const DRAFT_STORAGE_PREFIX = 'xscience.composer.draft.v1';
const MAX_DRAFT_BYTES = 1 << 20;
const MAX_DRAFT_CONTENT_LENGTH = 1_000_000;
const MAX_DRAFT_FILE_ITEMS = 100;
const MAX_DRAFT_PATH_LENGTH = 8_192;
const MAX_DRAFT_NAME_LENGTH = 1_024;
const MAX_OWNER_ID_LENGTH = 256;
const MAX_CONVERSATION_ID_LENGTH = 512;

export type StagedSessionOptions = {
  delegation: boolean;
  autoReview?: boolean;
  memory?: boolean;
  targetAgent: string;
};

/** Retire only the explicit Memory intent; other staged options remain durable. */
export function retireStagedMemory(options: StagedSessionOptions): StagedSessionOptions {
  const { memory: _retired, ...remaining } = options;
  return remaining;
}

/** A new explicit review mutation supersedes only the old staged review intent. */
export function retireStagedAutoReview(options: StagedSessionOptions): StagedSessionOptions {
  const { autoReview: _retired, ...remaining } = options;
  return remaining;
}

export type AcpSendBoxDraft = {
  specialistQueuedAuthority?: SpecialistSendAuthority;
  specialistQueuedInvalid?: true;
  pdfReadingMutation?: PdfReadingPendingMutation;
  pdfReadingAttachmentsDisabled?: true;
  pdfReadingQueuedSelection?: PdfReadingSelection;
  pdfReadingQueuedInvalid?: true;
  _type: 'acp';
  content: string;
  atPath: Array<string | FileOrFolderItem>;
  uploadFile: string[];
  contextItems: ComposerContextItem[];
  /**
   * A staged task can request plan-first execution for its first explicit
   * send. It rides in the durable draft so a reload keeps the intent until
   * the user sends, and it is consumed by that first accepted send.
   */
  planMode?: true;
  /**
   * Staged session options travel in the durable draft for the same reason:
   * the runtime session-options load resolves asynchronously and must not
   * overwrite the requesting page's choices before the first explicit send.
   */
  stagedSessionOptions?: StagedSessionOptions;
};

type PersistedSendBoxDraft = {
  version: 1;
  draft: AcpSendBoxDraft;
};

export function sendBoxDraftStorageKey(ownerId: string, conversationId: string): string | null {
  const owner = ownerId.trim() || 'local';
  const conversation = conversationId.trim();
  if (
    owner.length > MAX_OWNER_ID_LENGTH ||
    conversation.length === 0 ||
    conversation.length > MAX_CONVERSATION_ID_LENGTH
  ) {
    return null;
  }
  return `${DRAFT_STORAGE_PREFIX}:${encodeURIComponent(owner)}:${encodeURIComponent(conversation)}`;
}

export function loadSendBoxDraft(ownerId: string, conversationId: string): AcpSendBoxDraft | undefined {
  const storage = browserStorage();
  const key = sendBoxDraftStorageKey(ownerId, conversationId);
  if (!storage || !key) return undefined;
  try {
    return readStoredDraft(storage, key);
  } catch {
    try {
      storage.removeItem(key);
    } catch {
      // Storage may become unavailable between reads. The in-memory draft
      // remains authoritative for the current page lifetime.
    }
    return undefined;
  }
}

function readStoredDraft(storage: Storage, key: string, strictPending = false): AcpSendBoxDraft | undefined {
  const raw = storage.getItem(key);
  if (!raw) return undefined;
  if (new TextEncoder().encode(raw).byteLength > MAX_DRAFT_BYTES) throw new Error('Draft exceeds storage limit');
  const parsed = JSON.parse(raw) as unknown;
  if (!isRecord(parsed) || parsed.version !== DRAFT_VERSION) throw new Error('Invalid draft version');
  if (
    strictPending &&
    isRecord(parsed.draft) &&
    parsed.draft.pdfReadingMutation !== undefined &&
    !decodePdfReadingPending(parsed.draft.pdfReadingMutation)
  )
    throw new Error('Invalid PDF recovery request');
  const draft = normalizeAcpSendBoxDraft(parsed.draft);
  if (!draft) throw new Error('Invalid draft');
  return draft;
}

/** Strict mutation reads distinguish absence from failure and never delete data. */
export function readSendBoxDraftForMutation(
  ownerId: string,
  conversationId: string
): { available: true; draft?: AcpSendBoxDraft } | { available: false } {
  const storage = browserStorage();
  const key = sendBoxDraftStorageKey(ownerId, conversationId);
  if (!storage || !key) return { available: false };
  try {
    return { available: true, draft: readStoredDraft(storage, key, true) };
  } catch {
    return { available: false };
  }
}

export function saveSendBoxDraft(ownerId: string, conversationId: string, value: AcpSendBoxDraft): boolean {
  const storage = browserStorage();
  const key = sendBoxDraftStorageKey(ownerId, conversationId);
  const draft = normalizeAcpSendBoxDraft(value);
  if (!storage || !key || !draft) return false;
  try {
    if (isEmptyDraft(draft)) {
      storage.removeItem(key);
      return true;
    }
    const persisted: PersistedSendBoxDraft = { version: DRAFT_VERSION, draft };
    const serialized = JSON.stringify(persisted);
    if (new TextEncoder().encode(serialized).byteLength > MAX_DRAFT_BYTES) return false;
    storage.setItem(key, serialized);
    return true;
  } catch {
    return false;
  }
}

function isEmptyDraft(draft: AcpSendBoxDraft): boolean {
  return (
    draft.content.length === 0 &&
    draft.atPath.length === 0 &&
    draft.uploadFile.length === 0 &&
    draft.contextItems.length === 0 &&
    draft.pdfReadingMutation === undefined &&
    draft.pdfReadingQueuedSelection === undefined &&
    draft.pdfReadingQueuedInvalid !== true &&
    draft.specialistQueuedAuthority === undefined &&
    draft.specialistQueuedInvalid !== true
  );
}

export function clearSendBoxDraft(ownerId: string, conversationId: string): void {
  const storage = browserStorage();
  const key = sendBoxDraftStorageKey(ownerId, conversationId);
  if (!storage || !key) return;
  try {
    storage.removeItem(key);
  } catch {
    // The in-memory store is still cleared by the caller.
  }
}

function normalizeAcpSendBoxDraft(value: unknown): AcpSendBoxDraft | null {
  if (!isRecord(value) || value._type !== 'acp') return null;
  if (typeof value.content !== 'string' || value.content.length > MAX_DRAFT_CONTENT_LENGTH) return null;
  const atPath = normalizeAtPath(value.atPath);
  const uploadFile = normalizeStringList(value.uploadFile, MAX_DRAFT_PATH_LENGTH);
  if (!atPath || !uploadFile) return null;
  return {
    _type: 'acp',
    content: value.content,
    atPath,
    uploadFile,
    contextItems: normalizeComposerContextItems(value.contextItems),
    ...(value.planMode === true ? { planMode: true as const } : {}),
    ...(value.pdfReadingAttachmentsDisabled === true ? { pdfReadingAttachmentsDisabled: true as const } : {}),
    ...normalizeFrozenPdfDraft(value),
    ...normalizeFrozenSpecialistDraft(value),
    ...normalizeStagedSessionOptions(value.stagedSessionOptions),
    ...(decodePdfReadingPending(value.pdfReadingMutation)
      ? { pdfReadingMutation: decodePdfReadingPending(value.pdfReadingMutation) }
      : {}),
  };
}

function normalizeFrozenSpecialistDraft(
  value: Record<string, unknown>
): Pick<AcpSendBoxDraft, 'specialistQueuedAuthority' | 'specialistQueuedInvalid'> {
  if (value.specialistQueuedInvalid === true) return { specialistQueuedInvalid: true };
  if (value.specialistQueuedAuthority === undefined) return {};
  try {
    return { specialistQueuedAuthority: decodeSpecialistSendAuthority(value.specialistQueuedAuthority) };
  } catch {
    return { specialistQueuedInvalid: true };
  }
}

function normalizeFrozenPdfDraft(
  value: Record<string, unknown>
): Pick<AcpSendBoxDraft, 'pdfReadingQueuedSelection' | 'pdfReadingQueuedInvalid'> {
  if (value.pdfReadingQueuedInvalid === true) return { pdfReadingQueuedInvalid: true };
  if (value.pdfReadingQueuedSelection === undefined) return {};
  try {
    return { pdfReadingQueuedSelection: decodePdfReadingSelection(value.pdfReadingQueuedSelection) };
  } catch {
    return { pdfReadingQueuedInvalid: true };
  }
}

function normalizeStagedSessionOptions(value: unknown): { stagedSessionOptions?: StagedSessionOptions } {
  if (!isRecord(value)) return {};
  const delegation = value.delegation;
  const autoReview = value.autoReview;
  const memory = value.memory;
  const targetAgent = value.targetAgent;
  if (
    typeof delegation !== 'boolean' ||
    (autoReview !== undefined && typeof autoReview !== 'boolean') ||
    (memory !== undefined && typeof memory !== 'boolean') ||
    typeof targetAgent !== 'string' ||
    targetAgent.length > 256
  ) {
    return {};
  }
  return {
    stagedSessionOptions: {
      delegation,
      ...(typeof autoReview === 'boolean' ? { autoReview } : {}),
      ...(typeof memory === 'boolean' ? { memory } : {}),
      targetAgent,
    },
  };
}

function normalizeAtPath(value: unknown): Array<string | FileOrFolderItem> | null {
  if (!Array.isArray(value) || value.length > MAX_DRAFT_FILE_ITEMS) return null;
  const result: Array<string | FileOrFolderItem> = [];
  const seen = new Set<string>();
  for (const candidate of value) {
    let item: string | FileOrFolderItem | null = null;
    if (validBoundedString(candidate, MAX_DRAFT_PATH_LENGTH)) {
      item = candidate;
    } else if (isRecord(candidate)) {
      const path = validBoundedString(candidate.path, MAX_DRAFT_PATH_LENGTH) ? candidate.path : null;
      const name = validBoundedString(candidate.name, MAX_DRAFT_NAME_LENGTH) ? candidate.name : null;
      const relativePath =
        candidate.relativePath === undefined
          ? undefined
          : validBoundedString(candidate.relativePath, MAX_DRAFT_PATH_LENGTH)
            ? candidate.relativePath
            : null;
      if (path && name && typeof candidate.isFile === 'boolean' && relativePath !== null) {
        item = {
          path,
          name,
          isFile: candidate.isFile,
          ...(relativePath === undefined ? {} : { relativePath }),
        };
      }
    }
    if (!item) return null;
    const identity = typeof item === 'string' ? `path:${item}` : `item:${item.path}:${item.relativePath ?? ''}`;
    if (seen.has(identity)) continue;
    seen.add(identity);
    result.push(item);
  }
  return result;
}

function normalizeStringList(value: unknown, maxLength: number): string[] | null {
  if (!Array.isArray(value) || value.length > MAX_DRAFT_FILE_ITEMS) return null;
  const result: string[] = [];
  const seen = new Set<string>();
  for (const candidate of value) {
    if (!validBoundedString(candidate, maxLength)) return null;
    if (seen.has(candidate)) continue;
    seen.add(candidate);
    result.push(candidate);
  }
  return result;
}

function validBoundedString(value: unknown, maxLength: number): value is string {
  return typeof value === 'string' && value.trim().length > 0 && value.length <= maxLength;
}

function browserStorage(): Storage | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
