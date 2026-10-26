import { BackendHttpError } from '@/common/adapter/httpBridge';

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError';
}

export function attachmentAbortError(signal: AbortSignal): DOMException {
  return isAbortError(signal.reason)
    ? (signal.reason as DOMException)
    : new DOMException('Attachment upload was cancelled', 'AbortError');
}

export function throwIfAttachmentAborted(signal?: AbortSignal | null): void {
  if (signal?.aborted) throw attachmentAbortError(signal);
}

export function cancelAttachmentResponse(response?: Response): void {
  try {
    void response?.body?.cancel().catch((): void => undefined);
  } catch {
    // Fetch's original signal owns a locked body reader; cancellation is best effort.
  }
}

export function withAttachmentAbort<T>(
  signal: AbortSignal | null | undefined,
  operation: () => Promise<T>,
  onAbort?: () => void
): Promise<T> {
  if (!signal) return operation();
  if (signal.aborted) return Promise.reject(attachmentAbortError(signal));
  let rejectAbort!: (reason: unknown) => void;
  const cancelled = new Promise<never>((_resolve, reject) => {
    rejectAbort = reject;
  });
  const abort = () => {
    onAbort?.();
    rejectAbort(attachmentAbortError(signal));
  };
  signal.addEventListener('abort', abort, { once: true });
  const work = Promise.resolve()
    .then(() => {
      throwIfAttachmentAborted(signal);
      return operation();
    })
    .then((value) => {
      throwIfAttachmentAborted(signal);
      return value;
    });
  // Both promises remain observed, including a noncooperative late response.
  return Promise.race([work, cancelled]).finally(() => signal.removeEventListener('abort', abort));
}

export async function requestJson(
  path: string,
  options: { fetchImpl: typeof fetch; init?: RequestInit }
): Promise<unknown> {
  const signal = options.init?.signal;
  let response: Response | undefined;
  return withAttachmentAbort(
    signal,
    async () => {
      response = await options.fetchImpl(path, {
        credentials: 'include',
        ...options.init,
        headers: { Accept: 'application/json', ...options.init?.headers },
      });
      if (signal?.aborted) cancelAttachmentResponse(response);
      throwIfAttachmentAborted(signal);
      const text = await response.text();
      throwIfAttachmentAborted(signal);
      const payload = text ? safeJson(text) : null;
      if (!response.ok) {
        throw new BackendHttpError({
          method: options.init?.method ?? 'GET',
          path,
          status: response.status,
          body: payload ?? response.statusText,
        });
      }
      return payload;
    },
    () => cancelAttachmentResponse(response)
  );
}
export function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}
