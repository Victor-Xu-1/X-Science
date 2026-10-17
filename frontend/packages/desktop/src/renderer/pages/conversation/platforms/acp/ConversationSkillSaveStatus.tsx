import React from 'react';
import { useTranslation } from 'react-i18next';
import type { ConversationSkillSaveState } from './useConversationSkillSave';

type ConversationSkillSaveStatusProps = {
  state: ConversationSkillSaveState;
  saveDisabled: boolean;
  skillAvailable: boolean;
  onRetrySave: () => Promise<void>;
  onRetryRefresh: () => Promise<void>;
  onUseSkill: (name: string) => void;
};

const actionClassName =
  'shrink-0 rounded-6px border border-border-base bg-transparent px-8px py-2px text-12px text-t-primary hover:bg-fill-2 disabled:cursor-not-allowed disabled:opacity-50';

export function ConversationSkillSaveStatus({
  state,
  saveDisabled,
  skillAvailable,
  onRetrySave,
  onRetryRefresh,
  onUseSkill,
}: ConversationSkillSaveStatusProps) {
  const { t } = useTranslation();
  if (state.status === 'idle') return null;

  return (
    <div
      role={state.status === 'error' ? 'alert' : 'status'}
      aria-label={t('conversation.attachMenu.saveAsSkill')}
      aria-atomic='true'
      className='mb-8px flex flex-wrap items-center gap-8px rounded-8px border border-border-base bg-fill-1 px-12px py-8px text-12px text-t-secondary'
    >
      {state.status === 'saving' && <span>{t('conversation.attachMenu.saveAsSkillSaving')}</span>}
      {state.status === 'error' && (
        <>
          <span>{t('conversation.attachMenu.saveAsSkillError')}</span>
          <button type='button' className={actionClassName} disabled={saveDisabled} onClick={() => void onRetrySave()}>
            {t('conversation.attachMenu.saveAsSkillRetry')}
          </button>
        </>
      )}
      {state.status === 'saved' && (
        <>
          <span className='min-w-0 break-words'>
            {t('conversation.attachMenu.saveAsSkillSuccess', { name: state.name })}
          </span>
          {state.refresh === 'pending' && <span>{t('conversation.attachMenu.saveAsSkillRefreshing')}</span>}
          {state.refresh === 'error' && (
            <>
              <span>{t('conversation.attachMenu.saveAsSkillRefreshFailed')}</span>
              <button type='button' className={actionClassName} onClick={() => void onRetryRefresh()}>
                {t('conversation.attachMenu.saveAsSkillRetryRefresh')}
              </button>
            </>
          )}
          {state.refresh === 'ready' && !skillAvailable && (
            <span>{t('conversation.attachMenu.saveAsSkillUnavailable')}</span>
          )}
          <button
            type='button'
            className={actionClassName}
            disabled={state.refresh !== 'ready' || !skillAvailable}
            onClick={() => {
              if (state.refresh === 'ready' && skillAvailable) onUseSkill(state.name);
            }}
          >
            {t('conversation.attachMenu.saveAsSkillUse')}
          </button>
        </>
      )}
    </div>
  );
}
