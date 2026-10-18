/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { ConversationTurnCompletedEvent } from '@/common/adapter/conversationRuntimeProtocol';

/**
 * Framework-free core for WebUI browser notifications: pure gating and a
 * controller that turns conversation events into notification payloads.
 * Kept free of React / DOM globals so it is unit-testable in the node project.
 */

export type NotificationPermissionState = 'default' | 'granted' | 'denied';

export type NotificationGate = {
  isElectron: boolean;
  hasNotificationApi: boolean;
  isSecureContext: boolean;
  permission: NotificationPermissionState;
  settingEnabled: boolean;
  documentHidden: boolean;
};

export const shouldShowNotification = (gate: NotificationGate): boolean =>
  !gate.isElectron &&
  gate.hasNotificationApi &&
  gate.isSecureContext &&
  gate.permission === 'granted' &&
  gate.settingEnabled &&
  gate.documentHidden;

export type NotificationPayload = {
  body: string;
  conversationId?: string;
};

export type BrowserNotificationDeps = {
  readGate: () => NotificationGate;
  show: (payload: NotificationPayload) => boolean | void;
  bodyFor: (kind: 'confirmation' | 'turnCompleted') => string;
};

/**
 * Permission requests use the response stream. Completion is accepted only
 * through the existing decoded turn.completed contract, not a finish token.
 */
export type StreamMessage = {
  type?: string;
  conversation_id?: string;
  turn_id?: string;
};

// X-Science uses the ACP permission event for interactive confirmation.
const PERMISSION_TYPES = new Set(['acp_permission']);

export const createBrowserNotificationController = (deps: BrowserNotificationDeps) => {
  // A turn_id is a session identifier in the canonical protocol. A publication
  // boundary (or the historical canonical message ID) identifies one result.
  // This bounded presentation cache is not a persistent notification inbox.
  const notified = new Set<string>();

  const onStreamMessage = (message: StreamMessage): void => {
    if (!message?.type) return;

    if (PERMISSION_TYPES.has(message.type)) {
      if (!shouldShowNotification(deps.readGate())) return;
      deps.show({ body: deps.bodyFor('confirmation'), conversationId: message.conversation_id });
      return;
    }
  };

  const onTurnCompleted = (event: ConversationTurnCompletedEvent): void => {
    if (event.status !== 'finished') return;
    const publication =
      event.publication_boundary_id && event.source_publication_sequence !== undefined
        ? [event.publication_boundary_id, event.source_publication_sequence]
        : event.last_message.id
          ? [event.last_message.id]
          : null;
    if (!publication) return;
    const identity = JSON.stringify([event.session_id, event.frame_id ?? event.session_id, ...publication]);
    if (notified.has(identity) || !shouldShowNotification(deps.readGate())) return;
    if (deps.show({ body: deps.bodyFor('turnCompleted'), conversationId: event.session_id }) === false) return;
    notified.add(identity);
    if (notified.size > 512) notified.delete(notified.values().next().value!);
  };

  return { onStreamMessage, onTurnCompleted };
};
