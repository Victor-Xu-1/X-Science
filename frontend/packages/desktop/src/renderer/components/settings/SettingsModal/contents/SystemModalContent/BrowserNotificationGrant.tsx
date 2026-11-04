/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Button } from '@arco-design/web-react';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

/**
 * WebUI-only control to request browser notification permission. In Electron
 * this is never rendered (native notifications are used instead). Renders a
 * grant button, the granted/denied state, or a hint when the page is not a
 * secure context (HTTPS / localhost), where the Notification API is unavailable.
 */
const BrowserNotificationGrant: React.FC = () => {
  const { t } = useTranslation();
  const secureContext = typeof window !== 'undefined' && window.isSecureContext;
  const supported = secureContext && 'Notification' in window;
  const [permission, setPermission] = useState<NotificationPermission>(supported ? Notification.permission : 'denied');

  const pending = useRef(false);
  const [requesting, setRequesting] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!supported) return;
    const refresh = () => setPermission(Notification.permission);
    window.addEventListener('focus', refresh);
    return () => window.removeEventListener('focus', refresh);
  }, [supported]);

  const handleRequest = useCallback(async () => {
    if (!supported || pending.current) return;
    pending.current = true;
    setRequesting(true);
    setFailed(false);
    try {
      setPermission(await Notification.requestPermission());
    } catch {
      setFailed(true);
    } finally {
      pending.current = false;
      setRequesting(false);
    }
  }, [supported]);

  if (!supported) {
    return (
      <div className='pl-12px text-12px text-3'>
        {t(secureContext ? 'settings.notificationsPage.unsupported' : 'settings.browserNotification.insecureContext')}
      </div>
    );
  }
  if (permission === 'granted') {
    return <div className='pl-12px text-12px text-3'>{t('settings.browserNotification.granted')}</div>;
  }
  if (permission === 'denied') {
    return <div className='pl-12px text-12px text-3'>{t('settings.browserNotification.denied')}</div>;
  }
  return (
    <div className='pl-12px'>
      <Button
        type='outline'
        size='small'
        onClick={() => void handleRequest()}
        loading={requesting}
        disabled={requesting}
      >
        {t('settings.browserNotification.enable')}
      </Button>
      {failed && <p role='alert'>{t('settings.notificationsPage.permissionFailed')}</p>}
    </div>
  );
};

export default BrowserNotificationGrant;
