import { describe, expect, it } from 'vitest';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES as defaults,
  LIBRARY_COLUMN_IDS,
  normalizeLibraryColumnPreferences,
  parseLibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';

describe('Library column preference wire', () => {
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
