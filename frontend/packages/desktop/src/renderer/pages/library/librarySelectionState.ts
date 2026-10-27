export type LibrarySelectionState = Readonly<{
  selectedIds: readonly string[];
  allMatchingSelected: boolean;
  excludedMatchingIds: readonly string[];
}>;

const unique = (ids: Iterable<string>): readonly string[] => Object.freeze([...new Set(ids)]);
const snapshot = (selected: Iterable<string>, all: boolean, excluded: Iterable<string>): LibrarySelectionState =>
  Object.freeze({
    selectedIds: unique(selected),
    allMatchingSelected: all,
    excludedMatchingIds: unique(excluded),
  });

export const EMPTY_LIBRARY_SELECTION = snapshot([], false, []);
export const explicitLibrarySelection = (ids: Iterable<string>): LibrarySelectionState => snapshot(ids, false, []);
export const allMatchingLibrarySelection = (excluded: Iterable<string> = []): LibrarySelectionState =>
  snapshot([], true, excluded);

export function isLibraryItemSelected(selection: LibrarySelectionState, id: string): boolean {
  return selection.allMatchingSelected
    ? !selection.excludedMatchingIds.includes(id)
    : selection.selectedIds.includes(id);
}

export function setLibrarySelectionItems(
  selection: LibrarySelectionState,
  ids: Iterable<string>,
  selected: boolean
): LibrarySelectionState {
  const next = new Set(selection.allMatchingSelected ? selection.excludedMatchingIds : selection.selectedIds);
  for (const id of ids) {
    if (selected !== selection.allMatchingSelected) next.add(id);
    else next.delete(id);
  }
  return selection.allMatchingSelected ? allMatchingLibrarySelection(next) : explicitLibrarySelection(next);
}

export function toggleLibrarySelection(selection: LibrarySelectionState, id: string): LibrarySelectionState {
  return setLibrarySelectionItems(selection, [id], !isLibraryItemSelected(selection, id));
}

export function libraryPageSelection(selection: LibrarySelectionState, rowIds: readonly string[]) {
  const ids = unique(rowIds);
  const count = ids.filter((id) => isLibraryItemSelected(selection, id)).length;
  return { checked: ids.length > 0 && count === ids.length, mixed: count > 0 && count < ids.length, count };
}

/** The reference page checkbox clears an all/fully selected set; otherwise it replaces it. */
export function toggleLibraryPageSelection(
  selection: LibrarySelectionState,
  rowIds: readonly string[]
): LibrarySelectionState {
  return selection.allMatchingSelected || libraryPageSelection(selection, rowIds).checked
    ? EMPTY_LIBRARY_SELECTION
    : explicitLibrarySelection(rowIds);
}

/** A range is only meaningful within these known rows; missing endpoints do not imply all. */
export function libraryKnownRowRange(rowIds: readonly string[], fromId: string, toId: string): readonly string[] {
  const ids = unique(rowIds);
  const from = ids.indexOf(fromId);
  const to = ids.indexOf(toId);
  return from < 0 || to < 0 ? Object.freeze([]) : Object.freeze(ids.slice(Math.min(from, to), Math.max(from, to) + 1));
}

/** Call with complete IDs from the typed membership read, never with a displayed page. */
export function resolveLibrarySelection(
  selection: LibrarySelectionState,
  completeItemIds?: readonly string[]
): readonly string[] {
  if (!selection.allMatchingSelected) return selection.selectedIds;
  if (completeItemIds === undefined) throw new Error('Complete Library membership is required');
  const excluded = new Set(selection.excludedMatchingIds);
  return Object.freeze(unique(completeItemIds).filter((id) => !excluded.has(id)));
}

/** Unknown/inconsistent all-mode totals stay unknown, not a fabricated zero. */
export function librarySelectionCount(selection: LibrarySelectionState, totalMatching?: number): number | undefined {
  if (!selection.allMatchingSelected) return selection.selectedIds.length;
  return typeof totalMatching === 'number' &&
    Number.isSafeInteger(totalMatching) &&
    totalMatching >= selection.excludedMatchingIds.length
    ? totalMatching - selection.excludedMatchingIds.length
    : undefined;
}
