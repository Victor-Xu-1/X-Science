import { describe, expect, it } from 'vitest';
import {
  decodeLiteratureSelection,
  appendLiteratureScopeQuery,
  literatureSelectionScopeKey,
} from '@/common/adapter/literatureSelection';
import { decodeLiteraturePage } from '@/common/adapter/literatureLibrary';

describe('Complete Library selection wire and legacy page count', () => {
  it('accepts the complete membership and exact count without a page-sized cap', () => {
    const ids = Array.from({ length: 205 }, (_, index) => `item-${index}`);
    const value = decodeLiteratureSelection({
      schema: 'x-science.literature-selection.v1',
      item_ids: ids,
      total_count: 205,
    });
    ids.pop();
    expect(value.item_ids).toHaveLength(205);
    expect(Object.isFrozen(value.item_ids)).toBe(true);
    expect(
      decodeLiteratureSelection({ schema: 'x-science.literature-selection.v1', item_ids: [], total_count: 0 })
        .total_count
    ).toBe(0);
  });
  it('rejects partial counts, duplicates, invalid identifiers and a normal page', () => {
    const valid = { schema: 'x-science.literature-selection.v1', item_ids: ['a'], total_count: 1 };
    for (const value of [
      { ...valid, total_count: 2 },
      { ...valid, item_ids: ['a', 'a'], total_count: 2 },
      { ...valid, item_ids: [' a'] },
      { ...valid, next_cursor: 'page' },
      { ...valid, total_count: 1.5 },
      { schema: 'x-science.literature-page.v1', items: [] as unknown[], next_cursor: 'more' },
    ])
      expect(() => decodeLiteratureSelection(value)).toThrow('Invalid Library selection');
  });
  it('shares the real filters without importing cursor, account or page state', () => {
    const params = new URLSearchParams({ all_item_ids: 'true' });
    appendLiteratureScopeQuery(params, {
      query: ' study ',
      projectId: 'p',
      collectionId: 'c',
      readingStatus: 'unread',
    });
    expect(Object.fromEntries(params)).toEqual({
      all_item_ids: 'true',
      query: 'study',
      project_id: 'p',
      collection_id: 'c',
      reading_status: 'unread',
    });
    expect(literatureSelectionScopeKey({ query: ' study ', projectId: 'p' })).toBe(
      literatureSelectionScopeKey({ query: 'study', projectId: 'p' })
    );
    expect(() => appendLiteratureScopeQuery(params, { readingStatus: 'invalid' as never })).toThrow();
  });
  it('preserves a missing historical count and rejects an inconsistent declared count', () => {
    const page = { schema: 'x-science.literature-page.v1', items: [] as unknown[] };
    expect(decodeLiteraturePage(page)).toEqual(page);
    expect(decodeLiteraturePage({ ...page, total_count: 205 }).total_count).toBe(205);
    for (const count of [null, -1, 1.5, '205'])
      expect(() => decodeLiteraturePage({ ...page, total_count: count })).toThrow();
  });
});
