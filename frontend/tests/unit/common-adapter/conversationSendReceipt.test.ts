import { decodeConversationSendAccepted } from '@/common/adapter/conversationRuntimeProtocol';
import { describe, expect, it } from 'vitest';

const receipt = (overrides: Record<string, unknown> = {}): Record<string, unknown> => ({
  version: 1,
  frame_id: 'conversation-1',
  frame_incarnation_id: 'incarnation-1',
  input_message_id: 'input-old',
  request_sha256: 'a'.repeat(64),
  state: 'accepted',
  replayed: true,
  source: 'transcript',
  event_id: 'event-old',
  input_revision: 2,
  branch_id: 'selected-branch',
  branch_generation: 1,
  ...overrides,
});

const idle = {
  state: 'idle',
  can_send_message: true,
  has_task: false,
  task_status: 'finished',
  is_processing: false,
  pending_confirmations: 0,
  turn_id: null,
};

const acknowledged = (runtime: Record<string, unknown> = idle, inputReceipt: unknown = receipt()) => ({
  msg_id: 'input-old',
  turn_id: 'conversation-1',
  runtime,
  input_receipt: inputReceipt,
});

describe('durable conversation input receipts', () => {
  it('binds the accepted frame to the request that actually received the response', () => {
    const value = acknowledged();
    expect(decodeConversationSendAccepted(value, 'conversation-1')).toEqual(value);
    expect(() => decodeConversationSendAccepted(value, 'another-conversation')).toThrow(
      'conversation_send_response_invalid'
    );
  });
  it.each(['finished', 'error', 'cancelled'])(
    'accepts an original input receipt without inventing another running turn (%s)',
    (status) => {
      const value = acknowledged({ ...idle, task_status: status });
      expect(decodeConversationSendAccepted(value)).toEqual(value);
    }
  );

  it.each(['paused', 'waiting_input', 'waiting_approval'])('preserves the actual current %s runtime', (state) => {
    const value = acknowledged({
      ...idle,
      state,
      has_task: true,
      task_status: 'pending',
      turn_id: 'conversation-1',
      pending_confirmations: state === 'waiting_approval' ? 1 : 0,
    });
    expect(decodeConversationSendAccepted(value)).toEqual(value);
  });

  it('does not claim the replayed input owns a newer active input', () => {
    const value = acknowledged({
      ...idle,
      state: 'running',
      has_task: true,
      task_status: 'running',
      is_processing: true,
      can_send_message: false,
      turn_id: 'conversation-1',
      active_input_message_id: 'newer-input',
    });
    expect(decodeConversationSendAccepted(value)).toEqual(value);
  });

  it('distinguishes durable queue acceptance from canonical transcript admission', () => {
    const queued = receipt({ state: 'queued', source: 'queue', event_id: 'queued-event', queue_sequence: 8 });
    delete queued.input_revision;
    delete queued.branch_id;
    delete queued.branch_generation;
    const value = acknowledged(idle, queued);
    expect(decodeConversationSendAccepted(value)).toEqual(value);
  });

  it.each([
    ['null', null],
    ['unknown version', receipt({ version: 2 })],
    ['different frame', receipt({ frame_id: 'another-frame' })],
    ['different input', receipt({ input_message_id: 'another-input' })],
    ['missing incarnation', receipt({ frame_incarnation_id: '' })],
    ['missing digest', receipt({ request_sha256: undefined })],
    ['invalid digest', receipt({ request_sha256: 'g'.repeat(64) })],
    ['missing replay disposition', receipt({ replayed: undefined })],
    ['unknown state', receipt({ state: 'running' })],
    ['missing event', receipt({ event_id: '' })],
    ['nonpositive admission', receipt({ input_revision: 0 })],
    ['unsafe admission', receipt({ input_revision: Number.MAX_SAFE_INTEGER + 1 })],
    ['missing original branch', receipt({ branch_id: '' })],
    ['missing branch generation', receipt({ branch_generation: 0 })],
    ['queue claimed as transcript', receipt({ state: 'queued' })],
    ['transcript claimed as queue', receipt({ source: 'queue', queue_sequence: 1 })],
    ['unknown source', receipt({ source: 'guess' })],
  ])('rejects %s rather than weakening the runtime contract', (_name, invalid) => {
    expect(() => decodeConversationSendAccepted(acknowledged(idle, invalid))).toThrow(
      'conversation_send_response_invalid'
    );
  });

  it('still rejects idle responses without the explicit original input receipt', () => {
    const { input_receipt: _receipt, ...legacy } = acknowledged();
    expect(() => decodeConversationSendAccepted(legacy)).toThrow('conversation_send_response_invalid');
    expect(() => decodeConversationSendAccepted(acknowledged({ ...idle, turn_id: 'conversation-1' }))).toThrow(
      'conversation_runtime_invalid'
    );
  });
});
