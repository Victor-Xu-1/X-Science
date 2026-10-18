/**
 * @license
 * Copyright 2026 X-Science
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useId, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ipcBridge } from '@/common';
import { configService } from '@/common/config/configService';
import { isElectronDesktop } from '@/renderer/utils/platform';
import { createBrowserNotificationController, type NotificationPermissionState } from './browserNotificationCore';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '@/renderer/services/rendererAccountScope';

/**
 * WebUI-only: show a browser notification when an agent requests a
 * confirmation or finishes a turn, while the tab is hidden. No-op in
 * Electron, in non-secure contexts, or where the Notification API is absent.
 */
export const useBrowserNotification = (): void => {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const translate = useRef(t);
  translate.current = t;
  const resetId = useId();
  const [owner, setOwner] = useState(getRendererAccountScopeToken);
  useEffect(() => {
    const refreshOwner = () => setOwner(getRendererAccountScopeToken());
    const release = registerRendererAccountReset(`browser-notifications:${resetId}`, refreshOwner);
    refreshOwner();
    return release;
  }, [resetId]);

  useEffect(() => {
    if (isElectronDesktop()) return;
    if (typeof window === 'undefined' || !('Notification' in window) || !window.isSecureContext) return;

    const streamEmitter = ipcBridge.conversation.responseStream;
    const completedEmitter = ipcBridge.conversation.turnCompleted;
    let active = true;
    const owned = () => active && owner === getRendererAccountScopeToken() && Boolean(getRendererAccountOwnerId());
    const notifications = new Set<Notification>();
    const controller = createBrowserNotificationController({
      readGate: () => ({
        isElectron: isElectronDesktop(),
        hasNotificationApi: 'Notification' in window,
        isSecureContext: window.isSecureContext,
        permission: Notification.permission as NotificationPermissionState,
        settingEnabled: owned() && configService.get('system.notificationEnabled') !== false,
        documentHidden: document.hidden,
      }),
      bodyFor: (kind) =>
        kind === 'confirmation'
          ? translate.current('settings.browserNotification.bodyConfirmation')
          : translate.current('settings.browserNotification.bodyTurnCompleted'),
      show: ({ body, conversationId }) => {
        if (!owned()) return false;
        try {
          const notification = new Notification('X-Science', { body });
          notifications.add(notification);
          if (notifications.size > 64) {
            const oldest = notifications.values().next().value!;
            oldest.close();
            notifications.delete(oldest);
          }
          notification.addEventListener('close', () => notifications.delete(notification));
          notification.addEventListener('click', () => {
            if (owned()) {
              window.focus();
              if (conversationId) void navigate(`/conversation/${encodeURIComponent(conversationId)}`);
            }
            notification.close();
            notifications.delete(notification);
          });
          return true;
        } catch (error) {
          console.error('[useBrowserNotification] Failed to show notification:', error);
          return false;
        }
      },
    });

    const disposeStream = streamEmitter.on(controller.onStreamMessage);
    const disposeCompleted = completedEmitter.on(controller.onTurnCompleted);
    return () => {
      active = false;
      disposeStream();
      disposeCompleted();
      notifications.forEach((notification) => notification.close());
      notifications.clear();
    };
  }, [navigate, owner]);
};
