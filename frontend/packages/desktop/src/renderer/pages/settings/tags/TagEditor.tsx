import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { BookOpen, Bookmark, Bot, Code2, Database, FlaskConical, Star, Tag } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  RESOURCE_TAG_COLOR_KEYS,
  normalizeResourceTagName,
  decodeResourceTagCommand,
  type ResourceTagCommand,
  type ResourceTag,
} from '@/common/adapter/resourceTags';
import type { CustomResourceTag, ResourceTagFields } from '@/renderer/services/runtime/resourceTagOwner';
import { resourceTagDefiniteRejection } from '@/renderer/services/runtime/resourceTagMutationQueue';
import { ResourceTagVisual } from '../../library/ResourceTagBadges';
import { readTagEditorDraft, writeTagEditorDraft, type TagEditorDraft } from './tagEditorDraft';

const icons = {
  tag: Tag,
  star: Star,
  bookmark: Bookmark,
  'flask-conical': FlaskConical,
  'book-open': BookOpen,
  database: Database,
  'code-2': Code2,
  bot: Bot,
};
const empty: ResourceTagFields = { name: '', icon_key: 'tag', color_key: 'blue' };
const fields = (tag: CustomResourceTag): ResourceTagFields => ({
  name: tag.name,
  icon_key: tag.icon_key,
  color_key: tag.color_key,
});
export type TagEditorLeaveState = { dirty: boolean; busy: boolean };

/** Draft state never owns a request. One frozen submit intent is retained until its receipt/readback settles. */
export function TagEditor({
  ownerId,
  tag,
  tags,
  blocked,
  busy,
  submit,
  onCancel,
  onSaved,
  onLeaveChange,
}: {
  ownerId: string;
  tag?: CustomResourceTag;
  tags: readonly ResourceTag[];
  blocked: boolean;
  busy: boolean;
  submit(command: ResourceTagCommand): Promise<string | undefined>;
  onCancel(): void;
  onSaved(id: string): void;
  onLeaveChange(state: TagEditorLeaveState | null): void;
}) {
  const { t } = useTranslation();
  const [restored] = useState(() => {
    try {
      return { draft: readTagEditorDraft(ownerId, tag?.id), failed: false };
    } catch {
      return { draft: null, failed: true };
    }
  });
  const [recoveryFailed, setRecoveryFailed] = useState(restored.failed);
  const [baseVersion, setBaseVersion] = useState(restored.draft?.base_version ?? tag?.updated_at);
  const [baseline, setBaseline] = useState<ResourceTagFields>(
    () => restored.draft?.baseline ?? (tag ? fields(tag) : empty)
  );
  const [draft, setDraft] = useState<ResourceTagFields>(() => restored.draft?.fields ?? (tag ? fields(tag) : empty));
  const [saving, setSaving] = useState(false);
  const [uncertain, setUncertain] = useState(restored.draft?.intent !== null && restored.draft?.intent !== undefined);
  const [error, setError] = useState(false);
  const request = useRef<ResourceTagCommand | null>(restored.draft?.intent ?? null);
  const sending = useRef(false);
  const settled = useRef(false);
  const mounted = useRef(true);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const conflicted = tag !== undefined && tag.updated_at !== baseVersion && !uncertain;
  const dirty = JSON.stringify(draft) !== JSON.stringify(baseline);
  useLayoutEffect(() => {
    onLeaveChange({ dirty, busy: saving });
    return () => onLeaveChange(null);
  }, [dirty, saving, onLeaveChange]);
  let valid = true;
  let normalized = '';
  try {
    normalized = normalizeResourceTagName(draft.name);
  } catch {
    valid = false;
  }
  const duplicate =
    valid &&
    tags.some(
      (candidate) =>
        'name' in candidate && candidate.id !== tag?.id && candidate.name.toLowerCase() === normalized.toLowerCase()
    );
  const recovery = (intent = request.current): TagEditorDraft => ({
    fields: draft,
    baseline,
    base_version: baseVersion ?? null,
    intent,
  });
  useEffect(() => {
    if (settled.current || (restored.failed && !dirty && !request.current)) return;
    try {
      writeTagEditorDraft(ownerId, tag?.id, dirty || request.current ? recovery() : null);
      setRecoveryFailed(false);
    } catch {
      setRecoveryFailed(true);
    }
  }, [ownerId, tag?.id, draft, baseline, baseVersion, dirty, uncertain, saving]);
  const editable = !saving && !blocked && !uncertain;
  const save = async () => {
    if (sending.current || busy || (!request.current && (!valid || duplicate || conflicted || blocked))) return;
    const original =
      request.current ??
      decodeResourceTagCommand({
        kind: tag ? 'update' : 'create',
        operation_id: crypto.randomUUID(),
        ...(tag ? { tag_id: tag.id, expected_updated_at: baseVersion } : {}),
        ...draft,
      });
    try {
      writeTagEditorDraft(ownerId, tag?.id, recovery(original));
    } catch {
      setRecoveryFailed(true);
      return;
    }
    request.current = original;
    sending.current = true;
    setSaving(true);
    setError(false);
    try {
      const id = await submit(original);
      writeTagEditorDraft(ownerId, tag?.id, null);
      settled.current = true;
      if (!mounted.current) return;
      request.current = null;
      setUncertain(false);
      onLeaveChange(null);
      onSaved(tag?.id ?? id!);
    } catch (reason) {
      if (!mounted.current) return;
      const definite = resourceTagDefiniteRejection(reason) !== null;
      if (definite) request.current = null;
      setUncertain(!definite);
      setError(true);
    } finally {
      sending.current = false;
      if (mounted.current) setSaving(false);
    }
  };
  const latest = () => {
    if (!tag || saving || uncertain) return;
    const next = fields(tag);
    setDraft(next);
    setBaseline(next);
    setBaseVersion(tag.updated_at);
    setError(false);
  };
  return (
    <form
      className='tag-editor'
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <p className='tags-muted'>{t('settings.resourceTags.uniqueHint')}</p>
      {restored.draft && (
        <p role='status' className='tags-muted'>
          {t('settings.resourceTags.draftRestored')}
        </p>
      )}
      {recoveryFailed && (
        <p role='alert' className='tags-notice'>
          {t('settings.resourceTags.draftRecoveryFailed')}
        </p>
      )}
      {conflicted && (
        <section role='alert' className='tags-notice tags-notice--conflict'>
          <strong>{t('settings.resourceTags.versionChanged')}</strong>
          <p>{t('settings.resourceTags.keepDraftHint')}</p>
          {tag && <ResourceTagVisual tag={tag} favorite={t('conversation.resourceTags.favorite')} />}
          <div className='tags-actions'>
            <button type='button' className='settings-action-button' onClick={latest}>
              {t('settings.resourceTags.loadLatest')}
            </button>
            <button
              type='button'
              className='settings-action-button'
              onClick={() => {
                if (tag) setBaseVersion(tag.updated_at);
                setError(false);
              }}
            >
              {t('settings.resourceTags.continueDraft')}
            </button>
          </div>
        </section>
      )}
      {error && (
        <p role='alert' className='tags-notice'>
          {t(uncertain ? 'settings.resourceTags.resumeSaveHint' : 'settings.resourceTags.saveFailed')}
        </p>
      )}
      <fieldset disabled={!editable}>
        <label className='tag-editor__name'>
          {t('settings.resourceTags.name')}
          <input
            autoFocus
            value={draft.name}
            maxLength={64}
            onChange={(event) => setDraft((value) => ({ ...value, name: event.target.value }))}
            required
            aria-invalid={duplicate || undefined}
          />
        </label>
        {duplicate && !uncertain && (
          <p role='alert' className='tags-notice'>
            {t('settings.resourceTags.nameExists')}
          </p>
        )}
        <fieldset className='tag-editor__choices'>
          <legend>{t('settings.resourceTags.icon')}</legend>
          <div role='radiogroup' aria-label={t('settings.resourceTags.icon')}>
            {(Object.entries(icons) as [ResourceTagFields['icon_key'], typeof Tag][]).map(([key, Icon]) => (
              <label key={key} title={t(`settings.resourceTags.iconNames.${key}`)}>
                <input
                  type='radio'
                  name='tag-icon'
                  value={key}
                  checked={draft.icon_key === key}
                  onChange={() => setDraft((value) => ({ ...value, icon_key: key }))}
                  aria-label={`${t('settings.resourceTags.icon')}: ${t(`settings.resourceTags.iconNames.${key}`)}`}
                />
                <span>
                  <Icon size={18} aria-hidden='true' />
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        <fieldset className='tag-editor__choices'>
          <legend>{t('settings.resourceTags.color')}</legend>
          <div role='radiogroup' aria-label={t('settings.resourceTags.color')}>
            {RESOURCE_TAG_COLOR_KEYS.map((key) => (
              <label key={key} title={t(`settings.resourceTags.colorNames.${key}`)}>
                <input
                  type='radio'
                  name='tag-color'
                  value={key}
                  checked={draft.color_key === key}
                  onChange={() => setDraft((value) => ({ ...value, color_key: key }))}
                  aria-label={`${t('settings.resourceTags.color')}: ${t(`settings.resourceTags.colorNames.${key}`)}`}
                />
                <span className='tag-editor__color resource-tag-badge' data-color={key} />
              </label>
            ))}
          </div>
        </fieldset>
      </fieldset>
      <div className='tag-editor__preview'>
        <span className='tags-muted'>{t('settings.resourceTags.preview')}</span>
        <ResourceTagVisual
          tag={{
            id: 'draft-preview',
            created_at: 0,
            updated_at: 0,
            ...draft,
            name: draft.name || t('settings.resourceTags.new'),
          }}
          favorite={t('conversation.resourceTags.favorite')}
        />
      </div>
      <footer className='tag-editor__footer'>
        <button type='button' className='settings-action-button' onClick={onCancel} disabled={saving}>
          {t('common.cancel')}
        </button>
        <button
          type='submit'
          className='settings-action-button settings-action-button--primary'
          disabled={saving || busy || (!uncertain && (!valid || duplicate || conflicted || blocked))}
        >
          {t(
            saving ? 'conversation.resourceTags.saving' : uncertain ? 'settings.resourceTags.resumeSave' : 'common.save'
          )}
        </button>
      </footer>
    </form>
  );
}
