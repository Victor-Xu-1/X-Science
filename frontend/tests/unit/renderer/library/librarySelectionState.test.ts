import { describe, expect, it } from 'vitest';
import {
  EMPTY_LIBRARY_SELECTION,
  allMatchingLibrarySelection,
  explicitLibrarySelection,
  isLibraryItemSelected,
  libraryKnownRowRange,
  libraryPageSelection,
  librarySelectionCount,
  resolveLibrarySelection,
  setLibrarySelectionItems,
  toggleLibraryPageSelection,
  toggleLibrarySelection,
} from '@/renderer/pages/library/librarySelectionState';

describe('Library selection model', () => {
  it('freezes independent deduplicated snapshots without mutating previous selections', () => {
    const original = ['a', 'a', 'b'];
    const selected = explicitLibrarySelection(original);
    original.push('c');
    expect(selected.selectedIds).toEqual(['a', 'b']);
    expect(Object.isFrozen(selected)).toBe(true);
    expect(Object.isFrozen(selected.selectedIds)).toBe(true);
    expect(Object.isFrozen(selected.excludedMatchingIds)).toBe(true);
    expect(toggleLibrarySelection(selected, 'b').selectedIds).toEqual(['a']);
    expect(toggleLibrarySelection(selected, 'c').selectedIds).toEqual(['a', 'b', 'c']);
    expect(selected.selectedIds).toEqual(['a', 'b']);
    expect(librarySelectionCount(selected)).toBe(2);
  });

  it('derives page mixed state and follows explicit replace/whole-selection clear semantics', () => {
    const partial = explicitLibrarySelection(['a', 'outside']);
    expect(libraryPageSelection(partial, ['a', 'b', 'a'])).toEqual({ checked: false, mixed: true, count: 1 });
    const page = toggleLibraryPageSelection(partial, ['a', 'b', 'a']);
    expect(page.selectedIds).toEqual(['a', 'b']);
    expect(libraryPageSelection(page, ['a', 'b'])).toEqual({ checked: true, mixed: false, count: 2 });
    expect(toggleLibraryPageSelection(page, ['a', 'b'])).toBe(EMPTY_LIBRARY_SELECTION);
    expect(toggleLibraryPageSelection(allMatchingLibrarySelection(['b']), ['a', 'b'])).toBe(EMPTY_LIBRARY_SELECTION);
    expect(libraryPageSelection(EMPTY_LIBRARY_SELECTION, [])).toEqual({ checked: false, mixed: false, count: 0 });
  });

  it('resolves all exclusions only against explicitly supplied complete membership and keeps totals honest', () => {
    const selection = allMatchingLibrarySelection(['b', 'e', 'b']);
    expect(selection.excludedMatchingIds).toEqual(['b', 'e']);
    expect(isLibraryItemSelected(selection, 'a')).toBe(true);
    expect(isLibraryItemSelected(selection, 'e')).toBe(false);
    expect(resolveLibrarySelection(selection, ['a', 'b', 'c', 'd', 'e', 'c'])).toEqual(['a', 'c', 'd']);
    expect(() => resolveLibrarySelection(selection)).toThrow('Complete Library membership');
    expect(resolveLibrarySelection(selection, [])).toEqual([]);
    expect(librarySelectionCount(selection)).toBeUndefined();
    expect(librarySelectionCount(selection, Number.NaN)).toBeUndefined();
    expect(librarySelectionCount(selection, 1)).toBeUndefined();
    expect(librarySelectionCount(selection, 5)).toBe(3);
    expect(toggleLibrarySelection(selection, 'e').excludedMatchingIds).toEqual(['b']);
    expect(resolveLibrarySelection(explicitLibrarySelection(['outside']))).toEqual(['outside']);
  });

  it('limits shift ranges to known rows and applies selection or exclusion without inventing membership', () => {
    const rows = ['a', 'b', 'c', 'd', 'b'];
    const range = libraryKnownRowRange(rows, 'd', 'b');
    expect(range).toEqual(['b', 'c', 'd']);
    expect(Object.isFrozen(range)).toBe(true);
    expect(libraryKnownRowRange(rows, 'missing', 'd')).toEqual([]);
    expect(setLibrarySelectionItems(explicitLibrarySelection(['a']), range, true).selectedIds).toEqual([
      'a',
      'b',
      'c',
      'd',
    ]);
    const excluded = setLibrarySelectionItems(allMatchingLibrarySelection(), range, false);
    expect(excluded.excludedMatchingIds).toEqual(['b', 'c', 'd']);
    expect(setLibrarySelectionItems(excluded, ['c'], true).excludedMatchingIds).toEqual(['b', 'd']);
    expect(rows).toEqual(['a', 'b', 'c', 'd', 'b']);
  });
});
