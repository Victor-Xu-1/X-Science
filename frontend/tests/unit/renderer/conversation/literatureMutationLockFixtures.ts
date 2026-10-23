import { vi } from 'vitest';

/** Controlled browser-API boundary, not a claim of real cross-process locking. */
export function installLiteratureLocks(gate: Promise<void> = Promise.resolve()) {
  const tails = new Map<string, Promise<unknown>>();
  const request = vi.fn(<T>(name: string, options: LockOptions, callback: (lock: Lock) => T): Promise<Awaited<T>> => {
    const previous = tails.get(name) ?? Promise.resolve();
    let acquired = false;
    let abortListener: (() => void) | undefined;
    const aborted = new Promise<never>((_resolve, reject) => {
      abortListener = () => {
        if (!acquired) reject(options.signal?.reason ?? new DOMException('Aborted', 'AbortError'));
      };
      options.signal?.addEventListener('abort', abortListener, { once: true });
      if (options.signal?.aborted) abortListener();
    });
    const held = previous
      .catch((): undefined => undefined)
      .then(async () => {
        await gate;
        if (options.signal?.aborted) throw options.signal.reason;
        acquired = true;
        return callback({ name, mode: 'exclusive' } as Lock);
      });
    tails.set(
      name,
      held.catch((): undefined => undefined)
    );
    return Promise.race([held, aborted]).finally(() => {
      if (abortListener) options.signal?.removeEventListener('abort', abortListener);
    });
  });
  vi.stubGlobal('navigator', { locks: { request } });
  return request;
}

export function literaturePendingStorage() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
    values,
  };
}
