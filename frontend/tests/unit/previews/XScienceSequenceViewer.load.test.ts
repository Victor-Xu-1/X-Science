import { afterEach, describe, expect, it, vi } from 'vitest';
import { gzipSync } from 'node:zlib';
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
    await expect(loadSequenceContent({ filename: 'tiny.gbk', contentUrl: '/artifact/seq', signal })).resolves.toBe(
      body
    );
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
      loadSequenceContent({
        filename: 'large.gbk',
        contentUrl: '/artifact/large',
        signal: new AbortController().signal,
      })
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
      loadSequenceContent({
        filename: 'large.gbk',
        contentUrl: '/artifact/large',
        signal: new AbortController().signal,
      })
    ).rejects.toMatchObject({ code: 'too-large' });
    expect(pulls).toBeLessThanOrEqual(12);
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it('enforces compressed .gz budgets before decompression completes', async () => {
    const cancel = vi.fn();
    // Valid incompressible gzip exercises the raw budget before a format error
    // can terminate decoding. Binary input also excludes unrelated UTF-8 errors.
    // An endless all-zero stream is malformed gzip.
    const source = Buffer.alloc(12 * 1024 * 1024);
    let seed = 0x12345678;
    for (let i = 0; i < source.byteLength; i++) {
      seed ^= seed << 13;
      seed ^= seed >>> 17;
      seed ^= seed << 5;
      source[i] = seed & 0xff;
    }
    const compressed = gzipSync(source);
    expect(compressed.byteLength).toBeGreaterThan(MAX_SEQUENCE_BYTES);
    let pulls = 0;
    let offset = 0;
    let complete = false;
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        pulls += 1;
        if (offset === compressed.byteLength) {
          complete = true;
          controller.close();
          return;
        }
        const end = Math.min(offset + 1024 * 1024, compressed.byteLength);
        controller.enqueue(compressed.subarray(offset, end));
        offset = end;
      },
      cancel,
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    await expect(
      loadSequenceContent({
        filename: 'large.dna.gz',
        contentUrl: '/artifact/gz',
        signal: new AbortController().signal,
      })
    ).rejects.toMatchObject({ code: 'too-large' });
    expect(pulls).toBeLessThanOrEqual(12);
    expect(complete).toBe(false);
    expect(offset).toBeLessThan(compressed.byteLength);
    expect(cancel).toHaveBeenCalledOnce();
    expect(stream.locked).toBe(false);
  });

  it('rejects decompressed gzip output that exceeds the preview budget', async () => {
    const inflated = 'A'.repeat(MAX_SEQUENCE_BYTES + 1);
    const compressed = await gzipBytes(inflated);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(compressed)));
    await expect(
      loadSequenceContent({
        filename: 'inflated.fasta.gz',
        contentUrl: '/artifact/gz',
        signal: new AbortController().signal,
      })
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

  it('preserves forgiving plain-text UTF-8 decoding for a descriptive FASTA header', async () => {
    const bytes = new Uint8Array([62, 114, 255, 10, 65, 67, 71, 84, 10]);
    const expected = await new Response(bytes).text();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(bytes)));
    await expect(
      loadSequenceContent({
        filename: 'legacy.fasta',
        contentUrl: '/artifact/seq',
        signal: new AbortController().signal,
      })
    ).resolves.toBe(expected);
    expect(expected).toBe('>r\ufffd\nACGT\n');
  });

  it('ignores compressed transport Content-Length while bounding delivered decoded bytes', async () => {
    const source = '>decoded\nACGT\n';
    vi.stubGlobal('DecompressionStream', undefined);
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(source, {
          headers: { 'content-encoding': ' GZip ', 'content-length': String(MAX_SEQUENCE_BYTES + 1) },
        })
      )
    );
    await expect(
      loadSequenceContent({
        filename: 'decoded.fasta.gz',
        contentUrl: '/artifact/gz',
        signal: new AbortController().signal,
      })
    ).resolves.toBe(source);
  });

  it.each([false, true])(
    'interrupts a pending read and retains AbortError when cancellation rejects=%s',
    async (rejectCancel) => {
      const abort = new AbortController();
      let entered!: () => void;
      const reading = new Promise<void>((resolve) => {
        entered = resolve;
      });
      const cancel = vi.fn(() => {
        if (rejectCancel) throw new Error('synthetic cancellation rejection');
      });
      const stream = new ReadableStream<Uint8Array>(
        {
          pull() {
            entered();
            return new Promise<void>(() => {});
          },
          cancel,
        },
        { highWaterMark: 0 }
      );
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
      const pending = loadSequenceContent({
        filename: 'pending.fasta',
        contentUrl: '/artifact/seq',
        signal: abort.signal,
      });
      const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' });
      await reading;
      abort.abort();
      await rejected;
      expect(cancel).toHaveBeenCalledOnce();
      expect(stream.locked).toBe(false);
    }
  );

  it.each(['oversize.fasta', 'oversize.fasta.gz'])(
    'retains overflow classification when %s cancellation rejects',
    async (filename) => {
      const cancel = vi.fn(() => {
        throw new Error('synthetic cancellation rejection');
      });
      const stream = new ReadableStream<Uint8Array>(
        {
          pull(controller) {
            controller.enqueue(new Uint8Array(MAX_SEQUENCE_BYTES + 1));
          },
          cancel,
        },
        { highWaterMark: 0 }
      );
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
      await expect(
        loadSequenceContent({ filename, contentUrl: '/artifact/seq', signal: new AbortController().signal })
      ).rejects.toMatchObject({ code: 'too-large', details: { limit: '10 MB' } });
      expect(cancel).toHaveBeenCalledOnce();
      // A gzip pipe settles its upstream lock after the reader rejects.
      await vi.waitFor(() => expect(stream.locked).toBe(false));
    }
  );

  it.each([false, true])(
    'cancels an unread HTTP-error body and retains its status when cancellation rejects=%s',
    async (rejectCancel) => {
      const cancel = vi.fn(() => {
        if (rejectCancel) throw new Error('synthetic cancellation rejection');
      });
      const pull = vi.fn();
      const stream = new ReadableStream<Uint8Array>({ pull, cancel }, { highWaterMark: 0 });
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream, { status: 403 })));
      await expect(
        loadSequenceContent({
          filename: 'denied.fasta',
          contentUrl: '/artifact/seq',
          signal: new AbortController().signal,
        })
      ).rejects.toMatchObject({ code: 'request-failed', details: { status: 403 } });
      expect(pull).not.toHaveBeenCalled();
      expect(cancel).toHaveBeenCalledOnce();
      expect(stream.locked).toBe(false);
    }
  );
});
