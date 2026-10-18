type InputReceiptBase = {
  version: 1;
  frame_id: string;
  frame_incarnation_id: string;
  input_message_id: string;
  request_sha256: string;
  replayed: boolean;
  event_id: string;
};

export type ConversationInputReceipt = InputReceiptBase &
  (
    | { state: 'accepted'; source: 'transcript'; input_revision: number; branch_id: string; branch_generation: number }
    | { state: 'queued'; source: 'queue'; queue_sequence: number }
  );

const identifier = (value: unknown): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= 256 && value.trim() === value;
const positive = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) > 0;
const invalid = (): never => {
  throw new Error('conversation_send_response_invalid');
};

/** A durable input fact, not a claim about which input is running now. */
export function decodeConversationInputReceipt(
  raw: unknown,
  frameId: string,
  messageId: string
): ConversationInputReceipt {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return invalid();
  const value = raw as Record<string, unknown>;
  if (
    value.version !== 1 ||
    value.frame_id !== frameId ||
    value.input_message_id !== messageId ||
    !identifier(value.frame_incarnation_id) ||
    !identifier(value.event_id) ||
    typeof value.request_sha256 !== 'string' ||
    !/^[a-f0-9]{64}$/.test(value.request_sha256) ||
    typeof value.replayed !== 'boolean'
  )
    return invalid();
  const base: InputReceiptBase = {
    version: 1,
    frame_id: frameId,
    input_message_id: messageId,
    frame_incarnation_id: value.frame_incarnation_id,
    request_sha256: value.request_sha256,
    replayed: value.replayed,
    event_id: value.event_id,
  };
  if (
    value.state === 'accepted' &&
    value.source === 'transcript' &&
    positive(value.input_revision) &&
    identifier(value.branch_id) &&
    positive(value.branch_generation) &&
    value.queue_sequence === undefined
  ) {
    return {
      ...base,
      state: 'accepted',
      source: 'transcript',
      input_revision: value.input_revision,
      branch_id: value.branch_id,
      branch_generation: value.branch_generation,
    };
  }
  if (
    value.state === 'queued' &&
    value.source === 'queue' &&
    positive(value.queue_sequence) &&
    value.input_revision === undefined &&
    value.branch_id === undefined &&
    value.branch_generation === undefined
  ) {
    return { ...base, state: 'queued', source: 'queue', queue_sequence: value.queue_sequence };
  }
  return invalid();
}
