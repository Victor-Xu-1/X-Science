import React from 'react';
import { useTranslation } from 'react-i18next';
import type { ConversationSkillTaskState } from './useConversationSkillTask';

export function ConversationSkillTaskStatus({
  state,
  disabled,
  onRetry,
  onRetryRefresh,
}: {
  state: ConversationSkillTaskState;
  disabled: boolean;
  onRetry: () => Promise<void>;
  onRetryRefresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const failed = state.status === 'error' || (state.status === 'finished' && state.refresh === 'error');
  // Execution and its result belong to the normal transcript. This surface
  // only reports an actionable local dispatch/catalog failure, never progress.
  if (!failed) return null;
  return (
    <div
      role='alert'
      aria-label={t('conversation.attachMenu.saveAsSkill')}
      className='mb-8px flex flex-wrap items-center gap-8px rounded-8px bg-fill-1 px-12px py-8px text-12px text-t-secondary'
    >
      <span>
        {t(
          state.status === 'error'
            ? 'conversation.attachMenu.saveAsSkillTaskStartFailed'
            : 'conversation.attachMenu.saveAsSkillTaskRefreshFailed'
        )}
      </span>
      {failed && (
        <button
          type='button'
          disabled={state.status === 'error' && disabled}
          className='rounded-6px border border-border-base bg-transparent px-8px py-2px text-12px text-t-primary disabled:opacity-50'
          onClick={() => void (state.status === 'error' ? onRetry() : onRetryRefresh())}
        >
          {t(
            state.status === 'error'
              ? 'conversation.attachMenu.saveAsSkillRetry'
              : 'conversation.attachMenu.saveAsSkillRetryRefresh'
          )}
        </button>
      )}
    </div>
  );
}
