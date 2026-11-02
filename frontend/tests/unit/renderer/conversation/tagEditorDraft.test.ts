import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  decodeTagEditorDraft,
  readTagEditorDraft,
  writeTagEditorDraft,
  type TagEditorDraft,
} from '@/renderer/pages/settings/tags/tagEditorDraft';
import { moveResourceTag } from '@/renderer/pages/settings/tags/tagReorder';
const op = '00000000-0000-4000-8000-000000000001';
const fields = { name: 'Study', icon_key: 'book-open', color_key: 'green' } as const;
const baseline = { name: '', icon_key: 'tag', color_key: 'blue' } as const;
let values: Map<string, string>;
beforeEach(() => {
  values = new Map();
  vi.stubGlobal('sessionStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
});
afterEach(() => vi.unstubAllGlobals());
describe('tag editor scoped draft and original intent recovery', () => {
  it('restores a chosen presentation and frozen original create under only its original owner and editor', () => {
    const draft: TagEditorDraft = {
      fields,
      baseline,
      base_version: null,
      intent: { kind: 'create', operation_id: op, ...fields },
    } as const;
    writeTagEditorDraft('owner-a', undefined, draft);
    expect(readTagEditorDraft('owner-a')).toEqual(draft);
    expect(readTagEditorDraft('owner-b')).toBeNull();
    expect(readTagEditorDraft('owner-a', 'tag-a')).toBeNull();
    writeTagEditorDraft('owner-a', undefined, null);
    expect(values.size).toBe(0);
  });
  it('keeps the original CAS with the exact draft and rejects changed payload or another tag identity', () => {
    const draft = {
      fields,
      baseline: fields,
      base_version: 7,
      intent: { kind: 'update', operation_id: op, tag_id: 'tag-a', expected_updated_at: 7, ...fields },
    } as const;
    expect(decodeTagEditorDraft(draft, 'tag-a')).toEqual(draft);
    expect(() => decodeTagEditorDraft(draft, 'tag-b')).toThrow();
    expect(() => decodeTagEditorDraft({ ...draft, base_version: 8 }, 'tag-a')).toThrow();
    expect(() =>
      decodeTagEditorDraft({ ...draft, base_version: 8_640_000_000_000_000, intent: null }, 'tag-a')
    ).toThrow();
    expect(() => decodeTagEditorDraft({ fields, baseline, base_version: 7, intent: null })).toThrow();
    expect(() => decodeTagEditorDraft({ ...draft, fields: { ...fields, name: 'Rebased' } }, 'tag-a')).toThrow();
  });
  it('does not treat corrupt recovery or injected command fields as accepted work or erase them on read', () => {
    const draft: TagEditorDraft = { fields, baseline, base_version: null, intent: null };
    writeTagEditorDraft('owner-a', undefined, draft);
    const key = [...values.keys()][0];
    values.set(key, '{broken');
    expect(() => readTagEditorDraft('owner-a')).toThrow();
    expect(values.get(key)).toBe('{broken');
    expect(() => decodeTagEditorDraft({ ...draft, snapshot: {} })).toThrow();
    expect(() =>
      decodeTagEditorDraft({ ...draft, intent: { kind: 'delete', tag_id: 'tag-a', operation_id: op } })
    ).toThrow();
  });
});
describe('complete custom Tag reorder intent', () => {
  it('moves both directions and edges without dropping IDs or adding the protected Favorite', () => {
    expect(moveResourceTag(['a', 'b', 'c'], 'a', 'c', 'after')).toEqual(['b', 'c', 'a']);
    expect(moveResourceTag(['a', 'b', 'c'], 'c', 'a', 'before')).toEqual(['c', 'a', 'b']);
    expect(moveResourceTag(['a', 'b', 'c'], 'a', 'b', 'before')).toEqual(['a', 'b', 'c']);
    expect(moveResourceTag(['a', 'b', 'c'], 'a', 'missing', 'after')).toEqual(['a', 'b', 'c']);
  });
});
