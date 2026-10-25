export type LiteratureCollection = {
  id: string;
  revision: number;
  name: string;
  description: string;
  parent_id: string | null;
  sort_order: number;
  item_count: number;
};
export type LiteratureCollectionPage = {
  schema: 'x-science.literature-collections.v1';
  collections: LiteratureCollection[];
};
export type LiteratureCollectionMembership = {
  schema: 'x-science.literature-collection-membership.v1';
  item_id: string;
  collection_ids: string[];
};
export type LiteratureCollectionCommand =
  | { kind: 'collection-create'; operation_id: string; name: string; description?: string; parent_id?: string }
  | {
      kind: 'collection-update';
      operation_id: string;
      collection_id: string;
      expected_revision: number;
      name: string;
      description?: string;
    }
  | { kind: 'collection-delete'; operation_id: string; collection_id: string }
  | { kind: 'collection-membership'; operation_id: string; collection_id: string; item_id: string; included: boolean };
export type LiteratureCollectionReceipt = {
  schema: 'x-science.literature-collection-receipt.v1';
  operation_id: string;
  collection_id: string;
} & (
  | { action: 'create' | 'update'; collection: LiteratureCollection }
  | { action: 'delete' }
  | { action: 'membership'; item_id: string; included: boolean }
);

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const identity = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 512 && value.trim() === value;
const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const fail = (): never => {
  throw new Error('Invalid literature collection contract');
};
const keys = (value: Record<string, unknown>, allowed: string[]) =>
  Reflect.ownKeys(value).every((key) => typeof key === 'string' && allowed.includes(key));

/** The reference bounds normalized text in UTF-16 units, including astral names. */
export function normalizeLiteratureCollectionText(value: string, description = false): string {
  // A lone surrogate cannot round-trip through the Web server's UTF-8 JSON.
  // Reject it before reserving an operation whose receipt could never match.
  if (/[\uD800-\uDFFF]/u.test(value)) return fail();
  const normalized = value.normalize('NFKC').replace(/\s+/gu, ' ').trim();
  if ((!description && !normalized) || normalized.length > (description ? 1000 : 200)) return fail();
  return normalized;
}

export function decodeLiteratureCollection(value: unknown): LiteratureCollection {
  if (
    !record(value) ||
    !identity(value.id) ||
    !count(value.revision) ||
    value.revision < 1 ||
    typeof value.name !== 'string' ||
    typeof value.description !== 'string' ||
    !(value.parent_id === null || (identity(value.parent_id) && value.parent_id !== value.id)) ||
    !count(value.sort_order) ||
    !count(value.item_count) ||
    normalizeLiteratureCollectionText(value.name) !== value.name ||
    normalizeLiteratureCollectionText(value.description, true) !== value.description
  )
    return fail();
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    description: value.description,
    parent_id: typeof value.parent_id === 'string' ? value.parent_id : null,
    sort_order: value.sort_order,
    item_count: value.item_count,
  };
}

export function decodeLiteratureCollectionPage(value: unknown): LiteratureCollectionPage {
  if (!record(value) || value.schema !== 'x-science.literature-collections.v1' || !Array.isArray(value.collections))
    return fail();
  const collections = value.collections.map(decodeLiteratureCollection);
  const byId = new Map(collections.map((item) => [item.id, item]));
  if (byId.size !== collections.length) return fail();
  const visited = new Set<string>();
  for (const item of collections) {
    const path = new Set<string>();
    let current: LiteratureCollection | undefined = item;
    while (current && !visited.has(current.id)) {
      if (path.has(current.id)) return fail();
      path.add(current.id);
      if (current.parent_id === null) break;
      current = byId.get(current.parent_id);
      if (!current) return fail();
    }
    for (const id of path) visited.add(id);
  }
  return { schema: value.schema, collections };
}

export function decodeLiteratureCollectionMembership(value: unknown, itemId: string): LiteratureCollectionMembership {
  if (
    !record(value) ||
    value.schema !== 'x-science.literature-collection-membership.v1' ||
    value.item_id !== itemId ||
    !Array.isArray(value.collection_ids) ||
    !value.collection_ids.every(identity) ||
    new Set(value.collection_ids).size !== value.collection_ids.length
  )
    return fail();
  return { schema: value.schema, item_id: itemId, collection_ids: [...value.collection_ids] };
}

export function decodeLiteratureCollectionCommand(value: unknown): LiteratureCollectionCommand {
  if (
    !record(value) ||
    typeof value.operation_id !== 'string' ||
    !/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(value.operation_id)
  )
    return fail();
  const base = { operation_id: value.operation_id };
  if (value.kind === 'collection-create') {
    if (
      !keys(value, ['kind', 'operation_id', 'name', 'description', 'parent_id']) ||
      typeof value.name !== 'string' ||
      (value.description !== undefined && typeof value.description !== 'string') ||
      (value.parent_id !== undefined && !identity(value.parent_id))
    )
      return fail();
    return {
      ...base,
      kind: value.kind,
      name: normalizeLiteratureCollectionText(value.name),
      description: normalizeLiteratureCollectionText(
        typeof value.description === 'string' ? value.description : '',
        true
      ),
      ...(typeof value.parent_id === 'string' ? { parent_id: value.parent_id } : {}),
    };
  }
  if (!identity(value.collection_id)) return fail();
  const scoped = { ...base, collection_id: value.collection_id };
  if (value.kind === 'collection-update') {
    if (
      !keys(value, ['kind', 'operation_id', 'collection_id', 'expected_revision', 'name', 'description']) ||
      !count(value.expected_revision) ||
      value.expected_revision < 1 ||
      typeof value.name !== 'string' ||
      (value.description !== undefined && typeof value.description !== 'string')
    )
      return fail();
    return {
      ...scoped,
      kind: value.kind,
      expected_revision: value.expected_revision,
      name: normalizeLiteratureCollectionText(value.name),
      description: normalizeLiteratureCollectionText(
        typeof value.description === 'string' ? value.description : '',
        true
      ),
    };
  }
  if (value.kind === 'collection-delete' && keys(value, ['kind', 'operation_id', 'collection_id']))
    return { ...scoped, kind: value.kind };
  if (
    value.kind === 'collection-membership' &&
    keys(value, ['kind', 'operation_id', 'collection_id', 'item_id', 'included']) &&
    identity(value.item_id) &&
    typeof value.included === 'boolean'
  )
    return { ...scoped, kind: value.kind, item_id: value.item_id, included: value.included };
  return fail();
}

export function decodeLiteratureCollectionReceipt(
  value: unknown,
  input: LiteratureCollectionCommand
): LiteratureCollectionReceipt {
  const command = decodeLiteratureCollectionCommand(input);
  if (
    !record(value) ||
    value.schema !== 'x-science.literature-collection-receipt.v1' ||
    value.operation_id !== command.operation_id ||
    !identity(value.collection_id) ||
    value.action !== command.kind.slice('collection-'.length) ||
    ('collection_id' in command && value.collection_id !== command.collection_id)
  )
    return fail();
  const base = {
    schema: value.schema,
    operation_id: command.operation_id,
    collection_id: value.collection_id,
  } as const;
  if (command.kind === 'collection-create' || command.kind === 'collection-update') {
    const collection = decodeLiteratureCollection(value.collection);
    if (
      collection.id !== value.collection_id ||
      collection.name !== command.name ||
      collection.description !== command.description ||
      collection.revision !== (command.kind === 'collection-create' ? 1 : command.expected_revision + 1) ||
      (command.kind === 'collection-create' && collection.parent_id !== (command.parent_id ?? null))
    )
      return fail();
    return { ...base, action: command.kind === 'collection-create' ? 'create' : 'update', collection };
  }
  if (command.kind === 'collection-delete') return { ...base, action: 'delete' };
  if (value.item_id !== command.item_id || value.included !== command.included) return fail();
  return { ...base, action: 'membership', item_id: command.item_id, included: command.included };
}
