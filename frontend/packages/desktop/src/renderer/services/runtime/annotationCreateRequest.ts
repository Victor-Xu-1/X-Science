/** Bound one HTTP wait; aborting a request never proves the server did not commit. */
export function waitForAnnotationCreate<T>(
  abort: AbortController,
  timeoutMs: number,
  operation: (signal: AbortSignal) => Promise<T>
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => abort.abort(new Error('Annotation acknowledgement timed out')), timeoutMs);
    const cleanup = () => {
      clearTimeout(timer);
      abort.signal.removeEventListener('abort', cancelled);
    };
    const cancelled = () => {
      cleanup();
      reject(abort.signal.reason ?? new DOMException('Request cancelled', 'AbortError'));
    };
    if (abort.signal.aborted) {
      cancelled();
      return;
    }
    abort.signal.addEventListener('abort', cancelled, { once: true });
    void Promise.resolve()
      .then(() => {
        if (abort.signal.aborted) throw abort.signal.reason;
        return operation(abort.signal);
      })
      .then(
        (value) => {
          cleanup();
          if (abort.signal.aborted) reject(abort.signal.reason);
          else resolve(value);
        },
        (error: unknown) => {
          cleanup();
          reject(error);
        }
      );
  });
}
