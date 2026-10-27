import { sha256 } from '@noble/hashes/sha2.js';

// A read window, not a limit on the user's file. Keep only one window in flight.
const SLICE_BYTES = 256 * 1024;

function abortError(signal: AbortSignal): DOMException {
  return signal.reason instanceof DOMException && signal.reason.name === 'AbortError'
    ? signal.reason
    : new DOMException('File digest was cancelled', 'AbortError');
}

function requireActive(signal?: AbortSignal): void {
  if (signal?.aborted) throw abortError(signal);
}

/** Observe even a noncooperative late Blob read while settling cancellation promptly. */
function abortable<T>(signal: AbortSignal | undefined, operation: () => Promise<T>, cancel?: () => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const cleanup = () => signal?.removeEventListener('abort', abort);
    const abort = () => {
      cleanup();
      cancel?.();
      reject(abortError(signal!));
    };
    if (signal?.aborted) {
      abort();
      return;
    }
    signal?.addEventListener('abort', abort, { once: true });
    void Promise.resolve()
      .then(() => {
        requireActive(signal);
        return operation();
      })
      .then(
        (value) => {
          if (signal?.aborted) {
            abort();
            return;
          }
          cleanup();
          resolve(value);
        },
        (error: unknown) => {
          cleanup();
          reject(signal?.aborted ? abortError(signal) : error);
        }
      );
  });
}

function yieldToBrowser(signal?: AbortSignal): Promise<void> {
  let timer: ReturnType<typeof globalThis.setTimeout> | undefined;
  return abortable(
    signal,
    () => new Promise<void>((resolve) => (timer = globalThis.setTimeout(resolve, 0))),
    () => {
      if (timer !== undefined) globalThis.clearTimeout(timer);
    }
  );
}

/** Client-side byte continuity only; the server remains the artifact checksum authority. */
export async function digestLibraryFile(file: Blob, signal?: AbortSignal): Promise<string> {
  requireActive(signal);
  if (!(file instanceof Blob) || !Number.isSafeInteger(file.size) || file.size < 0)
    throw new TypeError('A valid Blob is required for a file digest');
  const size = file.size;
  const hasher = sha256.create();
  try {
    for (let offset = 0; offset < size;) {
      requireActive(signal);
      const end = Math.min(size, offset + SLICE_BYTES);
      {
        const bytes = await abortable(signal, () => file.slice(offset, end).arrayBuffer());
        requireActive(signal);
        if (bytes.byteLength !== end - offset) throw new Error('File digest read was incomplete');
        hasher.update(new Uint8Array(bytes));
      }
      offset = end;
      if (offset < size) await yieldToBrowser(signal);
    }
    requireActive(signal);
    return Array.from(hasher.digest(), (byte) => byte.toString(16).padStart(2, '0')).join('');
  } finally {
    hasher.destroy();
  }
}
