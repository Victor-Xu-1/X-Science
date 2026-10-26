import { describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  classifyXScienceAttachmentInitFailure,
  decodeOnboardingAttachmentInitReceipt,
  initializeXScienceProjectAttachment,
  type OnboardingAttachmentInitRequest,
} from '@/renderer/services/attachments/attachmentUploadInit';

const raw: OnboardingAttachmentInitRequest = Object.freeze({
  operation_id: '47581341-e76e-4e23-a532-15f487dfd914',
  project_id: 'owned-project',
  filename: 'reference.pdf',
  total_size: 12,
  content_type: 'application/pdf',
  chunk_size: 0,
});
const receipt = { upload_id: 'd2af72a7-c6d4-4a25-8192-429f61d81759', chunk_size: 4, total_chunks: 3 };
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};

describe('keyed attachment admission producer', () => {
  it('sends one frozen raw request and matching caller key, then decodes the immutable acknowledgement', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () => response(receipt));
    await expect(initializeXScienceProjectAttachment(raw, { fetchImpl })).resolves.toEqual(receipt);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    const [path, init] = fetchImpl.mock.calls[0];
    expect(path).toBe('/api/artifacts/upload/init');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('include');
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(raw.operation_id);
    expect(JSON.parse(String(init?.body))).toEqual(raw);
    expect(raw.chunk_size).toBe(0);
  });

  it('rejects invalid local identity or metadata before dispatch without replacing the UUID', async () => {
    const fetchImpl = vi.fn<typeof fetch>();
    for (const change of [
      { operation_id: '' },
      { operation_id: raw.operation_id.toUpperCase() },
      { project_id: ' ' },
      { filename: ' ' },
      { total_size: 0 },
      { total_size: Number.MAX_SAFE_INTEGER + 1 },
      { chunk_size: -1 },
      { ephemeral: 'false' },
    ]) {
      await expect(
        initializeXScienceProjectAttachment({ ...raw, ...change } as OnboardingAttachmentInitRequest, { fetchImpl })
      ).rejects.toThrow('Attachment admission request is invalid');
    }
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('leaves a lost acknowledgement unknown and reuses the same raw identity only on an explicit retry', async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError('acknowledgement lost'))
      .mockResolvedValueOnce(response(receipt));
    const failure = await initializeXScienceProjectAttachment(raw, { fetchImpl }).catch((error: unknown) => error);
    expect(classifyXScienceAttachmentInitFailure(failure)).toBe('unknown');
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    await expect(initializeXScienceProjectAttachment(raw, { fetchImpl })).resolves.toEqual(receipt);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
    for (const [, init] of fetchImpl.mock.calls) {
      expect(JSON.parse(String(init?.body))).toEqual(raw);
      expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(raw.operation_id);
    }
  });

  it('consumes only scoped explicit certainty and preserves the original capacity error details', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async () =>
      response(
        {
          code: 'ATTACHMENT_INIT_WRITE_REJECTED',
          detail: 'Insufficient upload storage',
          required_bytes: 12,
          available_bytes: 4,
        },
        507
      )
    );
    const failure = await initializeXScienceProjectAttachment(raw, { fetchImpl }).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(BackendHttpError);
    expect(classifyXScienceAttachmentInitFailure(failure)).toBe('rejected');
    expect((failure as BackendHttpError).body).toMatchObject({ required_bytes: 12, available_bytes: 4 });
    for (const [status, code, expected] of [
      [503, 'ATTACHMENT_INIT_OUTCOME_UNKNOWN', 'unknown'],
      [400, 'ATTACHMENT_INIT_OUTCOME_UNKNOWN', 'unknown'],
      [409, 'ATTACHMENT_INIT_IDEMPOTENCY_CONFLICT', 'conflict'],
      [400, 'ATTACHMENT_INIT_WRITE_REJECTED', 'rejected'],
      [404, 'ATTACHMENT_INIT_WRITE_REJECTED', 'rejected'],
      [500, 'ATTACHMENT_INIT_WRITE_REJECTED', 'unknown'],
      [400, 'UNRELATED_INVALID', 'unknown'],
      [403, '', 'unknown'],
    ] as const) {
      const error = new BackendHttpError({
        method: 'POST',
        path: '/api/artifacts/upload/init',
        status,
        body: { code },
      });
      expect(classifyXScienceAttachmentInitFailure(error)).toBe(expected);
    }
    expect(classifyXScienceAttachmentInitFailure(new Error('invalid acknowledgement'))).toBe('unknown');
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('never treats malformed successful acknowledgements as rejection or retries them automatically', async () => {
    for (const body of [
      null,
      {},
      { ...receipt, upload_id: 'upload-legacy' },
      { ...receipt, chunk_size: 0 },
      { ...receipt, total_chunks: 1.5 },
      { ...receipt, chunk_size: Number.MAX_SAFE_INTEGER + 1 },
    ]) {
      const fetchImpl = vi.fn<typeof fetch>(async () => response(body));
      const failure = await initializeXScienceProjectAttachment(raw, { fetchImpl }).catch((error: unknown) => error);
      expect(failure).toBeInstanceOf(Error);
      expect(classifyXScienceAttachmentInitFailure(failure)).toBe('unknown');
      expect(fetchImpl).toHaveBeenCalledTimes(1);
    }
    expect(decodeOnboardingAttachmentInitReceipt({ ...receipt, future_metadata: true })).toEqual(receipt);
  });

  it('cancels noncooperative pending headers promptly and ignores a late successful response', async () => {
    const pending = deferred<Response>();
    const controller = new AbortController();
    const fetchImpl = vi.fn<typeof fetch>(() => pending.promise);
    const result = initializeXScienceProjectAttachment(raw, { fetchImpl, signal: controller.signal });
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(1));
    controller.abort();
    await rejected;
    const late = response(receipt);
    const text = vi.spyOn(late, 'text');
    pending.resolve(late);
    await Promise.resolve();
    await Promise.resolve();
    expect(text).not.toHaveBeenCalled();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('cancels pending response bodies and observes their late rejection without another request', async () => {
    const pending = deferred<string>();
    const controller = new AbortController();
    const received = response(receipt);
    const text = vi.spyOn(received, 'text').mockImplementation(() => pending.promise);
    const fetchImpl = vi.fn<typeof fetch>(async () => received);
    const result = initializeXScienceProjectAttachment(raw, { fetchImpl, signal: controller.signal });
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(text).toHaveBeenCalledTimes(1));
    controller.abort();
    await rejected;
    pending.reject(new Error('late body failure'));
    await Promise.resolve();
    await Promise.resolve();
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('does not dispatch an already cancelled caller request', async () => {
    const controller = new AbortController();
    controller.abort();
    const fetchImpl = vi.fn<typeof fetch>();
    await expect(
      initializeXScienceProjectAttachment(raw, { fetchImpl, signal: controller.signal })
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetchImpl).not.toHaveBeenCalled();
  });
});
