import type {
  LiteratureReadingStatusCommand,
  LiteratureReadingStatusReceipt,
  LiteratureReadingStatus,
} from '@/common/adapter/literatureReadingStatus';
import { literatureValueIdentity } from '@/common/adapter/literatureValueIdentity';
import { OriginalPendingConflict, type LiteraturePending } from './literatureUploadIntent';
import {
  createLiteratureReadingBulk,
  decodeLiteratureReadingBulk,
  isLiteratureReadingBulk,
  type LiteratureReadingBulkIntent,
} from './literatureReadingBulkIntent';
import { BulkPausedError, runLiteratureReadingBulkFlow } from './literatureReadingBulkFlow';

type BulkUpdate = {
  pending?: LiteraturePending | null;
  action?: LiteraturePending;
  error?: 'uncertain' | 'rejected' | 'storage' | null;
  receipt?: LiteratureReadingStatusReceipt;
};
export type LiteratureReadingBulkSlot = {
  read(): LiteraturePending | null;
  write(expected: LiteraturePending | null, next: LiteratureReadingBulkIntent): void;
  settle(expected: LiteratureReadingBulkIntent, receipt?: LiteratureReadingStatusReceipt): void;
  publish(update: BulkUpdate): void;
};
export type LiteratureReadingBulkHost<Flight> = {
  current(): boolean;
  pending(): LiteraturePending | null;
  running(): Promise<unknown> | undefined;
  begin(
    intent: LiteratureReadingBulkIntent,
    run: (flight: Flight) => Promise<LiteratureReadingStatusReceipt>,
    canContinue?: () => boolean
  ): Promise<LiteratureReadingStatusReceipt>;
  checkActive(flight: Flight): void;
  pauseRequested(flight: Flight): boolean;
  locked<T>(flight: Flight | undefined, work: (slot: LiteratureReadingBulkSlot) => T): Promise<T>;
  send(command: LiteratureReadingStatusCommand, flight: Flight): Promise<unknown>;
  rejected(error: unknown): boolean;
  uuid(): string;
};

function sameOrigin(left: LiteratureReadingBulkIntent, right: LiteratureReadingBulkIntent): boolean {
  const identity = (value: LiteratureReadingBulkIntent) => ({
    ...value,
    phase: 'ready',
    confirmed_batches: 0,
    paused: false,
  });
  return literatureValueIdentity(identity(left)) === literatureValueIdentity(identity(right));
}

/** No queue or storage lives here: every transition uses the original account owner and slot lock. */
export function createLiteratureReadingBulkOperations<Flight>(host: LiteratureReadingBulkHost<Flight>) {
  const interrupted = () => new DOMException('Library bulk operation ended', 'AbortError');
  const requireCurrent = () => {
    if (!host.current()) throw interrupted();
  };
  const projectCurrent = (
    slot: LiteratureReadingBulkSlot,
    actual: LiteraturePending | null,
    receipt?: LiteratureReadingStatusReceipt
  ) =>
    slot.publish({
      pending: actual,
      action: actual ?? undefined,
      error: actual ? 'uncertain' : null,
      ...(receipt ? { receipt } : {}),
    });
  const port = (flight: Flight): Parameters<typeof runLiteratureReadingBulkFlow>[1] => ({
    checkActive: () => host.checkActive(flight),
    shouldPause: () => {
      const pending = host.pending();
      return host.pauseRequested(flight) || (isLiteratureReadingBulk(pending) && pending.paused);
    },
    save: (expected, next, receipt) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (
          isLiteratureReadingBulk(actual) &&
          sameOrigin(actual, expected) &&
          actual.confirmed_batches === expected.confirmed_batches &&
          actual.phase === expected.phase
        ) {
          // A stop from another view may arrive while this exact child is in flight.
          if (
            (actual.paused || host.pauseRequested(flight)) &&
            expected.phase === 'ready' &&
            next.phase === 'sending' &&
            expected.confirmed_batches < expected.batches.length
          ) {
            const paused = actual.paused ? actual : decodeLiteratureReadingBulk({ ...actual, paused: true });
            if (paused !== actual) slot.write(actual, paused);
            else slot.publish({ pending: actual, action: actual, error: null });
            throw new BulkPausedError(paused);
          }
          slot.write(actual, decodeLiteratureReadingBulk({ ...next, paused: actual.paused }));
          if (receipt) slot.publish({ receipt });
          return;
        }
        // Never refill a cleared slot or overwrite a newer request after a late ACK.
        if (receipt) slot.publish({ receipt });
        slot.write(expected, next);
      }),
    send: (command) => host.send(command, flight),
    finish: (expected, receipt) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (
          isLiteratureReadingBulk(actual) &&
          sameOrigin(actual, expected) &&
          actual.phase === 'ready' &&
          actual.confirmed_batches === expected.confirmed_batches &&
          actual.confirmed_batches === actual.batches.length
        )
          slot.settle(actual, receipt);
        else projectCurrent(slot, actual, receipt);
      }),
    paused: (intent) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (isLiteratureReadingBulk(actual) && sameOrigin(actual, intent)) {
          if (!actual.paused) slot.write(actual, decodeLiteratureReadingBulk({ ...actual, paused: true }));
          else slot.publish({ pending: actual, action: actual, error: null });
        } else projectCurrent(slot, actual);
      }),
    failed: (expected, error) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (
          !isLiteratureReadingBulk(actual) ||
          !sameOrigin(actual, expected) ||
          actual.confirmed_batches !== expected.confirmed_batches ||
          actual.phase !== expected.phase
        ) {
          projectCurrent(slot, actual);
          return;
        }
        const rejected = host.rejected(error);
        const retained = decodeLiteratureReadingBulk({
          ...actual,
          phase: rejected ? 'ready' : actual.phase,
          paused: true,
        });
        slot.write(actual, retained);
        slot.publish({ error: rejected ? 'rejected' : 'uncertain' });
      }),
  });

  const runningBulk = () => {
    const promise = host.running();
    if (promise && !isLiteratureReadingBulk(host.pending())) throw new OriginalPendingConflict();
    return promise as Promise<LiteratureReadingStatusReceipt> | undefined;
  };
  const pause = (operationId: string): Promise<void> =>
    host.locked(undefined, (slot) => {
      requireCurrent();
      const actual = slot.read();
      if (!isLiteratureReadingBulk(actual) || actual.operation_id !== operationId) {
        projectCurrent(slot, actual);
        return;
      }
      if (!actual.paused) slot.write(actual, decodeLiteratureReadingBulk({ ...actual, paused: true }));
      else slot.publish({ pending: actual, action: actual, error: null });
    });

  return {
    start(
      ids: readonly string[],
      status: LiteratureReadingStatus,
      scopeKey: string,
      canContinue?: () => boolean
    ): Promise<LiteratureReadingStatusReceipt> {
      requireCurrent();
      if (host.running()) throw new OriginalPendingConflict();
      const intent = createLiteratureReadingBulk(ids, status, scopeKey, host.uuid);
      return host.begin(
        intent,
        async (flight) => {
          await host.locked(flight, (slot) => slot.write(null, intent));
          return runLiteratureReadingBulkFlow(intent, port(flight));
        },
        canContinue
      );
    },
    resume(stopAfterCurrent = false, canContinue?: () => boolean): Promise<LiteratureReadingStatusReceipt> {
      requireCurrent();
      const running = runningBulk();
      if (running) {
        const pending = host.pending();
        if (stopAfterCurrent && isLiteratureReadingBulk(pending))
          return pause(pending.operation_id).then(() => running);
        return running;
      }
      // Bind the click to the plan the user actually saw; a fresh different plan is read back, never adopted.
      const expected = host.pending();
      if (!isLiteratureReadingBulk(expected))
        return host.locked(undefined, (slot) => {
          projectCurrent(slot, slot.read());
          throw new OriginalPendingConflict();
        });
      return host.begin(
        expected,
        async (flight) => {
          const intent = await host.locked(flight, (slot) => {
            const actual = slot.read();
            if (!isLiteratureReadingBulk(actual) || !sameOrigin(actual, expected)) {
              projectCurrent(slot, actual);
              throw new OriginalPendingConflict();
            }
            const next = decodeLiteratureReadingBulk({ ...actual, paused: stopAfterCurrent });
            slot.write(actual, next);
            return next;
          });
          return runLiteratureReadingBulkFlow(intent, port(flight));
        },
        canContinue
      );
    },
    pause,
    discard(operationId: string): Promise<void> {
      requireCurrent();
      if (host.running()) return Promise.reject(new Error('Library bulk update is still active'));
      return host.locked(undefined, (slot) => {
        const actual = slot.read();
        if (!isLiteratureReadingBulk(actual) || actual.operation_id !== operationId) {
          projectCurrent(slot, actual);
          throw new OriginalPendingConflict();
        }
        if (actual.phase !== 'ready')
          throw new Error('Confirm the original current batch before discarding remaining work');
        slot.settle(actual);
      });
    },
  };
}
