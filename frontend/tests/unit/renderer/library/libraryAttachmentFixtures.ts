import type { LiteratureItem, LiteratureVersion } from '@/common/adapter/literatureLibrary';

export const attachmentVersion = (id: string, number: number, available = true): LiteratureVersion => ({
  id,
  attachment_id: 'attachment-a',
  version_number: number,
  filename: 'paper-' + number + '.pdf',
  mime_type: 'application/pdf',
  size_bytes: 7,
  checksum: 'a'.repeat(64),
  page_count: 1,
  available,
});
export const attachmentItem = (
  versions: LiteratureVersion[] = [attachmentVersion('v1', 1), attachmentVersion('v2', 2)]
): LiteratureItem => ({
  id: 'paper-a',
  title: 'Synthetic paper',
  metadata_revision: 1,
  metadata: {},
  deleted: false,
  project_ids: [],
  attachments: [{ id: 'attachment-a', kind: 'fullText', versions }],
});
