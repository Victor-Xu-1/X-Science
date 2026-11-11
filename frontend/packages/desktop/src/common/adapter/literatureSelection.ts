import {
  decodeLiteratureReadingStatus,
  isLiteratureReadingStatusItemId,
  type LiteratureReadingStatus,
} from './literatureReadingStatus';
import { decodeLiteratureLifecycle, type LiteratureLifecycle } from './literatureLifecycle';

/** The same explicit, owner-authorized filters used by the catalogue read. */
export type LiteratureSelectionScope = Readonly<{
  query: string;
  projectId: string;
  collectionId: string;
  readingStatus: LiteratureReadingStatus | '';
  lifecycle?: LiteratureLifecycle;
}>;
export type LiteratureSelection = Readonly<{
  schema: 'x-science.literature-selection.v1';
  item_ids: readonly string[];
  total_count: number;
}>;
export type LiteratureRestoreTarget = Readonly<{ id: string; metadata_revision: number }>;
export type LiteratureRestorePreview = Readonly<{
  schema: 'x-science.literature-restore-preview.v1';
  items: readonly LiteratureRestoreTarget[];
  total_count: number;
}>;
export type LiteratureScopeInput = {
  query?: string;
  projectId?: string;
  collectionId?: string;
  readingStatus?: LiteratureReadingStatus | '';
  lifecycle?: LiteratureLifecycle;
};

export function normalizeLiteratureSelectionScope(input: LiteratureScopeInput): LiteratureSelectionScope {
  return Object.freeze({
    query: input.query?.trim() ?? '',
    projectId: input.projectId ?? '',
    collectionId: input.collectionId ?? '',
    readingStatus:
      input.readingStatus === undefined || input.readingStatus === ''
        ? ''
        : decodeLiteratureReadingStatus(input.readingStatus),
    ...(decodeLiteratureLifecycle(input.lifecycle) === 'deleted' ? { lifecycle: 'deleted' as const } : {}),
  });
}

export function appendLiteratureScopeQuery(params: URLSearchParams, input: LiteratureScopeInput): void {
  const scope = normalizeLiteratureSelectionScope(input);
  if (scope.query) params.set('query', scope.query);
  if (scope.projectId) params.set('project_id', scope.projectId);
  if (scope.collectionId) params.set('collection_id', scope.collectionId);
  if (scope.readingStatus) params.set('reading_status', scope.readingStatus);
  if (scope.lifecycle === 'deleted') params.set('lifecycle', 'deleted');
}

/** A label/fence for a visit, never a substitute for backend authorization or membership. */
export function literatureSelectionScopeKey(input: LiteratureScopeInput): string {
  const scope = normalizeLiteratureSelectionScope(input);
  return JSON.stringify([
    scope.query,
    scope.projectId,
    scope.collectionId,
    scope.readingStatus,
    ...(scope.lifecycle === 'deleted' ? ['deleted'] : []),
  ]);
}

/** An all-ID read is complete or rejected; a page or truncated response cannot masquerade as all. */
export function decodeLiteratureSelection(value: unknown): LiteratureSelection {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid Library selection');
  const data = value as Record<string, unknown>;
  const keys = Reflect.ownKeys(data);
  if (
    keys.length !== 3 ||
    !keys.every((key) => typeof key === 'string' && ['schema', 'item_ids', 'total_count'].includes(key)) ||
    data.schema !== 'x-science.literature-selection.v1' ||
    !Array.isArray(data.item_ids) ||
    !data.item_ids.every(isLiteratureReadingStatusItemId) ||
    new Set(data.item_ids).size !== data.item_ids.length ||
    typeof data.total_count !== 'number' ||
    !Number.isSafeInteger(data.total_count) ||
    data.total_count !== data.item_ids.length
  )
    throw new Error('Invalid Library selection');
  return Object.freeze({
    schema: data.schema,
    item_ids: Object.freeze([...data.item_ids]),
    total_count: data.total_count,
  });
}

const invalidRestorePreview = (): never => {
  throw new Error('Invalid Library restore preview');
};

/** Complete deleted membership and CAS revisions from one authorized snapshot. */
export function decodeLiteratureRestorePreview(value: unknown): LiteratureRestorePreview {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalidRestorePreview();
  const data = value as Record<string, unknown>;
  if (
    Reflect.ownKeys(data).length !== 3 ||
    !Reflect.ownKeys(data).every(
      (key) => typeof key === 'string' && ['schema', 'items', 'total_count'].includes(key)
    ) ||
    data.schema !== 'x-science.literature-restore-preview.v1' ||
    !Array.isArray(data.items) ||
    !Number.isSafeInteger(data.total_count) ||
    data.total_count !== data.items.length
  )
    return invalidRestorePreview();
  const ids = new Set<string>();
  const items = data.items.map((candidate): LiteratureRestoreTarget => {
    if (!candidate || typeof candidate !== 'object' || Array.isArray(candidate)) return invalidRestorePreview();
    const row = candidate as Record<string, unknown>;
    if (
      Reflect.ownKeys(row).length !== 2 ||
      !Object.hasOwn(row, 'id') ||
      !Object.hasOwn(row, 'metadata_revision') ||
      !isLiteratureReadingStatusItemId(row.id) ||
      ids.has(row.id) ||
      typeof row.metadata_revision !== 'number' ||
      !Number.isSafeInteger(row.metadata_revision) ||
      row.metadata_revision < 1 ||
      row.metadata_revision === Number.MAX_SAFE_INTEGER
    )
      return invalidRestorePreview();
    ids.add(row.id);
    return Object.freeze({ id: row.id, metadata_revision: row.metadata_revision });
  });
  return Object.freeze({
    schema: 'x-science.literature-restore-preview.v1',
    items: Object.freeze(items),
    total_count: items.length,
  });
}
