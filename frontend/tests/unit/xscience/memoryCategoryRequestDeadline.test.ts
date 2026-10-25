import { expect, it, vi } from 'vitest';
import {
  createXScienceMemoryCategory,
  updateXScienceMemoryCategory,
  loadXScienceMemoryContext,
  XScienceMemoryRequestError,
} from '../../../packages/desktop/src/renderer/services/xScienceMemory';

it('settles a stalled category response body within the finite metadata request budget', async () => {
  vi.useFakeTimers();
  let body: ReadableStreamDefaultController<Uint8Array> | undefined;
  let observedSignal: AbortSignal | null | undefined;
  let rejectBody: (() => void) | undefined;
  let outcome: 'pending' | 'resolved' | 'rejected' = 'pending';
  let observedError: unknown;
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      body = controller;
      controller.enqueue(new TextEncoder().encode('{"id":'));
    },
  });
  const fetchImpl: typeof fetch = async (_input, init) => {
    observedSignal = init?.signal;
    rejectBody = () => body?.error(new DOMException('request body aborted', 'AbortError'));
    observedSignal?.addEventListener('abort', rejectBody, { once: true });
    return new Response(stream, { status: 201, headers: { 'Content-Type': 'application/json' } });
  };
  const request = createXScienceMemoryCategory(
    { name: 'Synthetic category', guidance: 'Synthetic routing guidance', autoRecall: true },
    { fetchImpl }
  ).then(
    () => {
      outcome = 'resolved';
    },
    (error: unknown) => {
      outcome = 'rejected';
      observedError = error;
    }
  );
  try {
    await Promise.resolve();
    // One total metadata deadline must also cover the complete response body.
    await vi.advanceTimersByTimeAsync(30_001);
    expect(outcome).toBe('rejected');
    expect(observedError).toBeInstanceOf(Error);
    expect(observedSignal?.aborted).toBe(true);
  } finally {
    if (observedSignal && rejectBody) observedSignal.removeEventListener('abort', rejectBody);
    try {
      body?.error(new DOMException('fixture cleanup', 'AbortError'));
    } catch {
      /* Already settled. */
    }
    await request;
    vi.useRealTimers();
  }
});

it('bounds headers and ignores a late response after deadline', async () => {
  vi.useFakeTimers();
  let release!: (response: Response) => void;
  let signal: AbortSignal | null | undefined;
  const late = new Response('{"id":"late","name":"late"}', { status: 201 });
  const read = vi.spyOn(late, 'text');
  const fetchImpl: typeof fetch = (_input, init) => {
    signal = init?.signal;
    return new Promise<Response>((resolve) => {
      release = resolve;
    });
  };
  const result = createXScienceMemoryCategory(
    { name: 'Methods', guidance: 'Synthetic guidance' },
    { fetchImpl, timeoutMs: 25 }
  ).catch((error: unknown) => error);
  try {
    await vi.advanceTimersByTimeAsync(26);
    expect(await result).toMatchObject({ reason: 'deadline', stage: 'headers', categoryOutcome: 'unknown' });
    expect(signal?.aborted).toBe(true);
    release(late);
    await Promise.resolve();
    await Promise.resolve();
    expect(read).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  } finally {
    release(late);
    await result;
    vi.useRealTimers();
  }
});

it('bounds a noncooperative body and observes its late result without a second outcome', async () => {
  vi.useFakeTimers();
  let release!: (value: string) => void;
  const response = new Response('{}', { status: 201 });
  vi.spyOn(response, 'text').mockImplementation(
    () =>
      new Promise<string>((resolve) => {
        release = resolve;
      })
  );
  const settled = vi.fn();
  const result = createXScienceMemoryCategory(
    { name: 'Methods', guidance: 'Synthetic guidance' },
    { timeoutMs: 20, fetchImpl: async () => response }
  ).then(settled, (error: unknown) => {
    settled(error);
    return error;
  });
  try {
    await vi.advanceTimersByTimeAsync(21);
    expect(await result).toMatchObject({ reason: 'deadline', stage: 'body' });
    release('{"id":"late","name":"late"}');
    await Promise.resolve();
    await Promise.resolve();
    expect(settled).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  } finally {
    release('{}');
    await result;
    vi.useRealTimers();
  }
});

it('rejects invalid timer overrides before dispatch', async () => {
  const fetchImpl = vi.fn<typeof fetch>();
  for (const timeoutMs of [0, -1, 1.5, NaN, Infinity, 2_147_483_648]) {
    await expect(
      createXScienceMemoryCategory({ name: 'Methods', guidance: 'Synthetic guidance' }, { fetchImpl, timeoutMs })
    ).rejects.toBeInstanceOf(RangeError);
  }
  expect(fetchImpl).not.toHaveBeenCalled();
});

it('classifies only authoritative category codes and omits private error detail', async () => {
  const cases = [
    [400, undefined, 'unknown'],
    [503, undefined, 'unknown'],
    [400, 'MEMORY_CATEGORY_WRITE_REJECTED', 'rejected'],
    [503, 'MEMORY_CATEGORY_OUTCOME_UNKNOWN', 'unknown'],
    [410, 'MEMORY_CATEGORY_COMMITTED_RESULT_MISSING', 'committed-missing'],
    [409, 'MEMORY_CATEGORY_IDEMPOTENCY_CONFLICT', 'conflict'],
    [503, 'MEMORY_CATEGORY_WRITE_REJECTED', 'unknown'],
  ] as const;
  for (const [status, code, expected] of cases) {
    const error: unknown = await createXScienceMemoryCategory(
      { name: 'Methods', guidance: 'private guidance' },
      {
        fetchImpl: async () => Response.json({ detail: 'private guidance', ...(code ? { code } : {}) }, { status }),
      }
    ).catch((value: unknown) => value);
    expect(error).toBeInstanceOf(XScienceMemoryRequestError);
    expect(error).toMatchObject({ categoryOutcome: expected, status });
    expect((error as Error).message).not.toContain('private guidance');
  }
});

it('aborts the actual request from its caller and releases deadline resources', async () => {
  vi.useFakeTimers();
  const caller = new AbortController();
  let release!: (response: Response) => void;
  let actual: AbortSignal | null | undefined;
  const result = createXScienceMemoryCategory(
    { name: 'Methods', guidance: 'Synthetic guidance' },
    {
      signal: caller.signal,
      fetchImpl: (_input, init) => {
        actual = init?.signal;
        return new Promise<Response>((yes) => {
          release = yes;
        });
      },
    }
  ).catch((error: unknown) => error);
  try {
    await Promise.resolve();
    caller.abort('account reset');
    expect(await result).toMatchObject({ reason: 'abort', categoryOutcome: 'unknown' });
    expect(actual?.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  } finally {
    release(new Response(null, { status: 204 }));
    await result;
    vi.useRealTimers();
  }
});

it('preserves raw GET parsing and sends the original operation key on category writes only', async () => {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const fetchImpl: typeof fetch = async (input, init) => {
    calls.push({ url: String(input), init });
    if (String(input).endsWith('/api/memory/context'))
      return new Response(JSON.stringify({ memory_enabled: false, total_user_rows: 7, categories: [] }));
    const body = JSON.parse(String(init?.body));
    return Response.json({ id: 'category-one', ...body }, { status: init?.method === 'POST' ? 201 : 200 });
  };
  const options = { baseUrl: 'https://synthetic.invalid/', fetchImpl, operationId: 'synthetic-operation-001' };
  expect(await loadXScienceMemoryContext({}, options)).toMatchObject({ enabled: false, totalRows: 7 });
  await createXScienceMemoryCategory({ name: 'Methods', guidance: 'Synthetic guidance' }, options);
  await updateXScienceMemoryCategory(
    'category-one',
    { name: 'Methods', guidance: 'Synthetic guidance', autoRecall: true },
    options
  );
  expect(new Headers(calls[0].init?.headers).get('Idempotency-Key')).toBeNull();
  expect(calls[0].url).toBe('https://synthetic.invalid/api/memory/context');
  for (const call of calls.slice(1)) {
    expect(new Headers(call.init?.headers).get('Idempotency-Key')).toBe('synthetic-operation-001');
    expect(call.init?.credentials).toBe('include');
  }
});
