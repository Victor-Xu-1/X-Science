import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  loadSequenceContent,
  MAX_SEQUENCE_BYTES,
} from '@/renderer/pages/conversation/Preview/components/viewers/XScienceSequenceViewer';

afterEach(() => vi.unstubAllGlobals());

async function gzipBytes(source: string): Promise<Uint8Array> {
  if (typeof CompressionStream === 'undefined') {
    throw new Error('CompressionStream is required for gzip fixtures');
  }
  const stream = new Blob([source]).stream().pipeThrough(new CompressionStream('gzip'));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

describe('bounded sequence content transport', () => {
  it('reads a bounded plain GenBank response', async () => {
    const body = 'LOCUS       tiny\n//';
    const fetchMock = vi.fn().mockResolvedValue(new Response(body));
    vi.stubGlobal('fetch', fetchMock);
    const signal = new AbortController().signal;
    await expect(
      loadSequenceContent({ filename: 'tiny.gbk', contentUrl: '/artifact/seq', signal })
    ).resolves.toBe(body);
    expect(fetchMock).toHaveBeenCalledWith('/artifact/seq', {
      credentials: 'include',
      headers: { Accept: 'text/plain, application/octet-stream, chemical/seq-na-genbank' },
      signal,
    });
  });

  it('rejects a declared oversize response and cancels its unread body', async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({ cancel });
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(stream, {
          headers: { 'content-length': String(MAX_SEQUENCE_BYTES + 1) },
        })
      )
    );
    await expect(
      loadSequenceContent({ filename: 'large.gbk', contentUrl: '/artifact/large', signal: new AbortController().signal })
    ).rejects.toMatchObject({
      code: 'too-large',
      details: { limit: '10 MB' },
    });
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it.each([undefined, '1'])('enforces streamed body size with content-length %s', async (declared) => {
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
    await expect(
      loadSequenceContent({ filename: 'large.gbk', contentUrl: '/artifact/large', signal: new AbortController().signal })
    ).rejects.toMatchObject({ code: 'too-large' });
    expect(pulls).toBeLessThanOrEqual(12);
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it('enforces compressed .gz budgets before decompression completes', async () => {
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
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    await expect(
      loadSequenceContent({ filename: 'large.fasta.gz', contentUrl: '/artifact/gz', signal: new AbortController().signal })
    ).rejects.toMatchObject({ code: 'too-large' });
    expect(pulls).toBeLessThanOrEqual(12);
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it('rejects decompressed gzip output that exceeds the preview budget', async () => {
    const inflated = 'A'.repeat(MAX_SEQUENCE_BYTES + 1);
    const compressed = await gzipBytes(inflated);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(compressed)));
    await expect(
      loadSequenceContent({ filename: 'inflated.fasta.gz', contentUrl: '/artifact/gz', signal: new AbortController().signal })
    ).rejects.toMatchObject({
      code: 'too-large',
      details: { limit: '10 MB' },
    });
  });

  it('accepts transport-decoded gzip bodies within the limit', async () => {
    const body = 'LOCUS       decoded\n//';
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(body, {
          headers: { 'content-encoding': 'gzip' },
        })
      )
    );
    await expect(
      loadSequenceContent({
        filename: 'decoded.fasta.gz',
        contentUrl: '/artifact/gz',
        signal: new AbortController().signal,
      })
    ).resolves.toBe(body);
  });

  it('cancels and releases a body when the caller aborts', async () => {
    const abort = new AbortController();
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        abort.abort();
        controller.enqueue(new Uint8Array([76]));
      },
      cancel,
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    await expect(
      loadSequenceContent({ filename: 'abort.gbk', contentUrl: '/artifact/seq', signal: abort.signal })
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });
});
