import { normalizeLiteratureCollectionText } from './literatureCollections';

export const RESOURCE_TAG_FAVORITE_ID = 'tag-favorite';
export const RESOURCE_TAG_MUTATION_BODY_BYTES = 64 * 1024;
export const RESOURCE_TAG_TIMESTAMP_MAX = 8_640_000_000_000_000;
export const RESOURCE_TAG_ICON_KEYS = [
  'tag',
  'star',
  'bookmark',
  'flask-conical',
  'book-open',
  'database',
  'code-2',
  'bot',
] as const;
export const RESOURCE_TAG_COLOR_KEYS = ['gray', 'red', 'orange', 'amber', 'green', 'blue', 'purple', 'pink'] as const;
export const RESOURCE_TAG_RESOURCE_TYPES = [
  'catalog.skill',
  'catalog.connector',
  'catalog.specialist',
  'literature.item',
  'pdf.annotation',
] as const;
export type ResourceTagIconKey = (typeof RESOURCE_TAG_ICON_KEYS)[number];
export type ResourceTagColorKey = (typeof RESOURCE_TAG_COLOR_KEYS)[number];
export type ResourceTagResourceType = (typeof RESOURCE_TAG_RESOURCE_TYPES)[number];
export type ResourceTag = Readonly<
  { id: string; created_at: number; updated_at: number } & (
    | { system_key: 'favorite' }
    | { name: string; icon_key: ResourceTagIconKey; color_key: ResourceTagColorKey }
  )
>;
export type ResourceTagRef = Readonly<{ resource_type: ResourceTagResourceType; resource_id: string }>;
export type ResourceTagAssignment = Readonly<{
  tag_id: string;
  resource_type: ResourceTagResourceType;
  resource_id: string;
  created_at: number;
}>;
export type ResourceTagSnapshot = Readonly<{
  schema: 'x-science.resource-tags.v1';
  revision: number;
  resource_types: readonly ResourceTagResourceType[];
  tags: readonly ResourceTag[];
  assignments: readonly ResourceTagAssignment[];
}>;
type Operation = { operation_id: string };
type CustomFields = { name: string; icon_key: ResourceTagIconKey; color_key: ResourceTagColorKey };
export type ResourceTagCommand = Readonly<
  Operation &
    (
      | ({ kind: 'create' } & CustomFields)
      | ({ kind: 'update'; tag_id: string; expected_updated_at: number } & CustomFields)
      | { kind: 'delete'; tag_id: string }
      | { kind: 'reorder'; tag_ids: readonly string[] }
      | {
          kind: 'assignment';
          tag_id: string;
          resource_type: ResourceTagResourceType;
          resource_id: string;
          assigned: boolean;
        }
    )
>;
/** An original operation receipt. Its snapshot is historical, never an implicit current-view update. */
export type ResourceTagReceipt = Readonly<{
  schema: 'x-science.resource-tag-receipt.v1';
  operation_id: string;
  action: ResourceTagCommand['kind'];
  tag_id?: string;
  snapshot: ResourceTagSnapshot;
}>;

const fail = (): never => {
  throw new Error('Invalid resource tag contract');
};
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const scalar = (value: string) => !/[\uD800-\uDFFF]/u.test(value);
const text = (value: unknown): string => (typeof value === 'string' && scalar(value) ? value : fail());
const fields = (value: Record<string, unknown>, expected: readonly string[]) => {
  if (Reflect.ownKeys(value).length !== expected.length || !expected.every((key) => Object.hasOwn(value, key))) fail();
};
const array = (value: unknown): unknown[] => {
  if (!Array.isArray(value) || Reflect.ownKeys(value).length !== value.length + 1) return fail();
  return Array.from(value);
};
const choice = <T extends string>(value: unknown, allowed: readonly T[]): T => {
  if (typeof value !== 'string' || !allowed.includes(value as T)) return fail();
  return value as T;
};
const integer = (value: unknown, maximum = Number.MAX_SAFE_INTEGER): number => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || value > maximum) return fail();
  return value;
};
const timestamp = (value: unknown) => integer(value, RESOURCE_TAG_TIMESTAMP_MAX);
const identity = (value: unknown): string => {
  const id = text(value);
  // Mirror the server's bounded UTF-8 opaque ID and Unicode TrimSpace boundary.
  if (!id || new TextEncoder().encode(id).length > 512 || /^\p{White_Space}|\p{White_Space}$/u.test(id)) return fail();
  return id;
};
const operation = (value: unknown): string => {
  const id = text(value);
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id)) return fail();
  return id;
};
const resourceId = (value: unknown): string => {
  const original = text(value),
    trimmed = original.trim();
  if (!trimmed || trimmed.length > 256) return fail();
  return trimmed;
};
/** A canonical resource coordinate; this does not assert that its adapter is advertised or writable. */
export function decodeResourceTagRef(value: unknown): ResourceTagRef {
  if (!record(value)) return fail();
  fields(value, ['resource_type', 'resource_id']);
  return Object.freeze({
    resource_type: choice(value.resource_type, RESOURCE_TAG_RESOURCE_TYPES),
    resource_id: resourceId(value.resource_id),
  });
}
export function normalizeResourceTagName(value: unknown): string {
  const original = text(value);
  if (original.length > 64) return fail();
  let name: string;
  try {
    name = normalizeLiteratureCollectionText(original);
  } catch {
    return fail();
  }
  if (name.length > 64 || name.toLowerCase().length > 64) return fail();
  return name;
}
function decodeTag(value: unknown): ResourceTag {
  if (!record(value)) return fail();
  const id = identity(value.id),
    created_at = timestamp(value.created_at),
    updated_at = timestamp(value.updated_at);
  if (updated_at < created_at) return fail();
  if (value.system_key === 'favorite') {
    fields(value, ['id', 'system_key', 'created_at', 'updated_at']);
    if (id !== RESOURCE_TAG_FAVORITE_ID) return fail();
    return Object.freeze({ id, system_key: 'favorite', created_at, updated_at });
  }
  fields(value, ['id', 'name', 'icon_key', 'color_key', 'created_at', 'updated_at']);
  const name = normalizeResourceTagName(value.name);
  if (id === RESOURCE_TAG_FAVORITE_ID || name !== value.name) return fail();
  return Object.freeze({
    id,
    name,
    icon_key: choice(value.icon_key, RESOURCE_TAG_ICON_KEYS),
    color_key: choice(value.color_key, RESOURCE_TAG_COLOR_KEYS),
    created_at,
    updated_at,
  });
}
export function decodeResourceTagSnapshot(value: unknown): ResourceTagSnapshot {
  if (!record(value)) return fail();
  fields(value, ['schema', 'revision', 'resource_types', 'tags', 'assignments']);
  if (value.schema !== 'x-science.resource-tags.v1') return fail();
  const revision = integer(value.revision);
  const resource_types = array(value.resource_types).map((item) => choice(item, RESOURCE_TAG_RESOURCE_TYPES));
  if (new Set(resource_types).size !== resource_types.length) return fail();
  const tags = array(value.tags).map(decodeTag),
    byId = new Map(tags.map((tag) => [tag.id, tag]));
  if (!tags.length || byId.size !== tags.length || !('system_key' in tags[0])) return fail();
  const names = tags.flatMap((tag) => ('name' in tag ? [tag.name.toLowerCase()] : []));
  if (new Set(names).size !== names.length) return fail();
  const assignments = array(value.assignments).map((item): ResourceTagAssignment => {
    if (!record(item)) return fail();
    fields(item, ['tag_id', 'resource_type', 'resource_id', 'created_at']);
    const tag_id = identity(item.tag_id);
    const reference = decodeResourceTagRef({ resource_type: item.resource_type, resource_id: item.resource_id });
    if (
      !byId.has(tag_id) ||
      !resource_types.includes(reference.resource_type) ||
      reference.resource_id !== item.resource_id
    )
      return fail();
    return Object.freeze({ tag_id, ...reference, created_at: timestamp(item.created_at) });
  });
  const identities = assignments.map((item) => JSON.stringify([item.tag_id, item.resource_type, item.resource_id]));
  if (new Set(identities).size !== identities.length || (revision === 0 && (tags.length !== 1 || assignments.length)))
    return fail();
  return Object.freeze({
    schema: 'x-science.resource-tags.v1',
    revision,
    resource_types: Object.freeze(resource_types),
    tags: Object.freeze(tags),
    assignments: Object.freeze(assignments),
  });
}
export type ResourceTagWireRequest = Readonly<{
  method: 'POST' | 'PATCH' | 'DELETE' | 'PUT';
  path: string;
  body: Readonly<Record<string, unknown>>;
}>;
/** The sole wire encoder. Only decoded commands enter; routing is not another payload identity. */
function encodeValidatedCommand(command: ResourceTagCommand): ResourceTagWireRequest {
  if (command.kind === 'update' || command.kind === 'delete') {
    const { kind, tag_id, ...body } = command;
    return Object.freeze({
      method: kind === 'update' ? 'PATCH' : 'DELETE',
      path: `/api/tags/${encodeURIComponent(tag_id)}`,
      body: Object.freeze(body),
    });
  }
  const { kind, ...body } = command;
  return Object.freeze({
    method: kind === 'create' ? 'POST' : 'PUT',
    path: kind === 'create' ? '/api/tags' : kind === 'reorder' ? '/api/tags/order' : '/api/tags/assignments',
    body: Object.freeze(body),
  });
}
function freezeCommand(command: ResourceTagCommand): ResourceTagCommand {
  const wire = encodeValidatedCommand(command);
  if (new TextEncoder().encode(JSON.stringify(wire.body)).length > RESOURCE_TAG_MUTATION_BODY_BYTES) return fail();
  return Object.freeze(command);
}
/** Validate without rewriting the caller's raw name/resource ID or operation identity. */
export function decodeResourceTagCommand(value: unknown): ResourceTagCommand {
  if (!record(value)) return fail();
  const operation_id = operation(value.operation_id);
  if (value.kind === 'create' || value.kind === 'update') {
    fields(
      value,
      value.kind === 'create'
        ? ['kind', 'operation_id', 'name', 'icon_key', 'color_key']
        : ['kind', 'operation_id', 'tag_id', 'expected_updated_at', 'name', 'icon_key', 'color_key']
    );
    const name = text(value.name);
    normalizeResourceTagName(name);
    const common = {
      operation_id,
      name,
      icon_key: choice(value.icon_key, RESOURCE_TAG_ICON_KEYS),
      color_key: choice(value.color_key, RESOURCE_TAG_COLOR_KEYS),
    };
    if (value.kind === 'create') return freezeCommand({ kind: 'create', ...common });
    const expected_updated_at = timestamp(value.expected_updated_at);
    if (expected_updated_at === 0 || expected_updated_at === RESOURCE_TAG_TIMESTAMP_MAX) return fail();
    return freezeCommand({ kind: 'update', ...common, tag_id: identity(value.tag_id), expected_updated_at });
  }
  if (value.kind === 'delete') {
    fields(value, ['kind', 'operation_id', 'tag_id']);
    return freezeCommand({ kind: 'delete', operation_id, tag_id: identity(value.tag_id) });
  }
  if (value.kind === 'reorder') {
    fields(value, ['kind', 'operation_id', 'tag_ids']);
    const tag_ids = array(value.tag_ids).map(identity);
    if (tag_ids.includes(RESOURCE_TAG_FAVORITE_ID) || new Set(tag_ids).size !== tag_ids.length) return fail();
    return freezeCommand({ kind: 'reorder', operation_id, tag_ids: Object.freeze(tag_ids) });
  }
  if (value.kind === 'assignment') {
    fields(value, ['kind', 'operation_id', 'tag_id', 'resource_type', 'resource_id', 'assigned']);
    const reference = decodeResourceTagRef({ resource_type: value.resource_type, resource_id: value.resource_id });
    if (typeof value.assigned !== 'boolean') return fail();
    return freezeCommand({
      kind: 'assignment',
      operation_id,
      tag_id: identity(value.tag_id),
      resource_type: reference.resource_type,
      resource_id: text(value.resource_id),
      assigned: value.assigned,
    });
  }
  return fail();
}
export function decodeResourceTagReceipt(value: unknown, expected: ResourceTagCommand): ResourceTagReceipt {
  const command = decodeResourceTagCommand(expected);
  if (!record(value)) return fail();
  fields(
    value,
    command.kind === 'reorder'
      ? ['schema', 'operation_id', 'action', 'snapshot']
      : ['schema', 'operation_id', 'action', 'tag_id', 'snapshot']
  );
  if (
    value.schema !== 'x-science.resource-tag-receipt.v1' ||
    value.operation_id !== command.operation_id ||
    value.action !== command.kind
  )
    return fail();
  const snapshot = decodeResourceTagSnapshot(value.snapshot);
  const tag_id = command.kind === 'reorder' ? undefined : identity(value.tag_id);
  if (command.kind !== 'create' && command.kind !== 'reorder' && tag_id !== command.tag_id) return fail();
  const tag = snapshot.tags.find((item) => item.id === tag_id);
  if (command.kind === 'create' || command.kind === 'update') {
    if (
      !tag ||
      !('name' in tag) ||
      tag.name !== normalizeResourceTagName(command.name) ||
      tag.icon_key !== command.icon_key ||
      tag.color_key !== command.color_key ||
      (command.kind === 'update' && tag.updated_at <= command.expected_updated_at)
    )
      return fail();
  } else if (command.kind === 'delete') {
    if (tag) return fail();
  } else if (command.kind === 'reorder') {
    if (
      JSON.stringify(snapshot.tags.filter((item) => !('system_key' in item)).map((item) => item.id)) !==
      JSON.stringify(command.tag_ids)
    )
      return fail();
  } else {
    if (!tag || !snapshot.resource_types.includes(command.resource_type)) return fail();
    const assigned = snapshot.assignments.some(
      (item) =>
        item.tag_id === tag_id &&
        item.resource_type === command.resource_type &&
        item.resource_id === resourceId(command.resource_id)
    );
    if (assigned !== command.assigned) return fail();
  }
  return Object.freeze({
    schema: 'x-science.resource-tag-receipt.v1',
    operation_id: command.operation_id,
    action: command.kind,
    ...(tag_id === undefined ? {} : { tag_id }),
    snapshot,
  });
}

/** Validate and prepare the exact request, without allocating an operation or performing HTTP. */
export function prepareResourceTagCommand(
  input: unknown
): Readonly<ResourceTagWireRequest & { command: ResourceTagCommand }> {
  const command = decodeResourceTagCommand(input);
  return Object.freeze({ command, ...encodeValidatedCommand(command) });
}
