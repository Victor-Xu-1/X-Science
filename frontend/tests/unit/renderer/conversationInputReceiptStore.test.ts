import type { ConversationInputReceipt } from '@/common/adapter/conversationInputReceipt';
import type { TConversationRuntimeSummary } from '@/common/config/storage';
import {
  getConversationRuntimeViewSnapshot,
  hydrateSucceeded,
  localSendAccepted,
  localSendFailed,
  localSendStarted,
  resetConversationRuntimeViewStoreForTest,
  markTerminalProjectionReady,
} from '@/renderer/pages/conversation/runtime/conversationRuntimeViewStore';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { beforeEach, describe, expect, it } from 'vitest';

const idle: TConversationRuntimeSummary = {
  state: 'idle',
  can_send_message: true,
  has_task: false,
  task_status: 'finished',
  is_processing: false,
  pending_confirmations: 0,
  turn_id: null,
};
const accepted: ConversationInputReceipt = {
  version: 1,
  frame_id: 'conv',
  frame_incarnation_id: 'incarnation',
  input_message_id: 'original-input',
  request_sha256: 'a'.repeat(64),
  replayed: true,
  state: 'accepted',
  source: 'transcript',
  event_id: 'original-event',
  input_revision: 2,
  branch_id: 'branch',
  branch_generation: 1,
};

beforeEach(() => {
  resetConversationRuntimeViewStoreForTest();
  resetRendererAccountScopeForTest();
});

describe('input acknowledgement reservation ownership', () => {
  it('does not release a newer local submission when an older accepted retry arrives', () => {
    hydrateSucceeded('conv', idle);
    const earlier = {},
      newer = {};
    localSendStarted('conv', earlier);
    localSendStarted('conv', newer);
    localSendAccepted('conv', 'conv', idle, 'original-input', { ticket: earlier, receipt: accepted });
    expect(getConversationRuntimeViewSnapshot('conv')).toMatchObject({
      localSubmitting: true,
      canSendMessage: false,
      isProcessing: true,
    });
    localSendFailed('conv', 'newer request resolved', newer);
    expect(getConversationRuntimeViewSnapshot('conv')).toMatchObject({ localSubmitting: false, isProcessing: false });
  });

  it('settles its matching reservation without waiting for an already emitted terminal SSE', () => {
    hydrateSucceeded('conv', idle);
    const ticket = {};
    localSendStarted('conv', ticket);
    localSendAccepted('conv', 'conv', idle, 'original-input', { ticket, receipt: accepted });
    expect(getConversationRuntimeViewSnapshot('conv')).toMatchObject({
      state: 'idle',
      localSubmitting: false,
      isProcessing: false,
      activeTurnId: null,
    });
    // The existing canonical history/render reconciliation remains required;
    // a receipt is not permission to invent a rendered final message.
    expect(getConversationRuntimeViewSnapshot('conv').terminalProjectionPending).toBe(true);
    markTerminalProjectionReady('conv');
    expect(getConversationRuntimeViewSnapshot('conv').terminalProjectionPending).toBe(false);
  });

  it('uses the current backend input rather than rebinding a newer run to the replayed input', () => {
    const ticket = {};
    localSendStarted('conv', ticket);
    localSendAccepted(
      'conv',
      'conv',
      {
        ...idle,
        state: 'running',
        has_task: true,
        task_status: 'running',
        is_processing: true,
        can_send_message: false,
        turn_id: 'conv',
        active_input_message_id: 'newer-server-input',
      },
      'original-input',
      { ticket, receipt: accepted }
    );
    expect(getConversationRuntimeViewSnapshot('conv')).toMatchObject({
      activeInputMessageId: 'newer-server-input',
      isProcessing: true,
      localSubmitting: false,
    });
  });

  it('does not let a previous authenticated owner release the new owner reservation', () => {
    setRendererAccountOwner('first-owner');
    const earlier = {},
      newer = {};
    localSendStarted('conv', earlier);
    setRendererAccountOwner('second-owner');
    localSendStarted('conv', newer);
    localSendAccepted('conv', 'conv', idle, 'original-input', { ticket: earlier, receipt: accepted });
    expect(getConversationRuntimeViewSnapshot('conv')).toMatchObject({ localSubmitting: true, isProcessing: true });
  });
});
