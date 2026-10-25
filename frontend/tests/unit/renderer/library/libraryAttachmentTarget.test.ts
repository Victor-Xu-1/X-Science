import { describe, expect, it } from 'vitest';
import {
  currentLibraryAttachmentTarget,
  resolveLibraryAttachmentTarget,
} from '@/renderer/pages/library/libraryAttachmentTarget';
import { attachmentItem, attachmentVersion } from './libraryAttachmentFixtures';

describe('Library attachment exact-version targets', () => {
  it('chooses the highest actual version without replacing an unavailable latest with an older file', () => {
    const item = attachmentItem([
      attachmentVersion('v1', 1),
      attachmentVersion('v3', 3, false),
      attachmentVersion('v2', 2),
    ]);
    const latest = resolveLibraryAttachmentTarget(item, 'attachment-a');
    expect(latest?.version.id).toBe('v3');
    expect(latest?.version.available).toBe(false);
    expect(resolveLibraryAttachmentTarget(item, 'attachment-a', { kind: 'version', id: 'v1' })?.contentUrl).toBe(
      '/api/literature/attachment-versions/v1/content'
    );
    expect(resolveLibraryAttachmentTarget(item, 'attachment-a', { kind: 'version', id: 'absent' })).toBeUndefined();
  });

  it('uses only a version belonging to the selected item and attachment, without guessing artifact identity', () => {
    const item = attachmentItem();
    expect(resolveLibraryAttachmentTarget(item, 'another-attachment')).toBeUndefined();
    const target = resolveLibraryAttachmentTarget(item, 'attachment-a', { kind: 'version', id: 'v1' })!;
    expect(target.itemId).toBe('paper-a');
    expect(target.attachmentId).toBe('attachment-a');
    expect(target.contentUrl).not.toContain('/artifacts/');
    expect(target.contentUrl).not.toContain('frame_id=');
    expect(currentLibraryAttachmentTarget({ ...item, id: 'another-item' }, target)).toBeUndefined();
    expect(resolveLibraryAttachmentTarget({ ...item, deleted: true }, 'attachment-a')).toBeUndefined();
  });

  it('retains unsupported originals for download only and rejects changed immutable metadata', () => {
    const original = attachmentVersion('support', 1);
    const item = attachmentItem([{ ...original, filename: 'support.html', mime_type: 'text/html' }]);
    const target = resolveLibraryAttachmentTarget(item, 'attachment-a')!;
    expect(target.isPdf).toBe(false);
    expect(target.contentUrl).toBe('/api/literature/attachment-versions/support/content');
    const changed = attachmentItem([{ ...item.attachments[0].versions[0], checksum: 'b'.repeat(64) }]);
    expect(currentLibraryAttachmentTarget(changed, target)).toBeUndefined();
  });
});
