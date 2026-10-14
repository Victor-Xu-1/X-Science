import { Button } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';

const ConversationLoadError: React.FC<{ retrying: boolean; onRetry: () => void }> = ({ retrying, onRetry }) => {
  const { t } = useTranslation();

  return (
    <div
      className='relative flex-1 h-full flex flex-col items-center justify-center gap-12px p-24px text-center'
      data-testid='conversation-load-error'
      role='alert'
    >
      <p className='m-0 text-13px text-t-secondary'>{t('conversation.unavailable.loadFailedTitle')}</p>
      <Button size='small' loading={retrying} disabled={retrying} onClick={onRetry}>
        {t('common.retry')}
      </Button>
    </div>
  );
};

export default ConversationLoadError;
