import type { LiteratureItem, LiteratureVersion } from '@/common/adapter/literatureLibrary';
import type { LiteraturePdfReadingSource } from '@/common/adapter/pdfReadingContext';
import { pdfReadingContentUrl } from '@/renderer/services/runtime/pdfReadingPreview';

export type LibraryAttachmentChoice = { kind: 'latest' } | { kind: 'version'; id: string };
export type LibraryAttachmentTarget = {
  key: string;
  itemId: string;
  attachmentId: string;
  version: LiteratureVersion;
  contentUrl: string;
  isPdf: boolean;
};

/** The caller supplies an item decoded by the original Library read owner. */
export function resolveLibraryAttachmentTarget(
  item: LiteratureItem,
  attachmentId: string,
  choice: LibraryAttachmentChoice = { kind: 'latest' }
): LibraryAttachmentTarget | undefined {
  if (item.deleted) return undefined;
  const attachment = item.attachments.find((value) => value.id === attachmentId);
  if (!attachment) return undefined;
  const version =
    choice.kind === 'version'
      ? attachment.versions.find((value) => value.id === choice.id)
      : attachment.versions.reduce<LiteratureVersion | undefined>(
          (latest, value) => (!latest || value.version_number > latest.version_number ? value : latest),
          undefined
        );
  // An unavailable latest or a missing explicit version must never choose another file.
  if (!version || version.attachment_id !== attachment.id) return undefined;
  const source: LiteraturePdfReadingSource = {
    source_kind: 'literature-attachment-version',
    item_id: item.id,
    attachment_id: attachment.id,
    version_id: version.id,
  };
  return {
    key: JSON.stringify([
      item.id,
      attachment.id,
      version.id,
      version.version_number,
      version.filename,
      version.mime_type,
      version.checksum,
      version.size_bytes,
      version.page_count,
    ]),
    itemId: item.id,
    attachmentId: attachment.id,
    version: { ...version },
    contentUrl: pdfReadingContentUrl(source),
    isPdf:
      version.mime_type.split(';')[0].trim().toLowerCase() === 'application/pdf' ||
      version.filename.toLowerCase().endsWith('.pdf'),
  };
}

export function currentLibraryAttachmentTarget(
  item: LiteratureItem,
  target: LibraryAttachmentTarget
): LibraryAttachmentTarget | undefined {
  if (item.id !== target.itemId) return undefined;
  const current = resolveLibraryAttachmentTarget(item, target.attachmentId, { kind: 'version', id: target.version.id });
  return current?.key === target.key && current.version.available ? current : undefined;
}
