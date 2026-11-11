/* eslint-disable no-await-in-loop -- Each child requires the previous real ACK and durable cursor save. */
export type LiteratureBulkIntent = Readonly<{
  operation_id: string;
  scope_key: string;
  phase: 'ready' | 'sending';
  paused: boolean;
}>;
/** Typed codecs retain each original command, ACK and durable record authority. */
export type LiteratureBulkCodec<I extends LiteratureBulkIntent, C, R> = {
  is(value: unknown): value is I;
  decode(value: unknown): I;
  claim(intent: I): I;
  current(intent: I): C;
  receipt(value: unknown, command: C): R;
  advance(intent: I, receipt: R): I;
  position(intent: I): number;
  count(intent: I): number;
  sameOrigin(left: I, right: I): boolean;
};

export type LiteratureBulkPort<I, C, R> = {
  checkActive(): void;
  shouldPause(): boolean;
  save(expected: I, next: I, receipt?: R): Promise<void>;
  send(command: C): Promise<unknown>;
  finish(expected: I, lastReceipt: R): Promise<void>;
  paused(intent: I): void | Promise<void>;
  failed(intent: I, error: unknown): Promise<void>;
};

/** Control result when pausing has no actual child acknowledgement to return. */
export class BulkPausedError extends Error {
  constructor(readonly intent: unknown) {
    super('Library bulk paused before acknowledgement');
    this.name = 'BulkPausedError';
  }
}

async function pauseBulk<I, C, R>(intent: I, port: LiteratureBulkPort<I, C, R>, receipt?: R): Promise<R> {
  port.checkActive();
  await port.paused(intent);
  port.checkActive();
  if (receipt) return receipt;
  throw new BulkPausedError(intent);
}

/** The existing locked mutation owner supplies all storage, request and supersession authority. */
export async function runLiteratureBulkFlow<I extends LiteratureBulkIntent, C, R>(
  initial: I,
  port: LiteratureBulkPort<I, C, R>,
  codec: LiteratureBulkCodec<I, C, R>
): Promise<R> {
  let current = codec.decode(initial);
  let lastReceipt: R | undefined;
  for (;;) {
    port.checkActive();
    if (
      current.phase === 'ready' &&
      codec.position(current) < codec.count(current) &&
      (current.paused || port.shouldPause())
    )
      return pauseBulk(current, port, lastReceipt);
    const sending = codec.claim(current);
    if (sending !== current) {
      try {
        await port.save(current, sending);
      } catch (error) {
        if (error instanceof BulkPausedError) return pauseBulk(codec.decode(error.intent), port, lastReceipt);
        throw error;
      }
    }
    current = sending;
    port.checkActive();
    const command = codec.current(current);
    let response: unknown;
    try {
      response = await port.send(command);
    } catch (error) {
      port.checkActive();
      await port.failed(current, error);
      throw error;
    }
    port.checkActive();
    let receipt: R;
    let ready: I;
    try {
      receipt = codec.receipt(response, command);
      ready = codec.advance(current, receipt);
    } catch (error) {
      port.checkActive();
      await port.failed(current, error);
      throw error;
    }
    port.checkActive();
    // Storage failures remain storage failures; an ACK never authorizes an unsaved cursor.
    await port.save(current, ready, receipt);
    current = ready;
    lastReceipt = receipt;
    if (codec.position(current) === codec.count(current)) {
      port.checkActive();
      await port.finish(current, receipt);
      return receipt;
    }
  }
}
