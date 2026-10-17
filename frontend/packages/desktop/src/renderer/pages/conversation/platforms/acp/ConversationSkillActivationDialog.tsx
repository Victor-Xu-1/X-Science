import React from 'react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import type { SkillActivationRequest } from './useConversationSkillActivation';

export function ConversationSkillActivationDialog({
  state,
  onConfirm,
  onCancel,
}: {
  state: SkillActivationRequest | null;
  onConfirm: () => Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  if (!state) return null;
  return (
    <Modal
      visible={state !== null}
      title={t('conversation.attachMenu.enableSkillTitle')}
      style={{ width: 'min(420px, calc(100vw - 32px))' }}
      okText={t(
        state?.phase === 'refresh-error'
          ? 'conversation.attachMenu.saveAsSkillRetryRefresh'
          : 'conversation.attachMenu.enableSkillAndSelect'
      )}
      cancelText={t('common.cancel')}
      confirmLoading={state?.pending}
      cancelButtonProps={{ disabled: state?.pending }}
      maskClosable={!state?.pending}
      closable={!state?.pending}
      onCancel={onCancel}
      onOk={() => void onConfirm()}
    >
      <p className='my-0 break-words text-13px text-t-primary'>{state?.name}</p>
      <p className='mt-8px text-12px text-t-secondary'>{t('conversation.attachMenu.enableSkillHint')}</p>
      {(state?.phase === 'error' || state?.phase === 'refresh-error') && (
        <p role='alert' className='mt-8px text-12px text-t-secondary'>
          {t(
            state.phase === 'refresh-error'
              ? 'conversation.attachMenu.enableSkillRefreshFailed'
              : 'conversation.attachMenu.enableSkillFailed'
          )}
        </p>
      )}
    </Modal>
  );
}
