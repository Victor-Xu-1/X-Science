export const LIBRARY_COLUMN_IDS = ['readingStatus', 'year', 'authors', 'doi', 'attachments'] as const;

export type LibraryColumnId = (typeof LIBRARY_COLUMN_IDS)[number];
export type LibraryColumnPreferences = Readonly<{
  version: 1;
  order: readonly LibraryColumnId[];
  visible: readonly LibraryColumnId[];
}>;
type LibraryColumnWireId = LibraryColumnId | 'notes';
export type LibraryColumnPreferencesV2 = Readonly<{
  version: 2;
  order: readonly LibraryColumnWireId[];
  visible: readonly LibraryColumnWireId[];
}>;
export type LibraryColumnPreferencesWire = LibraryColumnPreferences | LibraryColumnPreferencesV2;
const LIBRARY_COLUMN_V2_IDS = [...LIBRARY_COLUMN_IDS, 'notes'] as const;

export const DEFAULT_LIBRARY_COLUMN_PREFERENCES: LibraryColumnPreferences = Object.freeze({
  version: 1,
  order: Object.freeze([...LIBRARY_COLUMN_IDS]),
  visible: Object.freeze(['readingStatus', 'year', 'authors', 'attachments'] as LibraryColumnId[]),
});

function columns<Id extends string>(value: unknown, complete: boolean, ids: readonly Id[]): value is Id[] {
  return (
    Array.isArray(value) &&
    value.length <= ids.length &&
    (!complete || value.length === ids.length) &&
    Array.from(value).every((id) => typeof id === 'string' && ids.includes(id as Id)) &&
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
    !columns(record.order, true, LIBRARY_COLUMN_IDS) ||
    !columns(record.visible, false, LIBRARY_COLUMN_IDS)
  )
    return null;
  const visible = record.visible;
  return Object.freeze({
    version: 1,
    order: Object.freeze([...record.order]),
    visible: Object.freeze(record.order.filter((id) => visible.includes(id))),
  });
}

/** A compatible reader accepts V2 without changing the closed V1 UI contract. */
export function parseLibraryColumnPreferencesWire(value: unknown): LibraryColumnPreferencesWire | null {
  const legacy = parseLibraryColumnPreferences(value);
  if (legacy) return legacy;
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  if (
    Object.keys(record).length !== 3 ||
    !Object.hasOwn(record, 'version') ||
    !Object.hasOwn(record, 'order') ||
    !Object.hasOwn(record, 'visible') ||
    record.version !== 2 ||
    !columns(record.order, true, LIBRARY_COLUMN_V2_IDS) ||
    !columns(record.visible, false, LIBRARY_COLUMN_V2_IDS)
  )
    return null;
  const visible = record.visible;
  return Object.freeze({
    version: 2,
    order: Object.freeze([...record.order]),
    visible: Object.freeze(record.order.filter((id) => visible.includes(id))),
  });
}

/** Presentation only: unsupported Notes never creates an empty menu/table column. */
export function projectLibraryColumnPreferences(value: LibraryColumnPreferencesWire): LibraryColumnPreferences {
  if (value.version === 1) return value;
  const supported = (id: LibraryColumnWireId): id is LibraryColumnId => id !== 'notes';
  return Object.freeze({
    version: 1,
    order: Object.freeze(value.order.filter(supported)),
    visible: Object.freeze(value.visible.filter(supported)),
  });
}

/** Apply supported-column edits without promoting V1 or discarding a received V2. */
export function liftLibraryColumnPreferences(
  base: LibraryColumnPreferencesWire,
  supported: LibraryColumnPreferences
): LibraryColumnPreferencesWire {
  if (base.version === 1)
    return Object.freeze({
      version: 1,
      order: Object.freeze([...supported.order]),
      visible: Object.freeze(supported.order.filter((id) => supported.visible.includes(id))),
    });
  let next = 0;
  const order = base.order.map((id) => (id === 'notes' ? id : supported.order[next++]));
  return Object.freeze({
    version: 2,
    order: Object.freeze(order),
    visible: Object.freeze(
      order.filter((id) => (id === 'notes' ? base.visible.includes(id) : supported.visible.includes(id)))
    ),
  });
}

/** Explicit presentation fallback only. Callers retain invalid/read-error state separately. */
export function normalizeLibraryColumnPreferences(value: unknown): LibraryColumnPreferences {
  return parseLibraryColumnPreferences(value) ?? DEFAULT_LIBRARY_COLUMN_PREFERENCES;
}
