import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';

export const LIBRARY_PERSONAL_NOTE_LIMIT = 10_000;
export type LibraryNoteCommand = Extract<LiteratureCommand, { kind: 'edit-metadata' }>;

/** Opaque legacy metadata is presentation-only until the user makes a meaningful edit. */
export function libraryPersonalNote(item: LiteratureItem): string {
  return typeof item.metadata.personal_note === 'string' ? item.metadata.personal_note : '';
}

export function libraryNoteCommand(
  item: LiteratureItem,
  draft: string,
  operationId: string
): LibraryNoteCommand | undefined {
  const original = libraryPersonalNote(item);
  const note = draft.trim();
  if (draft === original || note === original) return undefined;
  // Match the reference's trimmed JavaScript string limit; never silently slice a draft.
  if (note.length > LIBRARY_PERSONAL_NOTE_LIMIT) throw new Error('library_note_too_long');
  return {
    kind: 'edit-metadata',
    operation_id: operationId,
    item_id: item.id,
    expected_revision: item.metadata_revision,
    metadata_patch: note ? { set: { personal_note: note } } : { remove: ['personal_note'] },
  };
}
