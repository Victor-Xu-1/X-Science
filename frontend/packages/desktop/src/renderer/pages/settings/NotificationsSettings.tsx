import { Switch } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import BrowserNotificationGrant from '@/renderer/components/settings/SettingsModal/contents/SystemModalContent/BrowserNotificationGrant';
import { useBrowserNotificationPreference } from '@/renderer/hooks/system/notification/BrowserNotificationPreference';
import { isElectronDesktop } from '@/renderer/utils/platform';
import SettingsPageHeader from './components/SettingsPageHeader';
import SettingsPageWrapper from './components/SettingsPageWrapper';
import { SettingsSection } from './components/SettingsPrimitives';

const NotificationsSettings: React.FC = () => {
  const { t } = useTranslation();
  const { state, retry, setEnabled } = useBrowserNotificationPreference();
  const preferenceLabel = t('settings.notificationsPage.taskNotifications');

  return (
    <SettingsPageWrapper>
      <div className='settings-general-page flex flex-col' data-testid='x-science-notifications-settings'>
        <SettingsPageHeader
          title={t('settings.notificationsPage.title')}
          description={t('settings.notificationsPage.description')}
        />
        <SettingsSection
          title={preferenceLabel}
          description={t('settings.notificationsPage.taskDescription')}
          actions={
            <Switch
              aria-label={preferenceLabel}
              checked={state.status === 'ready' && state.enabled}
              disabled={state.status !== 'ready' || state.saving}
              onChange={setEnabled}
            />
          }
        >
          {state.status === 'loading' && <p role='status'>{t('settings.notificationsPage.loading')}</p>}
          {state.status === 'readFailed' && (
            <div className='flex items-center justify-between gap-12px'>
              <p role='alert'>{t('settings.notificationsPage.readFailed')}</p>
              <button type='button' className='settings-action-button' onClick={retry}>
                {t('common.retry')}
              </button>
            </div>
          )}
          {state.status === 'ready' && state.saving && <p role='status'>{t('settings.notificationsPage.saving')}</p>}
          {state.status === 'ready' && state.saveFailed && (
            <p role='alert'>{t('settings.notificationsPage.saveFailed')}</p>
          )}
        </SettingsSection>
        {!isElectronDesktop() && (
          <SettingsSection title={t('settings.notificationsPage.browserPermission')}>
            <BrowserNotificationGrant />
          </SettingsSection>
        )}
      </div>
    </SettingsPageWrapper>
  );
};

export default NotificationsSettings;
