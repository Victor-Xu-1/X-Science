export const LIBRARY_COLUMN_IDS = ['readingStatus', 'year', 'authors', 'doi', 'attachments'] as const;

export type LibraryColumnId = (typeof LIBRARY_COLUMN_IDS)[number];
export type LibraryColumnPreferences = Readonly<{
  version: 1;
  order: readonly LibraryColumnId[];
  visible: readonly LibraryColumnId[];
}>;

export const DEFAULT_LIBRARY_COLUMN_PREFERENCES: LibraryColumnPreferences = Object.freeze({
  version: 1,
  order: Object.freeze([...LIBRARY_COLUMN_IDS]),
  visible: Object.freeze(['readingStatus', 'year', 'authors', 'attachments'] as LibraryColumnId[]),
});

function columns(value: unknown, complete: boolean): value is LibraryColumnId[] {
  return (
    Array.isArray(value) &&
    value.length <= LIBRARY_COLUMN_IDS.length &&
    (!complete || value.length === LIBRARY_COLUMN_IDS.length) &&
    Array.from(value).every((id) => typeof id === 'string' && LIBRARY_COLUMN_IDS.includes(id as LibraryColumnId)) &&
    new Set(value).size === value.length
  );
}

/** The saved v1 wire is closed and bounded; malformed evidence is never silently repaired. */
export function parseLibraryColumnPreferences(value: unknown): LibraryColumnPreferences | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  if (
    Object.keys(record).length !== 3 ||
    !Object.hasOwn(record, 'version') ||
    !Object.hasOwn(record, 'order') ||
    !Object.hasOwn(record, 'visible') ||
    record.version !== 1 ||
    !columns(record.order, true) ||
    !columns(record.visible, false)
  )
    return null;
  const visible = record.visible;
  return Object.freeze({
    version: 1,
    order: Object.freeze([...record.order]),
    visible: Object.freeze(record.order.filter((id) => visible.includes(id))),
  });
}

/** Explicit presentation fallback only. Callers retain invalid/read-error state separately. */
export function normalizeLibraryColumnPreferences(value: unknown): LibraryColumnPreferences {
  return parseLibraryColumnPreferences(value) ?? DEFAULT_LIBRARY_COLUMN_PREFERENCES;
}
