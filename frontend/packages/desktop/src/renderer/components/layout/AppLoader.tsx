import React from 'react';
import { useTranslation } from 'react-i18next';
import PreviewLoadingState from '@/renderer/components/media/PreviewLoadingState';

const AppLoader: React.FC = () => {
  const { t } = useTranslation();
  return <PreviewLoadingState label={t('common.loading')} fitContainer />;
};

export default AppLoader;
