import React, { useLayoutEffect, useRef, useState } from 'react';
import { BookOpen, Bookmark, Bot, Code2, Database, FlaskConical, Star, Tag, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { ResourceTag } from '@/common/adapter/resourceTags';
import './resourceTags.css';

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
export const resourceTagLabel = (tag: ResourceTag, favorite: string) => ('system_key' in tag ? favorite : tag.name);

export function ResourceTagVisual({ tag, favorite }: { tag: ResourceTag; favorite: string }) {
  const system = 'system_key' in tag;
  const Icon = icons[system ? 'star' : tag.icon_key];
  const name = resourceTagLabel(tag, favorite);
  return (
    <span className='resource-tag-badge' data-color={system ? 'amber' : tag.color_key} title={name}>
      <Icon size={12} aria-hidden='true' />
      <span className='resource-tag-badge__name'>{name}</span>
    </span>
  );
}

export type ResourceTagBadgesProps = {
  targetKey: string;
  tags: readonly ResourceTag[];
  assignedIds: readonly string[];
  limit?: number;
  pendingTagIds?: readonly string[];
  disabled?: boolean;
  onRemove?: (tagId: string) => Promise<void>;
};

function Badges({
  tags,
  assignedIds,
  limit = 2,
  pendingTagIds = [],
  disabled = false,
  onRemove,
}: ResourceTagBadgesProps) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);
  const mounted = useRef(false);
  const pending = useRef(new Set<string>());
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const assigned = new Set(assignedIds);
  const visible = tags.filter((tag) => assigned.has(tag.id));
  const shown = visible.slice(0, Math.max(0, limit));
  const favorite = t('conversation.resourceTags.favorite');
  const remove = async (tag: ResourceTag) => {
    if (!onRemove || disabled || pendingTagIds.includes(tag.id) || pending.current.has(tag.id)) return;
    pending.current.add(tag.id);
    setFailed(false);
    try {
      await onRemove(tag.id);
    } catch {
      if (mounted.current) setFailed(true);
    } finally {
      pending.current.delete(tag.id);
    }
  };
  return (
    <span className='resource-tag-badges'>
      {shown.map((tag) => (
        <span className='resource-tag-badges__item' key={tag.id}>
          <ResourceTagVisual tag={tag} favorite={favorite} />
          {onRemove && (
            <button
              type='button'
              className='resource-tag-badges__remove'
              disabled={disabled || pendingTagIds.includes(tag.id)}
              aria-label={t('conversation.resourceTags.removeTag', { name: resourceTagLabel(tag, favorite) })}
              onClick={() => {
                void remove(tag);
              }}
            >
              <X size={11} aria-hidden='true' />
            </button>
          )}
        </span>
      ))}
      {visible.length > shown.length && (
        <span className='resource-tag-badges__more'>+{visible.length - shown.length}</span>
      )}
      {failed && (
        <span role='alert' className='resource-tags__error'>
          {t('conversation.resourceTags.updateFailed')}
        </span>
      )}
    </span>
  );
}

/** Catalog selection and pending state belong to the caller; only dismissal feedback is local. */
export function ResourceTagBadges(props: ResourceTagBadgesProps) {
  return <Badges key={props.targetKey} {...props} />;
}
