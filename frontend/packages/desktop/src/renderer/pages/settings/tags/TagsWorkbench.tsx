import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useSearchParams } from 'react-router';
import { ArrowLeft, Plus, RefreshCw, Tags } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '@/renderer/services/rendererAccountScope';
import { useResourceTags } from '../../library/useResourceTags';
import { resourceTagDefiniteRejection } from '@/renderer/services/runtime/resourceTagMutationQueue';
import SettingsPageHeader from '../components/SettingsPageHeader';
import { TagsCatalog } from './TagsCatalog';
import { TagEditor, type TagEditorLeaveState } from './TagEditor';

type View = 'list' | 'create' | 'edit';
/** Route and transient view state compose the one account tag owner, never a second catalog. */
function TagsScope({ account }: { account: string }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const state = useResourceTags(account);
  const ownerId = getRendererAccountOwnerId();
  const selectedId = params.get('tag') ?? '';
  const requestedView = params.get('view');
  const view: View = requestedView === 'create' || requestedView === 'edit' ? requestedView : 'list';
  const query = params.get('q') ?? '';
  const tags = state.snapshot?.tags ?? [];
  const selectedTag = tags.find((tag) => tag.id === selectedId) ?? tags[0];
  const editTag =
    selectedTag && !('system_key' in selectedTag) && selectedTag.id === selectedId ? selectedTag : undefined;
  const rejected = state.error && resourceTagDefiniteRejection(state.error) !== null && state.pending.length === 0;
  const blocked = state.busy || state.pending.length > 0 || Boolean(state.error && !rejected);
  const leave = useRef<TagEditorLeaveState | null>(null);
  const [leaveState, setLeaveState] = useState<TagEditorLeaveState | null>(null);
  const [pendingLeave, setPendingLeave] = useState<(() => void) | null>(null);
  const changeLeave = useCallback((next: TagEditorLeaveState | null) => {
    leave.current = next;
    setLeaveState(next);
  }, []);
  const requestLeave = useCallback((action: () => void) => {
    if (leave.current?.busy) return;
    if (leave.current?.dirty) setPendingLeave(() => action);
    else action();
  }, []);
  const navigate = (nextView: View, id = selectedId) => {
    const next = new URLSearchParams(params);
    next.delete('view');
    if (nextView !== 'list') next.set('view', nextView);
    if (id) next.set('tag', id);
    else next.delete('tag');
    setParams(next);
  };
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (leave.current?.dirty || leave.current?.busy) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    const link = (event: MouseEvent) => {
      if (!leave.current?.dirty && !leave.current?.busy) return;
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      const target = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null;
      if (
        !target ||
        target.target === '_blank' ||
        target.hasAttribute('download') ||
        target.href === window.location.href
      )
        return;
      event.preventDefault();
      event.stopPropagation();
      requestLeave(() => window.location.assign(target.href));
    };
    window.addEventListener('beforeunload', beforeUnload);
    document.addEventListener('click', link, true);
    return () => {
      window.removeEventListener('beforeunload', beforeUnload);
      document.removeEventListener('click', link, true);
    };
  }, [requestLeave]);
  useEffect(() => {
    if (!state.snapshot || state.busy || state.pending.length || state.error) return;
    if (view === 'edit' && !editTag) {
      const next = new URLSearchParams(params);
      next.delete('view');
      next.delete('tag');
      setParams(next, { replace: true });
    }
  }, [state.snapshot, state.busy, state.pending.length, state.error, view, editTag, params, setParams]);
  const retry = () => {
    void state.retry().catch((): void => undefined);
  };
  if (account !== getRendererAccountScopeToken() || !ownerId) return null;
  return (
    <div className='tags-workbench' data-testid='x-science-tags-settings'>
      <SettingsPageHeader
        title={
          <>
            <Tags size={19} aria-hidden='true' />
            {t(
              view === 'create'
                ? 'settings.resourceTags.new'
                : view === 'edit'
                  ? 'settings.resourceTags.edit'
                  : 'settings.resourceTags.title'
            )}
          </>
        }
        description={view === 'list' ? t('settings.resourceTags.description') : undefined}
        actions={
          view === 'list' ? (
            <>
              <button
                type='button'
                className='settings-action-button'
                disabled={state.busy}
                onClick={() => {
                  void state.reload().catch((): void => undefined);
                }}
                aria-label={t('common.refresh')}
              >
                <RefreshCw size={14} aria-hidden='true' />
              </button>
              <button
                type='button'
                className='settings-action-button settings-action-button--primary'
                disabled={!state.snapshot || blocked}
                onClick={() => navigate('create', '')}
              >
                <Plus size={14} aria-hidden='true' />
                {t('settings.resourceTags.new')}
              </button>
            </>
          ) : (
            <button
              type='button'
              className='settings-action-button'
              disabled={leaveState?.busy}
              onClick={() => requestLeave(() => navigate('list'))}
            >
              <ArrowLeft size={14} aria-hidden='true' />
              {t('settings.resourceTags.back')}
            </button>
          )
        }
      />
      {(state.error || (!state.busy && state.pending.length > 0)) && (
        <div role='alert' className='tags-notice tags-notice--actions'>
          <span>
            {t(
              state.pending.length
                ? 'conversation.resourceTags.resumeNeeded'
                : rejected
                  ? 'conversation.resourceTags.updateFailed'
                  : 'conversation.resourceTags.readFailed'
            )}
          </span>
          <button type='button' className='settings-action-button' disabled={state.busy} onClick={retry}>
            {t('common.retry')}
          </button>
        </div>
      )}
      {state.busy && (
        <p role='status' className='tags-muted'>
          {t(state.status === 'saving' ? 'conversation.resourceTags.saving' : 'conversation.resourceTags.loading')}
        </p>
      )}
      {state.snapshot &&
        (view === 'list' ? (
          <TagsCatalog
            account={account}
            snapshot={state.snapshot}
            selectedTag={selectedTag}
            blocked={blocked}
            busy={state.busy}
            query={query}
            onQueryChange={(value) => {
              const next = new URLSearchParams(params);
              if (value) next.set('q', value);
              else next.delete('q');
              setParams(next, { replace: true });
            }}
            select={(id) => navigate('list', id)}
            edit={(tag) => navigate('edit', tag.id)}
            reorder={state.reorder}
            submit={state.catalog}
            unassign={(tagId, id) => state.assign({ resource_type: 'literature.item', resource_id: id }, tagId, false)}
          />
        ) : view === 'create' || editTag ? (
          <TagEditor
            key={view === 'create' ? 'create' : editTag!.id}
            ownerId={ownerId}
            tag={view === 'edit' ? editTag : undefined}
            tags={tags}
            busy={state.busy}
            blocked={blocked}
            submit={state.catalog}
            onCancel={() => requestLeave(() => navigate('list'))}
            onSaved={(id) => {
              leave.current = null;
              navigate('list', id);
            }}
            onLeaveChange={changeLeave}
          />
        ) : null)}
      {pendingLeave && (
        <Modal
          visible
          title={t('settings.resourceTags.leaveQuestion')}
          style={{ width: 'min(420px, calc(100vw - 32px))' }}
          onCancel={() => setPendingLeave(null)}
          footer={
            <div className='tags-actions tags-actions--end'>
              <button type='button' className='settings-action-button' onClick={() => setPendingLeave(null)}>
                {t('settings.resourceTags.keepEditing')}
              </button>
              <button
                type='button'
                className='settings-action-button'
                onClick={() => {
                  const action = pendingLeave;
                  leave.current = null;
                  setPendingLeave(null);
                  action();
                }}
              >
                {t('settings.resourceTags.leave')}
              </button>
            </div>
          }
        >
          <p>{t('settings.resourceTags.leaveHint')}</p>
        </Modal>
      )}
    </div>
  );
}

export function TagsWorkbench() {
  const id = useId();
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  useEffect(
    () => registerRendererAccountReset(`global-tags:${id}`, () => setAccount(getRendererAccountScopeToken())),
    [id]
  );
  return <TagsScope key={account} account={account} />;
}
