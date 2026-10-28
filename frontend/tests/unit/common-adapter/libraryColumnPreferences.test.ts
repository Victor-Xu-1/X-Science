import { describe, expect, it } from 'vitest';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES as defaults,
  LIBRARY_COLUMN_IDS,
  normalizeLibraryColumnPreferences,
  parseLibraryColumnPreferences,
  parseLibraryColumnPreferencesWire,
  projectLibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';

describe('Library column preference wire', () => {
  it('reads a strict six-ID V2 while keeping the V1 parser and five-column presentation closed', () => {
    const stored = {
      version: 2,
      order: ['readingStatus', 'notes', 'doi', 'year', 'authors', 'attachments'],
      visible: ['notes', 'readingStatus', 'year'],
    };
    const decoded = parseLibraryColumnPreferencesWire(stored);
    expect(decoded).toEqual({ ...stored, visible: ['readingStatus', 'notes', 'year'] });
    expect(parseLibraryColumnPreferences(stored)).toBeNull();
    expect(parseLibraryColumnPreferencesWire(defaults)).toEqual(defaults);
    expect(projectLibraryColumnPreferences(decoded!)).toEqual({
      version: 1,
      order: ['readingStatus', 'doi', 'year', 'authors', 'attachments'],
      visible: ['readingStatus', 'year'],
    });
    expect(stored.visible).toEqual(['notes', 'readingStatus', 'year']);
    expect(Object.isFrozen(decoded)).toBe(true);
    expect(Object.isFrozen(decoded?.order)).toBe(true);
    for (const malformed of [
      { ...stored, version: 1 },
      { ...stored, version: 3 },
      { ...stored, version: '2' },
      { ...stored, order: [...LIBRARY_COLUMN_IDS] },
      { ...stored, order: ['notes', 'notes', 'doi', 'year', 'authors', 'attachments'] },
      { ...stored, order: [...stored.order, 'notes'] },
      { ...stored, order: ['title', ...stored.order.slice(1)] },
      { ...stored, visible: ['notes', 'notes'] },
      { ...stored, visible: ['title'] },
      { ...stored, visible: Array(1) },
      { ...stored, visible: null },
      { ...stored, extra: true },
      { version: 2, order: stored.order },
    ])
      expect(parseLibraryColumnPreferencesWire(malformed)).toBeNull();
  });

  it('keeps title outside a closed bounded v1 wire and rejects corrupt configurations', () => {
    expect(parseLibraryColumnPreferences(defaults)).toEqual(defaults);
    expect(
      parseLibraryColumnPreferences({ version: 1, order: [...LIBRARY_COLUMN_IDS].reverse(), visible: [] })
    ).toEqual({ version: 1, order: [...LIBRARY_COLUMN_IDS].reverse(), visible: [] });
    for (const value of [
      undefined,
      null,
      [],
      {},
      { ...defaults, version: 2 },
      { ...defaults, title: false },
      { ...defaults, order: ['year'] },
      { ...defaults, order: [...LIBRARY_COLUMN_IDS, 'year'] },
      { ...defaults, order: ['year', 'year', 'authors', 'doi', 'attachments'] },
      { ...defaults, visible: ['title'] },
      { ...defaults, visible: ['year', 'year'] },
      { ...defaults, visible: null },
      { ...defaults, visible: Array(1) },
    ])
      expect(parseLibraryColumnPreferences(value)).toBeNull();
    expect(normalizeLibraryColumnPreferences(null)).toBe(defaults);
    const input = { version: 1, order: [...LIBRARY_COLUMN_IDS], visible: ['attachments', 'year'] };
    const parsed = parseLibraryColumnPreferences(input);
    expect(parsed?.visible).toEqual(['year', 'attachments']);
    expect(input.visible).toEqual(['attachments', 'year']);
    expect(Object.isFrozen(parsed?.order)).toBe(true);
  });
});
