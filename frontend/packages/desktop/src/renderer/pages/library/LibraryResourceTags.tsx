import React, { createContext, useContext, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { projectResourceTagTarget } from '@/renderer/services/runtime/resourceTagMutationQueue';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { ResourceTagBadges } from './ResourceTagBadges';
import { ResourceTagMenu } from './ResourceTagMenu';
import { useResourceTags } from './useResourceTags';

type TagState = ReturnType<typeof useResourceTags>;
type TagContext = { account: string; state: TagState };
const Context = createContext<TagContext | null>(null);

/** The existing Library main shares one account catalog with every cell and detail. */
export function LibraryResourceTagsProvider({
  account,
  children,
  ...main
}: React.ComponentPropsWithoutRef<'main'> & { account: string }) {
  const state = useResourceTags(account);
  const value = useMemo(() => ({ account, state }), [account, state]);
  return (
    <Context.Provider value={value}>
      <main {...main}>
        <LibraryResourceTagsFeedback />
        {children}
      </main>
    </Context.Provider>
  );
}

function LibraryResourceTagsFeedback() {
  const context = useContext(Context);
  const { t } = useTranslation();
  if (!context || context.account !== getRendererAccountScopeToken()) return null;
  const { state } = context;
  const needsRecovery = !state.busy && state.status === 'recovery';
  if (state.error || needsRecovery) {
    return (
      <div className='library-workbench__notice' role={state.error ? 'alert' : 'status'}>
        <span>
          {t(
            state.error
              ? state.snapshot
                ? 'conversation.resourceTags.updateFailed'
                : 'conversation.resourceTags.readFailed'
              : 'conversation.resourceTags.resumeNeeded'
          )}
        </span>
        <button
          type='button'
          className='library-workbench__button'
          disabled={state.busy}
          onClick={() => void state.retry().catch((): void => undefined)}
        >
          {t('common.retry')}
        </button>
      </div>
    );
  }
  return state.busy ? (
    <span className='resource-tag-feedback__live' role='status'>
      {t(state.snapshot ? 'conversation.resourceTags.saving' : 'conversation.resourceTags.loading')}
    </span>
  ) : null;
}

/** Presentation only; assignments and accepted commands remain in the account owner. */
export function LibraryItemTags({
  item,
  disabled = false,
  compact = false,
}: {
  item: LiteratureItem;
  disabled?: boolean;
  compact?: boolean;
}) {
  const context = useContext(Context);
  const { t } = useTranslation();
  if (!context || context.account !== getRendererAccountScopeToken()) return null;
  const { account, state } = context;
  const target = { resource_type: 'literature.item', resource_id: item.id } as const;
  const targetKey = JSON.stringify([account, target.resource_type, target.resource_id]);
  if (!state.snapshot)
    return (
      <span className='resource-tag-badges__empty'>
        {t(state.error ? 'conversation.resourceTags.readFailed' : 'conversation.resourceTags.loading')}
      </span>
    );
  if (!state.snapshot.resource_types.includes(target.resource_type))
    return <span className='resource-tag-badges__empty'>{t('conversation.resourceTags.unavailable')}</span>;
  const projection = projectResourceTagTarget(state.snapshot, state.pending, target, state.busy);
  const blocked = disabled || item.deleted;
  const tags = state.snapshot.tags;
  const badges = (
    <ResourceTagBadges
      targetKey={targetKey}
      tags={tags}
      assignedIds={projection.assignedIds}
      pendingTagIds={projection.pendingTagIds}
      disabled={blocked}
      limit={compact ? 2 : Infinity}
      onRemove={compact ? undefined : (tagId) => state.assign(target, tagId, false)}
      tagHref={(tagId) => `#/settings/tags?tag=${encodeURIComponent(tagId)}`}
    />
  );
  const uncertainForTarget =
    state.error &&
    state.pending.some((entry) =>
      entry.command.kind === 'assignment'
        ? entry.command.resource_type === target.resource_type &&
          entry.command.resource_id.trim() === target.resource_id
        : entry.continuation?.target.resource_id === target.resource_id &&
          entry.continuation.target.resource_type === target.resource_type
    );
  return (
    <div className={compact ? 'library-resource-tags library-resource-tags--compact' : 'library-resource-tags'}>
      {compact && badges}
      <ResourceTagMenu
        targetKey={targetKey}
        tags={tags}
        assignedIds={projection.assignedIds}
        pendingTagIds={projection.pendingTagIds}
        creating={projection.creating}
        disabled={blocked}
        error={uncertainForTarget ? t('conversation.resourceTags.updateFailed') : undefined}
        onAssign={(tagId, desired) => state.assign(target, tagId, desired)}
        onCreate={(name) => state.createAndAssign(target, name)}
      />
      {!compact && badges}
    </div>
  );
}
