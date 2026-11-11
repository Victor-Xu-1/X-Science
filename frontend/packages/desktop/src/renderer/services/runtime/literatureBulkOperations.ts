import { OriginalPendingConflict, type LiteraturePending } from './literatureUploadIntent';
import {
  BulkPausedError,
  runLiteratureBulkFlow,
  type LiteratureBulkCodec,
  type LiteratureBulkIntent,
  type LiteratureBulkPort,
} from './literatureBulkFlow';

type BulkUpdate<R> = {
  pending?: LiteraturePending | null;
  action?: LiteraturePending;
  error?: 'uncertain' | 'rejected' | 'storage' | null;
  receipt?: R;
};
export type LiteratureBulkSlot<I, R> = {
  read(): LiteraturePending | null;
  write(expected: LiteraturePending | null, next: I): void;
  settle(expected: I, receipt?: R): void;
  publish(update: BulkUpdate<R>): void;
};
export type LiteratureBulkHost<Flight, I, C, R> = {
  current(): boolean;
  pending(): LiteraturePending | null;
  running(): Promise<unknown> | undefined;
  begin(intent: I, run: (flight: Flight) => Promise<R>, canContinue?: () => boolean): Promise<R>;
  checkActive(flight: Flight): void;
  pauseRequested(flight: Flight): boolean;
  locked<T>(flight: Flight | undefined, work: (slot: LiteratureBulkSlot<I, R>) => T): Promise<T>;
  send(command: C, flight: Flight): Promise<unknown>;
  rejected(error: unknown): boolean;
  uuid(): string;
};

const interrupted = () => new DOMException('Library bulk operation ended', 'AbortError');
const projectCurrent = <I, R>(slot: LiteratureBulkSlot<I, R>, actual: LiteraturePending | null, receipt?: R) =>
  slot.publish({
    pending: actual,
    action: actual ?? undefined,
    error: actual ? 'uncertain' : null,
    ...(receipt ? { receipt } : {}),
  });

/** No queue or storage lives here: every transition uses the original account owner and slot lock. */
export function createLiteratureBulkOperations<
  Flight,
  I extends LiteratureBulkIntent & LiteraturePending,
  C extends { operation_id: string },
  R,
>(host: LiteratureBulkHost<Flight, I, C, R>, codec: LiteratureBulkCodec<I, C, R>) {
  const requireCurrent = () => {
    if (!host.current()) throw interrupted();
  };
  const port = (flight: Flight, uncertainOperationId?: string): LiteratureBulkPort<I, C, R> => ({
    checkActive: () => host.checkActive(flight),
    shouldPause: () => {
      const pending = host.pending();
      return host.pauseRequested(flight) || (codec.is(pending) && pending.paused);
    },
    save: (expected, next, receipt) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (
          codec.is(actual) &&
          codec.sameOrigin(actual, expected) &&
          codec.position(actual) === codec.position(expected) &&
          actual.phase === expected.phase
        ) {
          // A stop from another view may arrive while this exact child is in flight.
          if (
            (actual.paused || host.pauseRequested(flight)) &&
            expected.phase === 'ready' &&
            next.phase === 'sending' &&
            codec.position(expected) < codec.count(expected)
          ) {
            const paused = actual.paused ? actual : codec.decode({ ...actual, paused: true });
            if (paused !== actual) slot.write(actual, paused);
            else slot.publish({ pending: actual, action: actual, error: null });
            throw new BulkPausedError(paused);
          }
          slot.write(actual, codec.decode({ ...next, paused: actual.paused }));
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
          codec.is(actual) &&
          codec.sameOrigin(actual, expected) &&
          actual.phase === 'ready' &&
          codec.position(actual) === codec.position(expected) &&
          codec.position(actual) === codec.count(actual)
        )
          slot.settle(actual, receipt);
        else projectCurrent(slot, actual, receipt);
      }),
    paused: (intent) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (codec.is(actual) && codec.sameOrigin(actual, intent)) {
          if (!actual.paused) slot.write(actual, codec.decode({ ...actual, paused: true }));
          else slot.publish({ pending: actual, action: actual, error: null });
        } else projectCurrent(slot, actual);
      }),
    failed: (expected, error) =>
      host.locked(flight, (slot) => {
        const actual = slot.read();
        if (
          !codec.is(actual) ||
          !codec.sameOrigin(actual, expected) ||
          codec.position(actual) !== codec.position(expected) ||
          actual.phase !== expected.phase
        ) {
          projectCurrent(slot, actual);
          return;
        }
        // Rejecting a replay says nothing about a previous attempt whose ACK is still unknown.
        const rejected = codec.current(expected).operation_id !== uncertainOperationId && host.rejected(error);
        const retained = codec.decode({
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
    if (promise && !codec.is(host.pending())) throw new OriginalPendingConflict();
    return promise as Promise<R> | undefined;
  };
  const pause = (operationId: string): Promise<void> =>
    host.locked(undefined, (slot) => {
      requireCurrent();
      const actual = slot.read();
      if (!codec.is(actual) || actual.operation_id !== operationId) {
        projectCurrent(slot, actual);
        return;
      }
      if (!actual.paused) slot.write(actual, codec.decode({ ...actual, paused: true }));
      else slot.publish({ pending: actual, action: actual, error: null });
    });
  const execute = (intent: I, flight: Flight) => {
    const uncertain = intent.phase === 'sending' || codec.position(intent) === codec.count(intent);
    return runLiteratureBulkFlow(
      intent,
      port(flight, uncertain ? codec.current(intent).operation_id : undefined),
      codec
    );
  };

  return {
    start(input: I, canContinue?: () => boolean): Promise<R> {
      requireCurrent();
      if (host.running()) throw new OriginalPendingConflict();
      const intent = codec.decode(input);
      return host.begin(
        intent,
        async (flight) => {
          await host.locked(flight, (slot) => slot.write(null, intent));
          return execute(intent, flight);
        },
        canContinue
      );
    },
    resume(stopAfterCurrent = false, canContinue?: () => boolean): Promise<R> {
      requireCurrent();
      const running = runningBulk();
      if (running) {
        const pending = host.pending();
        if (stopAfterCurrent && codec.is(pending)) return pause(pending.operation_id).then(() => running);
        return running;
      }
      // Bind the click to the plan the user actually saw; a fresh different plan is read back, never adopted.
      const expected = host.pending();
      if (!codec.is(expected))
        return host.locked(undefined, (slot) => {
          projectCurrent(slot, slot.read());
          throw new OriginalPendingConflict();
        });
      return host.begin(
        expected,
        async (flight) => {
          const intent = await host.locked(flight, (slot) => {
            const actual = slot.read();
            if (!codec.is(actual) || !codec.sameOrigin(actual, expected)) {
              projectCurrent(slot, actual);
              throw new OriginalPendingConflict();
            }
            const next = codec.decode({ ...actual, paused: stopAfterCurrent });
            slot.write(actual, next);
            return next;
          });
          return execute(intent, flight);
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
        if (!codec.is(actual) || actual.operation_id !== operationId) {
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
