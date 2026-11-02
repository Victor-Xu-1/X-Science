import {
  decodeResourceTagCommand,
  RESOURCE_TAG_COLOR_KEYS,
  RESOURCE_TAG_ICON_KEYS,
  RESOURCE_TAG_TIMESTAMP_MAX,
  type ResourceTagCommand,
} from '@/common/adapter/resourceTags';
import type { ResourceTagFields } from '@/renderer/services/runtime/resourceTagOwner';

export type TagEditorDraft = Readonly<{
  fields: ResourceTagFields;
  baseline: ResourceTagFields;
  base_version: number | null;
  intent: ResourceTagCommand | null;
}>;
const key = (ownerId: string, tagId?: string) =>
  `x-science.tag-editor-draft.v1:${encodeURIComponent(ownerId)}:${encodeURIComponent(tagId ?? 'new')}`;
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const fail = (): never => {
  throw new Error('Invalid tag editor recovery');
};
const fields = (value: unknown): ResourceTagFields => {
  if (
    !object(value) ||
    Object.keys(value).toSorted().join() !== 'color_key,icon_key,name' ||
    typeof value.name !== 'string' ||
    value.name.length > 64 ||
    /[\uD800-\uDFFF]/u.test(value.name) ||
    !RESOURCE_TAG_ICON_KEYS.includes(value.icon_key as never) ||
    !RESOURCE_TAG_COLOR_KEYS.includes(value.color_key as never)
  )
    return fail();
  return {
    name: value.name,
    icon_key: value.icon_key as ResourceTagFields['icon_key'],
    color_key: value.color_key as ResourceTagFields['color_key'],
  };
};
export function decodeTagEditorDraft(value: unknown, tagId?: string): TagEditorDraft {
  if (!object(value) || Object.keys(value).toSorted().join() !== 'base_version,baseline,fields,intent') return fail();
  if (
    value.base_version !== null &&
    (typeof value.base_version !== 'number' || !Number.isSafeInteger(value.base_version) || value.base_version < 0)
  )
    return fail();
  if (
    tagId
      ? value.base_version === null ||
        value.base_version === 0 ||
        Number(value.base_version) >= RESOURCE_TAG_TIMESTAMP_MAX
      : value.base_version !== null
  )
    return fail();
  const draft = fields(value.fields),
    baseline = fields(value.baseline);
  const intent = value.intent === null ? null : decodeResourceTagCommand(value.intent);
  if (intent) {
    if (intent.kind !== (tagId ? 'update' : 'create')) return fail();
    if (intent.kind === 'update' && (intent.tag_id !== tagId || intent.expected_updated_at !== value.base_version))
      return fail();
    if (
      (intent.kind !== 'create' && intent.kind !== 'update') ||
      JSON.stringify({ name: intent.name, icon_key: intent.icon_key, color_key: intent.color_key }) !==
        JSON.stringify(draft)
    )
      return fail();
  }
  return { fields: draft, baseline, base_version: value.base_version as number | null, intent };
}
/** UI recovery only. A saved intent is replayed solely through the existing durable mutation owner. */
export function readTagEditorDraft(ownerId: string, tagId?: string): TagEditorDraft | null {
  const raw = sessionStorage.getItem(key(ownerId, tagId));
  if (raw === null) return null;
  if (raw.length > 8192) return fail();
  return decodeTagEditorDraft(JSON.parse(raw), tagId);
}
export function writeTagEditorDraft(ownerId: string, tagId: string | undefined, draft: TagEditorDraft | null) {
  const storageKey = key(ownerId, tagId);
  if (draft === null) sessionStorage.removeItem(storageKey);
  else sessionStorage.setItem(storageKey, JSON.stringify(decodeTagEditorDraft(draft, tagId)));
}
