import { describe, expect, it } from 'vitest';
import { libraryNoteCommand, libraryPersonalNote } from '@/renderer/pages/library/libraryPersonalNote';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';

const item = (metadata: Record<string, unknown>): LiteratureItem => ({
  id: 'note-item',
  title: 'Synthetic note source',
  metadata_revision: 7,
  metadata,
  deleted: false,
  project_ids: ['original-project'],
  attachments: [],
});
describe('Personal note changes use only the existing metadata CAS patch', () => {
  it('trims ECMAScript edges while preserving internal text and untouched opaque metadata', () => {
    const base = item({ personal_note: 'Old', opaque: { nested: ['keep', 123] } });
    const command = libraryNoteCommand(base, '\uFEFF\u00a0 First\n  second \u00a0', 'original-operation');
    expect(command).toEqual({
      kind: 'edit-metadata',
      operation_id: 'original-operation',
      item_id: base.id,
      expected_revision: 7,
      metadata_patch: { set: { personal_note: 'First\n  second' } },
    });
    expect(base.metadata).toEqual({ personal_note: 'Old', opaque: { nested: ['keep', 123] } });
    expect(command).not.toHaveProperty('metadata');
    expect(command).not.toHaveProperty('title');
  });
  it('clears only the note key and leaves missing/nonstring/overlong unedited legacy values alone', () => {
    expect(
      libraryNoteCommand(item({ personal_note: 'Old', doi: '10.1/keep' }), ' \uFEFF ', 'clear')?.metadata_patch
    ).toEqual({ remove: ['personal_note'] });
    for (const metadata of [{}, { personal_note: { opaque: true } }, { personal_note: 'x'.repeat(10_001) }]) {
      const base = item(metadata);
      expect(libraryNoteCommand(base, libraryPersonalNote(base), 'untouched')).toBeUndefined();
      expect(base.metadata).toEqual(metadata);
    }
  });
  it('enforces the trimmed UTF16 boundary without truncating or changing the complete-metadata limit', () => {
    const base = item({ personal_note: 'Old' });
    expect(
      libraryNoteCommand(base, ` ${'😀'.repeat(5000)} `, 'boundary')?.metadata_patch.set?.personal_note
    ).toHaveLength(10_000);
    const draft = `${'😀'.repeat(5000)}x`;
    expect(() => libraryNoteCommand(base, draft, 'too-long')).toThrow('library_note_too_long');
    expect(draft).toHaveLength(10_001);
  });
});
