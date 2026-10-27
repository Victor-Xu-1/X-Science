import {
  decodeLiteratureReadingStatusReceipt,
  type LiteratureReadingStatusCommand,
  type LiteratureReadingStatusReceipt,
} from '@/common/adapter/literatureReadingStatus';
import {
  advanceLiteratureReadingBulk,
  claimLiteratureReadingBulk,
  currentLiteratureReadingBulkCommand,
  decodeLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from './literatureReadingBulkIntent';

export type LiteratureReadingBulkPort = {
  checkActive(): void;
  shouldPause(): boolean;
  save(
    expected: LiteratureReadingBulkIntent,
    next: LiteratureReadingBulkIntent,
    receipt?: LiteratureReadingStatusReceipt
  ): Promise<void>;
  send(command: LiteratureReadingStatusCommand): Promise<unknown>;
  finish(expected: LiteratureReadingBulkIntent, lastReceipt: LiteratureReadingStatusReceipt): Promise<void>;
  paused(intent: LiteratureReadingBulkIntent): void | Promise<void>;
  failed(intent: LiteratureReadingBulkIntent, error: unknown): Promise<void>;
};

/** Control result when pausing has no actual child acknowledgement to return. */
export class BulkPausedError extends Error {
  constructor(readonly intent: LiteratureReadingBulkIntent) {
    super('Library reading bulk paused before acknowledgement');
    this.name = 'BulkPausedError';
  }
}

async function pauseBulk(
  intent: LiteratureReadingBulkIntent,
  port: LiteratureReadingBulkPort,
  receipt?: LiteratureReadingStatusReceipt
): Promise<LiteratureReadingStatusReceipt> {
  port.checkActive();
  await port.paused(intent);
  port.checkActive();
  if (receipt) return receipt;
  throw new BulkPausedError(intent);
}

/** The existing locked mutation owner supplies all storage, request and supersession authority. */
export async function runLiteratureReadingBulkFlow(
  initial: LiteratureReadingBulkIntent,
  port: LiteratureReadingBulkPort
): Promise<LiteratureReadingStatusReceipt> {
  let current = decodeLiteratureReadingBulk(initial);
  let lastReceipt: LiteratureReadingStatusReceipt | undefined;
  for (;;) {
    port.checkActive();
    if (
      current.phase === 'ready' &&
      current.confirmed_batches < current.batches.length &&
      (current.paused || port.shouldPause())
    )
      return pauseBulk(current, port, lastReceipt);
    const sending = claimLiteratureReadingBulk(current);
    if (sending !== current) {
      try {
        await port.save(current, sending);
      } catch (error) {
        if (error instanceof BulkPausedError) return pauseBulk(error.intent, port, lastReceipt);
        throw error;
      }
    }
    current = sending;
    port.checkActive();
    const command = currentLiteratureReadingBulkCommand(current);
    let response: unknown;
    try {
      response = await port.send(command);
    } catch (error) {
      port.checkActive();
      await port.failed(current, error);
      throw error;
    }
    port.checkActive();
    let receipt: LiteratureReadingStatusReceipt;
    let ready: LiteratureReadingBulkIntent;
    try {
      receipt = decodeLiteratureReadingStatusReceipt(response, command);
      ready = advanceLiteratureReadingBulk(current, receipt);
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
    if (current.confirmed_batches === current.batches.length) {
      port.checkActive();
      await port.finish(current, receipt);
      return receipt;
    }
  }
}
