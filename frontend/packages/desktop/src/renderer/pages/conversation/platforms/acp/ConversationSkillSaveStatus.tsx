import React from 'react';
import { useTranslation } from 'react-i18next';
import type { ConversationSkillSaveState } from './useConversationSkillSave';
import { ConversationSkillConflictRecovery } from './ConversationSkillConflictRecovery';

type ConversationSkillSaveStatusProps = {
  state: ConversationSkillSaveState;
  saveDisabled: boolean;
  skillAvailable: boolean;
  availableSkillNames: string[];
  selectedSkillNames: string[];
  onRetrySave: () => Promise<void>;
  onRetryRefresh: () => Promise<void>;
  onUseSkill: (name: string) => void;
};

const actionClassName =
  'shrink-0 rounded-6px border border-border-base bg-transparent px-8px py-2px text-12px text-t-primary hover:bg-fill-2 disabled:cursor-not-allowed disabled:opacity-50';

// Never use an untrusted error/message as text or a translation resource key.
function failureTranslationKey(code: unknown): string {
  switch (code) {
    case 'skill_authoring_timeout':
      return 'conversation.attachMenu.saveAsSkillErrorTimeout';
    case 'skill_authoring_cancelled':
      return 'conversation.attachMenu.saveAsSkillErrorCancelled';
    case 'skill_authoring_output_limit':
      return 'conversation.attachMenu.saveAsSkillErrorOutputLimit';
    case 'skill_authoring_refused':
      return 'conversation.attachMenu.saveAsSkillErrorRefused';
    case 'skill_authoring_invalid_draft':
      return 'conversation.attachMenu.saveAsSkillErrorInvalidDraft';
    case 'skill_authoring_provider_auth':
      return 'conversation.attachMenu.saveAsSkillErrorProviderAuth';
    case 'skill_authoring_rate_limited':
      return 'conversation.attachMenu.saveAsSkillErrorRateLimited';
    case 'skill_authoring_provider_unavailable':
      return 'conversation.attachMenu.saveAsSkillErrorProvider';
    case 'skill_authoring_configuration':
      return 'conversation.attachMenu.saveAsSkillErrorConfiguration';
    case 'skill_authoring_source_unavailable':
      return 'conversation.attachMenu.saveAsSkillErrorSource';
    default:
      return 'conversation.attachMenu.saveAsSkillError';
  }
}

export function ConversationSkillSaveStatus({
  state,
  saveDisabled,
  skillAvailable,
  availableSkillNames,
  selectedSkillNames,
  onRetrySave,
  onRetryRefresh,
  onUseSkill,
}: ConversationSkillSaveStatusProps) {
  const { t } = useTranslation();
  if (state.status === 'idle') return null;

  return (
    <div
      role={state.status === 'error' || state.status === 'conflict' ? 'alert' : 'status'}
      aria-label={t('conversation.attachMenu.saveAsSkill')}
      aria-atomic='true'
      className='mb-8px flex flex-wrap items-center gap-8px rounded-8px border border-border-base bg-fill-1 px-12px py-8px text-12px text-t-secondary'
    >
      {state.status === 'saving' && <span>{t('conversation.attachMenu.saveAsSkillSaving')}</span>}
      {state.status === 'error' && (
        <>
          <span>{t(failureTranslationKey(state.code))}</span>
          <button type='button' className={actionClassName} disabled={saveDisabled} onClick={() => void onRetrySave()}>
            {t('conversation.attachMenu.saveAsSkillRetry')}
          </button>
        </>
      )}
      {state.status === 'conflict' && (
        <>
          <span>{t('conversation.attachMenu.saveAsSkillConflict')}</span>
          <ConversationSkillConflictRecovery
            skillNames={availableSkillNames}
            selectedSkillNames={selectedSkillNames}
            onSelectSkill={onUseSkill}
            actionClassName={actionClassName}
          />
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
