import { describe, expect, it, vi } from 'vitest';
import { BackendHttpError, readBackendHttpError } from '@/common/adapter/httpBridge';
import { createXScienceAnnotationTarget } from '@/renderer/services/xScienceAnnotations';

const checksum = 'a'.repeat(64);
const input = { type: 'point' as const, text: 'Original request', xPercent: 50, yPercent: 50, pageNumber: 2 };

describe('annotation transport uses the shared typed backend-error authority', () => {
  it.each([
    [409, 'ANNOTATION_CREATE_IDEMPOTENCY_CONFLICT'],
    [410, 'ANNOTATION_COMMITTED_RESULT_MISSING'],
    [503, 'ANNOTATION_CREATE_OUTCOME_UNKNOWN'],
  ] as const)('retains status %s and code %s without inspecting message text', async (status, code) => {
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ code, detail: 'Original request retained' }), { status }));
    const error = await createXScienceAnnotationTarget(
      { kind: 'literature', versionId: 'litv_one' },
      input,
      checksum,
      fetch
    ).catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(BackendHttpError);
    expect(error).toMatchObject({ status, code, backendMessage: 'Original request retained' });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('accepts the current text returned by replay after the original note was edited', async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: '00000000-0000-4000-8000-000000000001',
          type: 'point',
          text: 'Later user edit',
          label: '①',
          x_percent: 50,
          y_percent: 50,
          page_number: 2,
          created_at: '2026-11-01T00:00:00Z',
          target_key: 'litv:litv_one',
          content_checksum: checksum,
        }),
        { status: 201 }
      )
    );
    const result = await createXScienceAnnotationTarget(
      { kind: 'literature', versionId: 'litv_one' },
      input,
      checksum,
      fetch
    );
    expect(result).toMatchObject({
      id: '00000000-0000-4000-8000-000000000001',
      text: 'Later user edit',
      targetKey: 'litv:litv_one',
      contentChecksum: checksum,
    });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('preserves a plain error status and bounds an oversized body with the original reader', async () => {
    const plain = await readBackendHttpError(
      'POST',
      '/api/annotations?private=value',
      new Response('Unavailable', { status: 404 })
    );
    expect(plain).toMatchObject({ status: 404, code: '', backendMessage: 'Unavailable' });
    expect(plain.message).not.toContain('private=value');
    const oversized = await readBackendHttpError(
      'POST',
      '/api/annotations',
      new Response('x'.repeat(20_000), { status: 503 })
    );
    expect(oversized.status).toBe(503);
    expect(JSON.stringify(oversized.body).length).toBeLessThan(1000);
  });

  it('sends an explicit Artifact checksum and key while keeping the canonical DTO', async () => {
    const key = '00000000-0000-4000-8000-000000000009';
    const abort = new AbortController();
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: '00000000-0000-4000-8000-000000000001',
          type: 'point',
          text: 'Current note',
          target_key: 'av:version-1',
          content_checksum: checksum,
        }),
        { status: 201 }
      )
    );
    await createXScienceAnnotationTarget(
      { kind: 'artifact', artifactId: 'artifact-1', versionId: 'version-1' },
      input,
      checksum,
      fetch,
      { idempotencyKey: key, signal: abort.signal }
    );
    const [path, init] = fetch.mock.calls[0];
    expect(path).toBe('/api/artifacts/artifact-1/versions/version-1/annotations');
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe(key);
    expect(init.signal).toBe(abort.signal);
    expect(JSON.parse(init.body)).toMatchObject({
      text: input.text,
      content_checksum: checksum,
      x_percent: 50,
      y_percent: 50,
    });
  });

  it('rejects an unexpected successful status for a keyed create instead of settling it', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ id: 'note', text: 'Current note' }), { status: 200 }));
    await expect(
      createXScienceAnnotationTarget({ kind: 'literature', versionId: 'litv_one' }, input, checksum, fetch, {
        idempotencyKey: 'key-one',
      })
    ).rejects.toThrow('acknowledgement status');
  });
});
