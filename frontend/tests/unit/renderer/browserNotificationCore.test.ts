/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import { describe, it, expect, vi } from 'vitest';
import type { ConversationTurnCompletedEvent } from '@/common/adapter/conversationRuntimeProtocol';
import {
  shouldShowNotification,
  createBrowserNotificationController,
  type NotificationGate,
} from '@/renderer/hooks/system/notification/browserNotificationCore';

const openGate: NotificationGate = {
  isElectron: false,
  hasNotificationApi: true,
  isSecureContext: true,
  permission: 'granted',
  settingEnabled: true,
  documentHidden: true,
};

const completed = (
  boundary: string,
  sequence: number,
  status: ConversationTurnCompletedEvent['status'] = 'finished'
): ConversationTurnCompletedEvent => ({
  session_id: 'c1',
  turn_id: 'c1',
  status,
  state: status === 'error' ? 'error' : 'ai_waiting_input',
  detail: '',
  can_send_message: true,
  publication_boundary_id: boundary,
  source_publication_sequence: sequence,
  runtime: {
    state: 'idle',
    can_send_message: true,
    has_task: false,
    task_status: status,
    is_processing: false,
    pending_confirmations: 0,
    turn_id: null,
  },
  last_message: {
    id: 'message-' + boundary,
    content: '',
    status: status === 'error' ? 'error' : 'finish',
    created_at: sequence,
  },
});

describe('shouldShowNotification', () => {
  it('returns true when all gates pass', () => {
    expect(shouldShowNotification(openGate)).toBe(true);
  });

  it.each([
    ['isElectron', { isElectron: true }],
    ['no api', { hasNotificationApi: false }],
    ['insecure', { isSecureContext: false }],
    ['not granted', { permission: 'default' as const }],
    ['setting off', { settingEnabled: false }],
    ['tab visible', { documentHidden: false }],
  ])('returns false when %s', (_label, override) => {
    expect(shouldShowNotification({ ...openGate, ...override })).toBe(false);
  });
});

describe('createBrowserNotificationController.onStreamMessage', () => {
  const makeDeps = (gate: NotificationGate = openGate) => {
    const show = vi.fn();
    const controller = createBrowserNotificationController({
      readGate: () => gate,
      show,
      bodyFor: (kind) => kind,
    });
    return { show, controller };
  };

  it('ignores an unproved finish token rather than announcing task completion', () => {
    const { show, controller } = makeDeps();
    controller.onStreamMessage({ type: 'finish', conversation_id: 'c1', turn_id: 't1' });
    expect(show).not.toHaveBeenCalled();
  });

  it('shows a confirmation notification on an acp_permission stream message', () => {
    const { show, controller } = makeDeps();
    controller.onStreamMessage({ type: 'acp_permission', conversation_id: 'c2' });
    expect(show).toHaveBeenCalledWith({ body: 'confirmation', conversationId: 'c2' });
  });

  it('ignores retired generic permission stream messages', () => {
    const { show, controller } = makeDeps();
    controller.onStreamMessage({ type: 'permission', conversation_id: 'c3' });
    expect(show).not.toHaveBeenCalled();
  });

  it('ignores non-terminal stream message types', () => {
    const { show, controller } = makeDeps();
    controller.onStreamMessage({ type: 'thinking', conversation_id: 'c1', turn_id: 't1' });
    controller.onStreamMessage({ type: 'text', conversation_id: 'c1', turn_id: 't1' });
    controller.onStreamMessage({ type: 'start', conversation_id: 'c1', turn_id: 't1' });
    expect(show).not.toHaveBeenCalled();
  });

  it('does not show when the gate is closed', () => {
    const { show, controller } = makeDeps({ ...openGate, documentHidden: false });
    controller.onStreamMessage({ type: 'finish', conversation_id: 'c1', turn_id: 't1' });
    controller.onStreamMessage({ type: 'acp_permission', conversation_id: 'c1' });
    expect(show).not.toHaveBeenCalled();
  });

  it('dedups the same publication without suppressing a later round in the same session', () => {
    const { show, controller } = makeDeps();
    controller.onTurnCompleted(completed('boundary-1', 10));
    controller.onTurnCompleted(completed('boundary-1', 10));
    controller.onTurnCompleted(completed('boundary-2', 20));
    controller.onTurnCompleted(completed('boundary-1', 10));
    expect(show).toHaveBeenCalledTimes(2);
    expect(show).toHaveBeenCalledWith({ body: 'turnCompleted', conversationId: 'c1' });
  });

  it.each(['error', 'cancelled'] as const)('never presents %s as successful completion', (status) => {
    const { show, controller } = makeDeps();
    controller.onTurnCompleted(completed('boundary-1', 10, status));
    expect(show).not.toHaveBeenCalled();
  });

  it('does not consume a publication when the platform declines to show it', () => {
    const show = vi.fn().mockReturnValueOnce(false).mockReturnValue(true);
    const controller = createBrowserNotificationController({ readGate: () => openGate, show, bodyFor: (kind) => kind });
    controller.onTurnCompleted(completed('boundary-1', 10));
    controller.onTurnCompleted(completed('boundary-1', 10));
    controller.onTurnCompleted(completed('boundary-1', 10));
    expect(show).toHaveBeenCalledTimes(2);
  });

  it('uses a canonical historical message identity only when no publication boundary is available', () => {
    const { show, controller } = makeDeps();
    const first = completed('boundary-1', 10);
    delete first.publication_boundary_id;
    delete first.source_publication_sequence;
    controller.onTurnCompleted(first);
    controller.onTurnCompleted(first);
    controller.onTurnCompleted({ ...first, last_message: { ...first.last_message, id: 'different-message' } });
    controller.onTurnCompleted({ ...first, last_message: { ...first.last_message, id: undefined } });
    expect(show).toHaveBeenCalledTimes(2);
  });

  it('ignores messages without a type', () => {
    const { show, controller } = makeDeps();
    controller.onStreamMessage({ conversation_id: 'c1' });
    expect(show).not.toHaveBeenCalled();
  });
});
