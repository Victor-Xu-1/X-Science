import { registerRendererAccountReset } from '../rendererAccountScope';

export type RendererMutationStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

export class RendererMutationSlotConflict<T = unknown> extends Error {
  constructor(readonly actual: T | null) {
    super('The original renderer mutation changed');
    this.name = 'RendererMutationSlotConflict';
  }
}

export type RendererMutationSlot<T> = {
  read: () => T | null;
  /** The caller must hold this slot's browser lock. */
  replace: (expected: T | null, next: T) => void;
  /** Clear only the exact original intent; an old ACK cannot clear a newer one. */
  settle: (expected: T, clear: boolean) => { remove: boolean; pending: T | null };
};

/** Storage mechanics only. Domain codecs, account ownership and rejection policy stay with the caller. */
export function createRendererMutationSlot<T>(options: {
  key: string;
  storage: () => RendererMutationStorage;
  decode: (value: unknown) => T;
  encode: (value: T) => string;
  same: (left: T, right: T) => boolean;
}): RendererMutationSlot<T> {
  const read = (): T | null => {
    const raw = options.storage().getItem(options.key);
    return raw === null ? null : options.decode(JSON.parse(raw));
  };
  return {
    read,
    replace(expected, next) {
      const actual = read();
      const matches = actual === null ? expected === null : expected !== null && options.same(actual, expected);
      if (!matches) throw new RendererMutationSlotConflict(actual);
      options.storage().setItem(options.key, options.encode(next));
    },
    settle(expected, clear) {
      const actual = read();
      const remove = Boolean(clear && actual !== null && options.same(actual, expected));
      if (remove) options.storage().removeItem(options.key);
      return { remove, pending: remove ? null : actual };
    },
  };
}

const ACQUIRE_TIMEOUT_MS = 5_000;
let acquisitionId = 0;

/** One same-origin lock authority; there is no unlocked storage fallback. */
export async function withRendererMutationLock<T>(
  key: string,
  current: () => boolean,
  critical: () => T,
  options: { label?: string; resetKeyPrefix?: string } = {}
): Promise<T> {
  const label = options.label ?? 'Renderer';
  if (typeof navigator === 'undefined' || !navigator.locks?.request)
    throw new Error(`${label} pending storage lock unavailable`);

  const abort = new AbortController();
  const timeout = setTimeout(
    () => abort.abort(new Error(`${label} pending storage lock timed out`)),
    ACQUIRE_TIMEOUT_MS
  );
  const unregister = registerRendererAccountReset(
    `${options.resetKeyPrefix ?? 'renderer-mutation-lock'}:${++acquisitionId}`,
    () => abort.abort(new Error(`${label} account changed`))
  );
  try {
    if (!current()) abort.abort(new Error(`${label} account changed`));
    return await navigator.locks.request(key, { mode: 'exclusive', signal: abort.signal }, () => {
      if (abort.signal.aborted) throw abort.signal.reason;
      if (!current()) throw new Error(`${label} account changed`);
      clearTimeout(timeout);
      return critical();
    });
  } finally {
    clearTimeout(timeout);
    unregister();
  }
}
