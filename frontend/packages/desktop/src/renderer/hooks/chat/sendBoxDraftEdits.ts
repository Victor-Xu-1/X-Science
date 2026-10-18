import type { AcpSendBoxDraft } from './sendBoxDraftPersistence';

// These are presentation/draft fields. The pending mutation is reserved for
// the typed conditional owner and is deliberately absent from ordinary edits.
const ordinaryFields = [
  'content',
  'atPath',
  'uploadFile',
  'contextItems',
  'planMode',
  'stagedSessionOptions',
  'pdfReadingAttachmentsDisabled',
  'pdfReadingQueuedSelection',
  'pdfReadingQueuedInvalid',
] as const satisfies ReadonlyArray<keyof Omit<AcpSendBoxDraft, 'pdfReadingMutation'>>;

export type OrdinaryDraftEdits = Partial<Omit<AcpSendBoxDraft, '_type' | 'pdfReadingMutation'>>;

export function ordinaryDraftValues(draft: AcpSendBoxDraft): OrdinaryDraftEdits {
  return Object.fromEntries(ordinaryFields.map((field) => [field, draft[field]])) as OrdinaryDraftEdits;
}

export function changedOrdinaryDraftFields(before: AcpSendBoxDraft, after: AcpSendBoxDraft): OrdinaryDraftEdits {
  return Object.fromEntries(
    ordinaryFields
      .filter((field) => JSON.stringify(before[field]) !== JSON.stringify(after[field]))
      .map((field) => [field, after[field]])
  ) as OrdinaryDraftEdits;
}

export function sameDraftSnapshot(left: AcpSendBoxDraft, right: AcpSendBoxDraft): boolean {
  return (
    left._type === right._type &&
    ordinaryFields.every((field) => JSON.stringify(left[field]) === JSON.stringify(right[field])) &&
    JSON.stringify(left.pdfReadingMutation) === JSON.stringify(right.pdfReadingMutation)
  );
}
