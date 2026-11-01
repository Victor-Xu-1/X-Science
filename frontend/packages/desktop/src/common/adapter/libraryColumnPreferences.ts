export const LIBRARY_COLUMN_IDS = ['readingStatus', 'year', 'authors', 'doi', 'attachments'] as const;

export type LibraryColumnId = (typeof LIBRARY_COLUMN_IDS)[number];
export type LibraryColumnPreferences = Readonly<{
  version: 1;
  order: readonly LibraryColumnId[];
  visible: readonly LibraryColumnId[];
}>;
export type LibraryColumnV2Id = LibraryColumnId | 'notes';
export type LibraryColumnWireId = LibraryColumnV2Id | 'rating';
export type LibraryColumnPreferencesV2 = Readonly<{
  version: 2;
  order: readonly LibraryColumnV2Id[];
  visible: readonly LibraryColumnV2Id[];
}>;
export type LibraryColumnPreferencesV3 = Readonly<{
  version: 3;
  order: readonly LibraryColumnWireId[];
  visible: readonly LibraryColumnWireId[];
}>;
export type LibraryColumnPreferencesWire =
  | LibraryColumnPreferences
  | LibraryColumnPreferencesV2
  | LibraryColumnPreferencesV3;
export const LIBRARY_COLUMN_V2_IDS = [...LIBRARY_COLUMN_IDS, 'notes'] as const;
export const LIBRARY_COLUMN_V3_IDS = [...LIBRARY_COLUMN_V2_IDS, 'rating'] as const;

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

/** Each wire version stays closed; a newer reader never rewrites a saved layout. */
export function parseLibraryColumnPreferencesWire(value: unknown): LibraryColumnPreferencesWire | null {
  const legacy = parseLibraryColumnPreferences(value);
  if (legacy) return legacy;
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const ids = record.version === 2 ? LIBRARY_COLUMN_V2_IDS : LIBRARY_COLUMN_V3_IDS;
  if (
    Object.keys(record).length !== 3 ||
    !Object.hasOwn(record, 'version') ||
    !Object.hasOwn(record, 'order') ||
    !Object.hasOwn(record, 'visible') ||
    (record.version !== 2 && record.version !== 3) ||
    !columns(record.order, true, ids) ||
    !columns(record.visible, false, ids)
  )
    return null;
  const visible = record.visible;
  return Object.freeze({
    version: record.version,
    order: Object.freeze([...record.order]),
    visible: Object.freeze(record.order.filter((id) => visible.includes(id))),
  }) as LibraryColumnPreferencesWire;
}

/** Presentation only: unsupported Notes never creates an empty menu/table column. */
export function projectLibraryColumnPreferences(value: LibraryColumnPreferencesWire): LibraryColumnPreferences {
  if (value.version === 1) return value;
  const supported = (id: LibraryColumnWireId): id is LibraryColumnId => id !== 'notes' && id !== 'rating';
  return Object.freeze({
    version: 1,
    order: Object.freeze([...value.order].filter(supported)),
    visible: Object.freeze([...value.visible].filter(supported)),
  });
}

/** Notes-capable presentation only; a legacy layout is never written merely by reading it. */
export function projectLibraryNotesColumns(value: LibraryColumnPreferencesWire): LibraryColumnPreferencesV2 {
  if (value.version === 2) return value;
  if (value.version === 3) {
    const supported = (id: LibraryColumnWireId): id is LibraryColumnV2Id => id !== 'rating';
    return Object.freeze({
      version: 2,
      order: Object.freeze(value.order.filter(supported)),
      visible: Object.freeze(value.visible.filter(supported)),
    });
  }
  return Object.freeze({
    version: 2,
    order: Object.freeze([...value.order, 'notes'] as LibraryColumnV2Id[]),
    visible: Object.freeze([...value.visible]),
  });
}

/** New columns start hidden; existing visibility/order is not changed by a read. */
export function projectLibraryRatingColumns(value: LibraryColumnPreferencesWire): LibraryColumnPreferencesV3 {
  if (value.version === 3) return value;
  const notes = projectLibraryNotesColumns(value);
  return Object.freeze({
    version: 3,
    order: Object.freeze([...notes.order, 'rating'] as LibraryColumnWireId[]),
    visible: Object.freeze([...notes.visible]),
  });
}

/** Keep unsupported columns in place when an older capable view edits its subset. */
function liftSupportedColumns(
  base: LibraryColumnPreferencesWire,
  supported: LibraryColumnPreferencesWire
): LibraryColumnPreferencesWire {
  if (base.version <= supported.version)
    return Object.freeze({
      version: supported.version,
      order: Object.freeze([...supported.order]),
      visible: Object.freeze(
        supported.order.filter((id) => (supported.visible as readonly LibraryColumnWireId[]).includes(id))
      ),
    }) as LibraryColumnPreferencesWire;
  const supportedIds = new Set<LibraryColumnWireId>(supported.order);
  let next = 0;
  const order = base.order.map((id) => (supportedIds.has(id) ? supported.order[next++] : id));
  return Object.freeze({
    version: base.version,
    order: Object.freeze(order),
    visible: Object.freeze(
      order.filter((id) =>
        supportedIds.has(id)
          ? (supported.visible as readonly LibraryColumnWireId[]).includes(id)
          : (base.visible as readonly LibraryColumnWireId[]).includes(id)
      )
    ),
  }) as LibraryColumnPreferencesWire;
}

/** Apply legacy edits without dropping newer Notes or Rating layout evidence. */
export function liftLibraryColumnPreferences(
  base: LibraryColumnPreferencesWire,
  supported: LibraryColumnPreferences
): LibraryColumnPreferencesWire {
  return liftSupportedColumns(base, supported);
}

export function liftLibraryNotesColumns(
  base: LibraryColumnPreferencesWire,
  supported: LibraryColumnPreferencesV2
): LibraryColumnPreferencesWire {
  return liftSupportedColumns(base, supported);
}

/** Explicit presentation fallback only. Callers retain invalid/read-error state separately. */
export function normalizeLibraryColumnPreferences(value: unknown): LibraryColumnPreferences {
  return parseLibraryColumnPreferences(value) ?? DEFAULT_LIBRARY_COLUMN_PREFERENCES;
}
