import React, { useEffect, useRef, useState } from 'react';
import { GripVertical, LockKeyhole, Pencil, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { ResourceTag, ResourceTagSnapshot, ResourceTagCommand } from '@/common/adapter/resourceTags';
import type { CustomResourceTag } from '@/renderer/services/runtime/resourceTagOwner';
import { ResourceTagVisual, resourceTagLabel } from '../../library/ResourceTagBadges';
import { TagResourceBrowser } from './TagResourceBrowser';
import { TagDeleteConfirm } from './TagDeleteConfirm';
import { moveResourceTag, useResourceTagReorder } from './tagReorder';

export function TagsCatalog({
  account,
  snapshot,
  selectedTag,
  blocked,
  busy,
  query,
  onQueryChange,
  select,
  edit,
  reorder,
  submit,
  unassign,
}: {
  account: string;
  snapshot: ResourceTagSnapshot;
  selectedTag?: ResourceTag;
  blocked: boolean;
  busy: boolean;
  query: string;
  onQueryChange(value: string): void;
  select(id: string): void;
  edit(tag: CustomResourceTag): void;
  reorder(ids: readonly string[]): Promise<void>;
  submit(command: ResourceTagCommand): Promise<string | undefined>;
  unassign(tagId: string, resourceId: string): Promise<void>;
}) {
  const { t } = useTranslation();
  const [deleting, setDeleting] = useState<ResourceTag | null>(null);
  const [reorderError, setReorderError] = useState(false);
  const [announcement, setAnnouncement] = useState('');
  const pending = useRef(false);
  const mounted = useRef(true);
  const grips = useRef(new Map<string, HTMLButtonElement>());
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (deleting && !snapshot.tags.some((tag) => tag.id === deleting.id) && !busy) setDeleting(null);
  }, [deleting, snapshot.tags, busy]);
  const ids = snapshot.tags.filter((tag) => !('system_key' in tag)).map((tag) => tag.id);
  const favorite = t('conversation.resourceTags.favorite');
  const counts = new Map(snapshot.tags.map((tag) => [tag.id, 0]));
  snapshot.assignments.forEach((entry) => counts.set(entry.tag_id, (counts.get(entry.tag_id) ?? 0) + 1));
  const move = async (ordered: readonly string[]) => {
    if (blocked || pending.current || JSON.stringify(ordered) === JSON.stringify(ids)) return;
    const moved = ordered.find((id, index) => id !== ids[index]);
    const activeId = document.activeElement instanceof HTMLElement ? document.activeElement.dataset.tagGrip : undefined;
    pending.current = true;
    setReorderError(false);
    try {
      await reorder(ordered);
      if (mounted.current) {
        const tag = snapshot.tags.find((value) => value.id === (activeId ?? moved));
        setAnnouncement(
          t('settings.resourceTags.moved', {
            name: tag ? resourceTagLabel(tag, favorite) : '',
            position: ordered.indexOf(activeId ?? moved ?? '') + 2,
          })
        );
        if (activeId) grips.current.get(activeId)?.focus({ preventScroll: true });
      }
    } catch {
      if (mounted.current) setReorderError(true);
    } finally {
      pending.current = false;
    }
  };
  const drag = useResourceTagReorder(ids, blocked, (ordered) => {
    void move(ordered);
  });
  return (
    <div className='tags-catalog'>
      <aside className='tags-catalog__rail' aria-label={t('settings.resourceTags.title')}>
        <ol className='tags-catalog__tags'>
          {snapshot.tags.map((tag) => {
            const system = 'system_key' in tag;
            const index = ids.indexOf(tag.id);
            const drop = drag.indicator?.id === tag.id ? drag.indicator.edge : undefined;
            return (
              <li key={tag.id} data-reorderable-tag-id={system ? undefined : tag.id} data-drop={drop}>
                {system ? (
                  <span className='tags-grip' title={t('settings.resourceTags.systemFirst')}>
                    <LockKeyhole size={13} aria-hidden='true' />
                  </span>
                ) : (
                  <button
                    type='button'
                    ref={(element) => {
                      if (element) grips.current.set(tag.id, element);
                      else grips.current.delete(tag.id);
                    }}
                    className='tags-grip tags-grip--movable'
                    data-tag-grip={tag.id}
                    disabled={blocked}
                    aria-label={t('settings.resourceTags.reorderTag', { name: tag.name })}
                    title={t('settings.resourceTags.reorderHint')}
                    onPointerDown={(event) => drag.down(event, tag.id)}
                    onPointerMove={drag.move}
                    onPointerUp={drag.up}
                    onPointerCancel={drag.finish}
                    onKeyDown={(event) => {
                      if (event.key === 'Escape') {
                        drag.finish();
                        return;
                      }
                      if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
                      event.preventDefault();
                      const target = ids[index + (event.key === 'ArrowUp' ? -1 : 1)];
                      if (target)
                        void move(moveResourceTag(ids, tag.id, target, event.key === 'ArrowUp' ? 'before' : 'after'));
                    }}
                  >
                    <GripVertical size={14} aria-hidden='true' />
                  </button>
                )}
                <button
                  type='button'
                  className='tags-catalog__tag'
                  aria-current={selectedTag?.id === tag.id ? 'true' : undefined}
                  onClick={() => select(tag.id)}
                >
                  <ResourceTagVisual tag={tag} favorite={favorite} />
                  <span className='tags-catalog__count'>{counts.get(tag.id) ?? 0}</span>
                </button>
              </li>
            );
          })}
        </ol>
        {reorderError && (
          <p role='alert' className='tags-notice'>
            {t('settings.resourceTags.reorderFailed')}
          </p>
        )}
        <p role='status' aria-live='polite' className='sr-only'>
          {announcement}
        </p>
      </aside>
      <div className='tags-catalog__detail'>
        {selectedTag ? (
          <>
            <div className='tags-catalog__detail-heading'>
              <h2>
                <ResourceTagVisual tag={selectedTag} favorite={favorite} />
              </h2>
              {!('system_key' in selectedTag) && (
                <div className='tags-actions'>
                  <button
                    type='button'
                    className='tags-icon-button'
                    aria-label={t('settings.resourceTags.edit')}
                    disabled={blocked}
                    onClick={() => edit(selectedTag)}
                  >
                    <Pencil size={15} aria-hidden='true' />
                  </button>
                  <button
                    type='button'
                    className='tags-icon-button'
                    aria-label={t('settings.resourceTags.delete')}
                    disabled={blocked}
                    onClick={() => setDeleting(selectedTag)}
                  >
                    <Trash2 size={15} aria-hidden='true' />
                  </button>
                </div>
              )}
            </div>
            <TagResourceBrowser
              key={selectedTag.id}
              account={account}
              tagId={selectedTag.id}
              snapshot={snapshot}
              disabled={blocked}
              query={query}
              onQueryChange={onQueryChange}
              remove={(id) => unassign(selectedTag.id, id)}
            />
          </>
        ) : (
          <p className='tags-empty'>{t('settings.resourceTags.empty')}</p>
        )}
      </div>
      {deleting && (
        <TagDeleteConfirm
          key={deleting.id}
          tag={deleting}
          count={counts.get(deleting.id) ?? 0}
          busy={busy}
          submit={submit}
          close={() => setDeleting(null)}
        />
      )}
    </div>
  );
}
