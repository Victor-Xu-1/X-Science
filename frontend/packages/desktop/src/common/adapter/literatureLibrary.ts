import { decodeLiteratureMetadataPatch, type LiteratureMetadataPatch } from './literatureMetadataPatch';
import { literatureValueIdentity } from './literatureValueIdentity';
import { decodeLiteratureReadingStatus, type LiteratureReadingStatus } from './literatureReadingStatus';

export { literatureValueIdentity } from './literatureValueIdentity';
export type { LiteratureMetadataPatch } from './literatureMetadataPatch';

/** An authoritative detail response cannot be used for the requested item. */
export class LiteratureDetailUnavailableError extends Error {
  constructor() {
    super('Literature detail is no longer valid for this view');
  }
}

export type LiteratureVersion = {
  id: string;
  attachment_id: string;
  version_number: number;
  filename: string;
  mime_type: string;
  size_bytes: number;
  checksum: string;
  page_count: number;
  available: boolean;
};
export type LiteratureItem = {
  id: string;
  title: string;
  metadata_revision: number;
  reading_status?: LiteratureReadingStatus;
  metadata: Record<string, unknown>;
  deleted: boolean;
  project_ids: string[];
  attachments: Array<{ id: string; kind: string; versions: LiteratureVersion[] }>;
};
export type LiteraturePage = {
  schema: 'x-science.literature-page.v1';
  items: LiteratureItem[];
  next_cursor?: string;
};
export type LiteratureReceipt = {
  schema: 'x-science.literature-receipt.v1';
  operation_id: string;
  item: LiteratureItem;
  attachment_id?: string;
  version_id?: string;
};
export type LiteratureCommand =
  | {
      kind: 'import';
      operation_id: string;
      artifact_id: string;
      version_id: string;
      title: string;
      metadata?: Record<string, unknown>;
      project_id?: string;
    }
  | {
      kind: 'edit';
      operation_id: string;
      item_id: string;
      expected_revision: number;
      title?: string;
      metadata?: Record<string, unknown>;
      deleted?: boolean;
    }
  | {
      kind: 'edit-metadata';
      operation_id: string;
      item_id: string;
      expected_revision: number;
      title?: string;
      metadata_patch: LiteratureMetadataPatch;
    }
  | { kind: 'membership'; operation_id: string; item_id: string; project_id: string; included: boolean };

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const identity = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 512 && value.trim() === value;
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const fail = (): never => {
  throw new Error('Invalid literature Library contract');
};
/** A missing legacy field has one presentation default; explicit invalid values do not. */
export function literatureItemReadingStatus(item: Pick<LiteratureItem, 'reading_status'>): LiteratureReadingStatus {
  return item.reading_status === undefined ? 'unread' : decodeLiteratureReadingStatus(item.reading_status);
}
export function decodeLiteratureItem(value: unknown): LiteratureItem {
  if (
    !record(value) ||
    !identity(value.id) ||
    typeof value.title !== 'string' ||
    value.title.length > 4096 ||
    !count(value.metadata_revision) ||
    !record(value.metadata) ||
    typeof value.deleted !== 'boolean' ||
    !Array.isArray(value.project_ids) ||
    value.project_ids.length > 10000 ||
    !value.project_ids.every(identity) ||
    new Set(value.project_ids).size !== value.project_ids.length ||
    !Array.isArray(value.attachments) ||
    value.attachments.length > 100
  )
    return fail();
  const attachmentIds = new Set<string>();
  const versionIds = new Set<string>();
  const attachments = value.attachments.map((attachment) => {
    if (
      !record(attachment) ||
      !identity(attachment.id) ||
      attachmentIds.has(attachment.id) ||
      typeof attachment.kind !== 'string' ||
      !Array.isArray(attachment.versions) ||
      attachment.versions.length > 1000
    )
      return fail();
    attachmentIds.add(attachment.id);
    const attachmentId = attachment.id;
    const versions = attachment.versions.map((version): LiteratureVersion => {
      if (
        !record(version) ||
        !identity(version.id) ||
        versionIds.has(version.id) ||
        version.attachment_id !== attachment.id ||
        !count(version.version_number) ||
        version.version_number < 1 ||
        typeof version.filename !== 'string' ||
        !version.filename ||
        version.filename.length > 4096 ||
        typeof version.mime_type !== 'string' ||
        version.mime_type.length > 512 ||
        !count(version.size_bytes) ||
        typeof version.checksum !== 'string' ||
        !/^[a-f0-9]{64}$/.test(version.checksum) ||
        !count(version.page_count) ||
        typeof version.available !== 'boolean'
      )
        return fail();
      versionIds.add(version.id);
      return {
        id: version.id,
        attachment_id: attachmentId,
        version_number: version.version_number,
        filename: version.filename,
        mime_type: version.mime_type,
        size_bytes: version.size_bytes,
        checksum: version.checksum,
        page_count: version.page_count,
        available: version.available,
      };
    });
    return { id: attachment.id, kind: attachment.kind, versions };
  });
  return {
    id: value.id,
    title: value.title,
    metadata_revision: value.metadata_revision,
    ...(Object.hasOwn(value, 'reading_status')
      ? { reading_status: decodeLiteratureReadingStatus(value.reading_status) }
      : {}),
    metadata: structuredClone(value.metadata),
    deleted: value.deleted,
    project_ids: [...value.project_ids],
    attachments,
  };
}
export function decodeLiteraturePage(value: unknown): LiteraturePage {
  if (
    !record(value) ||
    value.schema !== 'x-science.literature-page.v1' ||
    !Array.isArray(value.items) ||
    value.items.length > 100 ||
    (value.next_cursor !== undefined &&
      (typeof value.next_cursor !== 'string' ||
        !value.next_cursor ||
        value.next_cursor.length > 4096 ||
        value.next_cursor.trim() !== value.next_cursor))
  )
    return fail();
  const items = value.items.map(decodeLiteratureItem);
  if (new Set(items.map((item) => item.id)).size !== items.length) return fail();
  return {
    schema: value.schema,
    items,
    ...(typeof value.next_cursor === 'string' ? { next_cursor: value.next_cursor } : {}),
  };
}
export function decodeLiteratureReceipt(value: unknown, operationId: string): LiteratureReceipt {
  if (!record(value) || value.schema !== 'x-science.literature-receipt.v1' || value.operation_id !== operationId)
    return fail();
  const item = decodeLiteratureItem(value.item);
  if ((value.attachment_id === undefined) !== (value.version_id === undefined)) return fail();
  if (
    value.attachment_id !== undefined &&
    !item.attachments.some(
      (attachment) =>
        attachment.id === value.attachment_id && attachment.versions.some((version) => version.id === value.version_id)
    )
  )
    return fail();
  return {
    schema: value.schema,
    operation_id: operationId,
    item,
    ...(typeof value.attachment_id === 'string' && typeof value.version_id === 'string'
      ? { attachment_id: value.attachment_id, version_id: value.version_id }
      : {}),
  };
}
export function decodeLiteratureCommand(value: unknown): LiteratureCommand {
  if (
    !record(value) ||
    typeof value.operation_id !== 'string' ||
    !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value.operation_id)
  )
    return fail();
  if (value.kind === 'edit-metadata') {
    if (
      Reflect.ownKeys(value).some(
        (key) =>
          typeof key !== 'string' ||
          !['kind', 'operation_id', 'item_id', 'expected_revision', 'title', 'metadata_patch'].includes(key)
      ) ||
      !identity(value.item_id) ||
      !count(value.expected_revision) ||
      (value.title !== undefined &&
        (typeof value.title !== 'string' || !value.title.trim() || value.title.length > 4096)) ||
      !Object.hasOwn(value, 'metadata_patch')
    )
      return fail();
    const command: LiteratureCommand = {
      kind: 'edit-metadata',
      operation_id: value.operation_id,
      item_id: value.item_id,
      expected_revision: value.expected_revision,
      ...(typeof value.title === 'string' ? { title: value.title.trim() } : {}),
      metadata_patch: decodeLiteratureMetadataPatch(value.metadata_patch),
    };
    // The existing pending owner compares this whole immutable command.
    literatureValueIdentity(command);
    return command;
  }
  // A patch is never silently stripped into an old edit/import/membership.
  if (Object.hasOwn(value, 'metadata_patch')) return fail();
  const metadata = value.metadata === undefined ? undefined : decodeLiteratureMetadata(value.metadata);
  if (
    value.kind === 'import' &&
    identity(value.artifact_id) &&
    identity(value.version_id) &&
    typeof value.title === 'string' &&
    value.title.trim().length > 0 &&
    value.title.length <= 4096 &&
    (value.project_id === undefined || identity(value.project_id)) &&
    (value.metadata === undefined || record(value.metadata))
  )
    return {
      kind: 'import',
      operation_id: value.operation_id,
      artifact_id: value.artifact_id,
      version_id: value.version_id,
      title: value.title.trim(),
      ...(typeof value.project_id === 'string' ? { project_id: value.project_id } : {}),
      ...(metadata ? { metadata } : {}),
    };
  if (
    value.kind === 'membership' &&
    identity(value.item_id) &&
    identity(value.project_id) &&
    typeof value.included === 'boolean'
  )
    return {
      kind: 'membership',
      operation_id: value.operation_id,
      item_id: value.item_id,
      project_id: value.project_id,
      included: value.included,
    };
  if (
    value.kind === 'edit' &&
    identity(value.item_id) &&
    count(value.expected_revision) &&
    (value.title === undefined ||
      (typeof value.title === 'string' && value.title.trim().length > 0 && value.title.length <= 4096)) &&
    (value.metadata === undefined || record(value.metadata)) &&
    (value.deleted === undefined || typeof value.deleted === 'boolean') &&
    (value.title !== undefined || value.metadata !== undefined || value.deleted !== undefined)
  )
    return {
      kind: 'edit',
      operation_id: value.operation_id,
      item_id: value.item_id,
      expected_revision: value.expected_revision,
      ...(typeof value.title === 'string' ? { title: value.title.trim() } : {}),
      ...(typeof value.deleted === 'boolean' ? { deleted: value.deleted } : {}),
      ...(metadata ? { metadata } : {}),
    };
  return fail();
}
function decodeLiteratureMetadata(value: unknown): Record<string, unknown> {
  if (!record(value)) return fail();
  const raw = JSON.stringify(value);
  if (raw.length > 65536) return fail();
  const normalized: unknown = JSON.parse(raw);
  literatureValueIdentity(normalized);
  return normalized as Record<string, unknown>;
}
