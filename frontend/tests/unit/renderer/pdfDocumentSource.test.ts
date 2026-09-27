import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadPdfDocumentBytes, MAX_PDF_PREVIEW_BYTES } from '@/renderer/pages/artifact/pdfDocumentSource';

afterEach(() => vi.unstubAllGlobals());

describe('bounded PDF source transport', () => {
  it('reads actual streamed bytes through the authenticated transport', async () => {
    const bytes = new Uint8Array([37, 80, 68, 70, 45]);
    const fetchMock = vi.fn().mockResolvedValue(new Response(bytes));
    vi.stubGlobal('fetch', fetchMock);
    const signal = new AbortController().signal;
    await expect(loadPdfDocumentBytes('/api/artifacts/pdf', signal)).resolves.toEqual(bytes);
    expect(fetchMock).toHaveBeenCalledWith('/api/artifacts/pdf', { credentials: 'same-origin', signal });
  });

  it('rejects a declared oversize response and cancels its unread body', async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({ cancel });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(stream, {
          headers: { 'content-length': String(MAX_PDF_PREVIEW_BYTES + 1) },
        })
      )
    );
    await expect(loadPdfDocumentBytes('/api/artifacts/large', new AbortController().signal)).rejects.toMatchObject({
      code: 'too-large',
      details: { limit: '64 MB' },
    });
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it.each([undefined, '1'])('enforces decoded stream size with content-length %s', async (declared) => {
    const cancel = vi.fn();
    const chunk = new Uint8Array(1024 * 1024);
    let pulls = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        pulls += 1;
        controller.enqueue(chunk);
      },
      cancel,
    });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(stream, {
          headers: declared ? { 'content-length': declared } : {},
        })
      )
    );
    await expect(loadPdfDocumentBytes('/api/artifacts/large', new AbortController().signal)).rejects.toMatchObject({
      code: 'too-large',
    });
    expect(pulls).toBeLessThanOrEqual(66);
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it('cancels and releases a body when the caller aborts', async () => {
    const abort = new AbortController();
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        abort.abort();
        controller.enqueue(new Uint8Array([37]));
      },
      cancel,
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    await expect(loadPdfDocumentBytes('/api/artifacts/pdf', abort.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });
});
