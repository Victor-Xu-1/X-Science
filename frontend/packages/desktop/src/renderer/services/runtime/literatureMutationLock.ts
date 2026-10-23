import { registerRendererAccountReset } from '../rendererAccountScope';

const ACQUIRE_TIMEOUT_MS = 5_000;
let acquisitionId = 0;

/** The browser arbitrates the original v1 slot across same-origin renderers. */
export async function withLiteratureMutationLock<T>(
  key: string,
  current: () => boolean,
  critical: () => T
): Promise<T> {
  if (typeof navigator === 'undefined' || !navigator.locks?.request)
    throw new Error('Library pending storage lock unavailable');

  const abort = new AbortController();
  const timeout = setTimeout(
    () => abort.abort(new Error('Library pending storage lock timed out')),
    ACQUIRE_TIMEOUT_MS
  );
  const unregister = registerRendererAccountReset(`literature-mutation-lock:${++acquisitionId}`, () =>
    abort.abort(new Error('Library account changed'))
  );
  try {
    if (!current()) abort.abort(new Error('Library account changed'));
    return await navigator.locks.request(key, { mode: 'exclusive', signal: abort.signal }, () => {
      // A cancelled acquisition must never mutate storage, even if its callback arrives late.
      if (abort.signal.aborted) throw abort.signal.reason;
      if (!current()) throw new Error('Library account changed');
      clearTimeout(timeout);
      return critical();
    });
  } finally {
    clearTimeout(timeout);
    unregister();
  }
}
