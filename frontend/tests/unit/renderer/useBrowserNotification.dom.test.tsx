/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, cleanup, renderHook } from '@testing-library/react';
import {
  decodeConversationTurnCompleted,
  type ConversationTurnCompletedEvent,
} from '@/common/adapter/conversationRuntimeProtocol';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const streamHandlers: Array<(e: unknown) => void> = [];
const completedHandlers: Array<(e: unknown) => void> = [];

vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      responseStream: {
        on: (h: (e: unknown) => void) => {
          streamHandlers.push(h);
          return () => {
            const index = streamHandlers.indexOf(h);
            if (index >= 0) streamHandlers.splice(index, 1);
          };
        },
      },
      turnCompleted: {
        on: (h: (e: unknown) => void) => {
          completedHandlers.push(h);
          return () => {
            const index = completedHandlers.indexOf(h);
            if (index >= 0) completedHandlers.splice(index, 1);
          };
        },
      },
    },
  },
}));
vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
vi.mock('@/common/config/configService', () => ({ configService: { get: () => true } }));
const navigate = vi.fn();
vi.mock('react-router', () => ({ useNavigate: () => navigate }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k: string) => k }) }));

import { useBrowserNotification } from '@/renderer/hooks/system/notification/useBrowserNotification';

const emitStream = (message: unknown) => streamHandlers.forEach((h) => h(message));
const completed = (
  boundary: string,
  sequence: number,
  status: ConversationTurnCompletedEvent['status'] = 'finished'
): ConversationTurnCompletedEvent => ({
  session_id: 's1',
  turn_id: 's1',
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
const emitCompleted = (event: ConversationTurnCompletedEvent) =>
  completedHandlers.forEach((h) => h(decodeConversationTurnCompleted(event)));

class FakeNotification {
  static permission = 'granted';
  private readonly listeners = new Map<string, () => void>();
  close = vi.fn();
  constructor(
    public title: string,
    public options: { body: string }
  ) {
    FakeNotification.instances.push(this);
  }
  addEventListener(type: string, listener: () => void) {
    this.listeners.set(type, listener);
  }
  dispatch(type: string) {
    this.listeners.get(type)?.();
  }
  static instances: FakeNotification[] = [];
}

beforeEach(() => {
  streamHandlers.length = 0;
  completedHandlers.length = 0;
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('owner-1');
  FakeNotification.instances.length = 0;
  navigate.mockClear();
  (globalThis as unknown as { Notification: unknown }).Notification = FakeNotification;
  // jsdom does not implement window.focus(); stub it so the click path is quiet.
  window.focus = vi.fn();
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true });
  Object.defineProperty(document, 'hidden', { value: true, configurable: true });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('useBrowserNotification', () => {
  it('shows a confirmation notification on an acp_permission stream message when hidden', () => {
    renderHook(() => useBrowserNotification());
    emitStream({ type: 'acp_permission', conversation_id: 'c1' });
    expect(FakeNotification.instances).toHaveLength(1);
    expect(FakeNotification.instances[0].options.body).toBe('settings.browserNotification.bodyConfirmation');
  });

  it('shows a canonical successful completion and navigates on click', () => {
    renderHook(() => useBrowserNotification());
    emitCompleted(completed('boundary-1', 10));
    expect(FakeNotification.instances).toHaveLength(1);
    expect(FakeNotification.instances[0].options.body).toBe('settings.browserNotification.bodyTurnCompleted');
    FakeNotification.instances[0].dispatch('click');
    expect(navigate).toHaveBeenCalledWith('/conversation/s1');
  });

  it('ignores non-terminal stream messages', () => {
    renderHook(() => useBrowserNotification());
    emitStream({ type: 'thinking', conversation_id: 's1', turn_id: 't1' });
    emitStream({ type: 'text', conversation_id: 's1', turn_id: 't1' });
    emitStream({ type: 'finish', conversation_id: 's1', turn_id: 's1' });
    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('notifies both rounds of a session while retaining duplicate protection across rerenders', () => {
    const view = renderHook(() => useBrowserNotification());
    emitCompleted(completed('boundary-1', 10));
    view.rerender();
    emitCompleted(completed('boundary-1', 10));
    emitCompleted(completed('boundary-2', 20));
    expect(FakeNotification.instances).toHaveLength(2);
    expect(completedHandlers).toHaveLength(1);
    view.unmount();
    expect(streamHandlers).toHaveLength(0);
    expect(completedHandlers).toHaveLength(0);
    expect(FakeNotification.instances.every((item) => item.close.mock.calls.length === 1)).toBe(true);
  });

  it('does not announce a failure or cancellation as a success', () => {
    renderHook(() => useBrowserNotification());
    emitCompleted(completed('boundary-error', 10, 'error'));
    emitCompleted(completed('boundary-cancelled', 20, 'cancelled'));
    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('bounds retained platform notification handles during a long-lived session', () => {
    renderHook(() => useBrowserNotification());
    for (let sequence = 1; sequence <= 65; sequence++) emitCompleted(completed('boundary-' + sequence, sequence));
    expect(FakeNotification.instances).toHaveLength(65);
    expect(FakeNotification.instances[0].close).toHaveBeenCalledOnce();
    expect(FakeNotification.instances[64].close).not.toHaveBeenCalled();
  });

  it('fences an old account callback and notification click, including after logout/login', () => {
    renderHook(() => useBrowserNotification());
    const oldHandler = completedHandlers[0];
    emitCompleted(completed('boundary-1', 10));
    const oldNotice = FakeNotification.instances[0];
    act(() => {
      setRendererAccountOwner(null);
      setRendererAccountOwner('owner-1');
    });
    oldHandler(completed('old-account-late', 20));
    oldNotice.dispatch('click');
    expect(navigate).not.toHaveBeenCalled();
    expect(oldNotice.close).toHaveBeenCalled();
    expect(FakeNotification.instances).toHaveLength(1);
    emitCompleted(completed('new-scope', 30));
    expect(FakeNotification.instances).toHaveLength(2);
  });
});
