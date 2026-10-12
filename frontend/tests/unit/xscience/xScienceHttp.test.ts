import { describe, expect, it, vi } from 'vitest';
import { isXScienceHttpError, requestXScienceJson, XScienceHttpError } from '@/renderer/services/xScienceHttp';

const jsonResponse = (value: unknown) =>
  ({
    ok: true,
    text: async () => JSON.stringify(value),
  }) as Response;

const errorResponse = (value: unknown, status = 400) =>
  ({
    ok: false,
    status,
    text: async () => (typeof value === 'string' ? value : JSON.stringify(value)),
  }) as Response;

describe('requestXScienceJson cancellation', () => {
  it('forwards the gateway abort signal to the real fetch boundary', async () => {
    const controller = new AbortController();
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ ok: true })) as unknown as typeof fetch;

    await requestXScienceJson('/api/test', {}, { fetchImpl, signal: controller.signal });

    expect(fetchImpl).toHaveBeenCalledWith(
      '/api/test',
      expect.objectContaining({ credentials: 'include', signal: controller.signal })
    );
  });

  it('keeps an explicit request signal authoritative over the gateway default', async () => {
    const requestController = new AbortController();
    const defaultController = new AbortController();
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ ok: true })) as unknown as typeof fetch;

    await requestXScienceJson(
      '/api/test',
      { signal: requestController.signal },
      { fetchImpl, signal: defaultController.signal }
    );

    expect(fetchImpl).toHaveBeenCalledWith('/api/test', expect.objectContaining({ signal: requestController.signal }));
  });
});

describe('requestXScienceJson timeout boundary', () => {
  it('aborts a request that exceeds its explicit timeout', async () => {
    vi.useFakeTimers();
    try {
      const fetchImpl = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener(
            'abort',
            () => reject(new DOMException('X-Science request timed out', 'TimeoutError')),
            { once: true }
          );
        });
      }) as unknown as typeof fetch;

      const request = requestXScienceJson('/api/slow', {}, { fetchImpl, timeoutMs: 1000 });
      const rejection = expect(request).rejects.toMatchObject({ name: 'TimeoutError' });
      await vi.advanceTimersByTimeAsync(1000);

      await rejection;
      expect(fetchImpl).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('requestXScienceJson error boundary', () => {
  it('keeps structured backend details available without leaking them through Error.message', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      errorResponse(
        {
          error: 'provider rejected sk-sensitiveValue123456',
          code: 'PROVIDER_REJECTED',
          details: { retryable: false },
        },
        503
      )
    ) as unknown as typeof fetch;

    const promise = requestXScienceJson('/api/runtime?token=secret', {}, { fetchImpl });

    await expect(promise).rejects.toMatchObject({
      name: 'XScienceHttpError',
      status: 503,
      code: 'PROVIDER_REJECTED',
      backendMessage: 'provider rejected sk-sensitiveValue123456',
      details: { retryable: false },
    });
    await promise.catch((error: unknown) => {
      expect(error).toBeInstanceOf(XScienceHttpError);
      expect(isXScienceHttpError(error)).toBe(true);
      expect((error as Error).message).toBe('X-Science GET /api/runtime failed (503) [PROVIDER_REJECTED]');
      expect((error as Error).message).not.toContain('secret');
      expect((error as Error).message).not.toContain('sensitiveValue123456');
    });
  });

  it.each([
    [{ detail: 'legacy private detail' }, 'legacy private detail'],
    [{ message: 'legacy private message' }, 'legacy private message'],
    ['plain private response', 'plain private response'],
  ])('preserves legacy backend diagnostics outside the safe summary', async (body, expectedMessage) => {
    const fetchImpl = vi.fn().mockResolvedValue(errorResponse(body, 422)) as unknown as typeof fetch;

    const promise = requestXScienceJson('/api/runtime', { method: 'PATCH' }, { fetchImpl });

    await promise.catch((error: unknown) => {
      expect(isXScienceHttpError(error)).toBe(true);
      expect((error as XScienceHttpError).backendMessage).toBe(expectedMessage);
      expect((error as Error).message).toBe('X-Science PATCH /api/runtime failed (422)');
      expect((error as Error).message).not.toContain(expectedMessage);
    });
  });

  it('never includes an untrusted error code in the diagnostic summary', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(errorResponse({ error: 'bad request', code: 'INVALID\nsecret' })) as unknown as typeof fetch;

    const promise = requestXScienceJson('/api/runtime', {}, { fetchImpl });

    await promise.catch((error: unknown) => {
      expect(isXScienceHttpError(error)).toBe(true);
      expect((error as XScienceHttpError).code).toBe('INVALID\nsecret');
      expect((error as Error).message).toBe('X-Science GET /api/runtime failed (400)');
    });
  });
});
