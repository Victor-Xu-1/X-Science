import React from 'react';
import { cleanup, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Blob as NativeBlob } from 'node:buffer';
import {
  DecompressionStream as NativeDecompressionStream,
  ReadableStream as NativeReadableStream,
} from 'node:stream/web';
import { gzipSync } from 'node:zlib';

vi.mock('@arco-design/web-react', () => ({ Spin: () => <div>loading</div> }));
const seqVizMock = vi.hoisted(() => vi.fn());
vi.mock('seqviz', () => ({
  SeqViz: (props: Record<string, unknown>) => {
    seqVizMock(props);
    return <div data-testid='seqviz'>{String(props.name)}</div>;
  },
}));
import { renderWithI18n } from '../i18nTestUtils';
import SequenceViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceSequenceViewer';
import * as sequenceModel from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';

const LIMIT = 10 * 1024 * 1024;
const SOURCE_BYTES = 12 * 1024 * 1024;
const CHUNK_BYTES = 64 * 1024;
const NativeResponse = globalThis.Response;
type Stats = { readBytes: number; cancels: number; done: boolean };

function countedBody(total: number, chunkBytes = CHUNK_BYTES, byte = 65) {
  const stats: Stats = { readBytes: 0, cancels: 0, done: false };
  const chunk = new Uint8Array(chunkBytes).fill(byte);
  const body = new NativeReadableStream<Uint8Array>(
    {
      pull(controller) {
        if (stats.readBytes === total) {
          stats.done = true;
          controller.close();
          return;
        }
        const size = Math.min(chunk.byteLength, total - stats.readBytes);
        stats.readBytes += size;
        controller.enqueue(chunk.subarray(0, size));
      },
      cancel() {
        stats.cancels++;
      },
    },
    { highWaterMark: 0 }
  );
  return { body, stats };
}

function countedBytes(data: Uint8Array, chunkBytes = CHUNK_BYTES) {
  const stats: Stats = { readBytes: 0, cancels: 0, done: false };
  const body = new NativeReadableStream<Uint8Array>(
    {
      pull(controller) {
        if (stats.readBytes === data.byteLength) {
          stats.done = true;
          controller.close();
          return;
        }
        const end = Math.min(stats.readBytes + chunkBytes, data.byteLength);
        controller.enqueue(data.subarray(stats.readBytes, end));
        stats.readBytes = end;
      },
      cancel() {
        stats.cancels++;
      },
    },
    { highWaterMark: 0 }
  );
  return { body, stats };
}

function countNativeGzipOutput() {
  const stats: Stats = { readBytes: 0, cancels: 0, done: false };
  const streams: { readable: ReadableStream<Uint8Array>; writable: WritableStream<Uint8Array> }[] = [];
  class CountedNativeGzip {
    writable: WritableStream<Uint8Array>;
    readable: ReadableStream<Uint8Array>;
    constructor(format: CompressionFormat) {
      const native = new NativeDecompressionStream(format);
      const reader = native.readable.getReader();
      this.writable = native.writable as unknown as WritableStream<Uint8Array>;
      this.readable = new NativeReadableStream<Uint8Array>(
        {
          async pull(controller) {
            const { done, value } = await reader.read().catch((reason) => {
              reader.releaseLock();
              throw reason;
            });
            if (done) {
              stats.done = true;
              reader.releaseLock();
              controller.close();
              return;
            }
            stats.readBytes += value.byteLength;
            controller.enqueue(value);
          },
          async cancel(reason) {
            stats.cancels++;
            try {
              await reader.cancel(reason);
            } finally {
              reader.releaseLock();
            }
          },
        },
        { highWaterMark: 0 }
      ) as unknown as ReadableStream<Uint8Array>;
      streams.push(this);
    }
  }
  vi.stubGlobal('DecompressionStream', CountedNativeGzip);
  return { stats, streams };
}

async function mountResponse(filename: string, response: Response) {
  vi.stubGlobal('Blob', NativeBlob);
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => response)
  );
  const view = await renderWithI18n(
    <SequenceViewer filename={filename} contentUrl='/api/artifacts/synthetic-sequence/content' />,
    'en-US'
  );
  await screen.findByText('sequence file exceeds the 10 MB preview limit.', {}, { timeout: 8000 });
  expect(screen.queryByTestId('seqviz')).toBeNull();
  return view;
}

beforeEach(() => {
  vi.stubGlobal('Blob', NativeBlob);
  seqVizMock.mockClear();
});

async function renderResponse(filename: string, response: Response) {
  const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => response);
  vi.stubGlobal('fetch', fetchMock);
  const view = await renderWithI18n(
    <SequenceViewer filename={filename} contentUrl='/api/artifacts/synthetic-sequence/content' />,
    'en-US'
  );
  return { view, fetchMock };
}

async function assertSequence(name: string, seq: string) {
  await waitFor(() => expect(seqVizMock).toHaveBeenCalledWith(expect.objectContaining({ name, seq })), {
    timeout: 8000,
  });
}

async function assertReleased(
  body?: NativeReadableStream<Uint8Array>,
  streams: { readable: ReadableStream<Uint8Array>; writable: WritableStream<Uint8Array> }[] = []
) {
  await waitFor(
    () => {
      if (body) expect(body.locked).toBe(false);
      for (const stream of streams) {
        expect(stream.readable.locked).toBe(false);
        expect(stream.writable.locked).toBe(false);
      }
    },
    { timeout: 8000 }
  );
}

function stalledBody(firstChunk: Uint8Array) {
  const stats = { readBytes: 0, cancels: 0, waiting: false };
  const body = new NativeReadableStream<Uint8Array>(
    {
      pull(controller) {
        if (!stats.readBytes) {
          stats.readBytes = firstChunk.byteLength;
          controller.enqueue(firstChunk);
          return;
        }
        stats.waiting = true;
        return new Promise<void>(() => {});
      },
      cancel() {
        stats.cancels++;
      },
    },
    { highWaterMark: 0 }
  );
  return { body, stats };
}

function snapGeneFixture() {
  const header = Buffer.alloc(19);
  header[0] = 9;
  header.writeUInt32BE(14, 1);
  header.write('SnapGene', 5, 'ascii');
  header.writeUInt16BE(1, 13);
  const dna = Buffer.alloc(10);
  dna.writeUInt32BE(5, 1);
  dna.write('ACGT', 6, 'ascii');
  return Buffer.concat([header, dna]);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('SequenceViewer enforces its existing 10 MiB budget during reading', () => {
  it('rejects oversized declared gzip content before buffering its body', async () => {
    const { body, stats } = countedBody(SOURCE_BYTES);
    await mountResponse(
      'reads.fastq.gz',
      new NativeResponse(body as unknown as BodyInit, {
        headers: { 'Content-Type': 'application/gzip', 'Content-Length': String(SOURCE_BYTES) },
      })
    );
    console.log('DECLARED_GZIP', JSON.stringify(stats));
    expect(stats.readBytes).toBe(0);
    expect(stats.cancels).toBe(1);
    expect(body.locked).toBe(false);
  });

  it('stops a missing-length plain body near the budget and cancels the rest', async () => {
    const { body, stats } = countedBody(SOURCE_BYTES);
    await mountResponse(
      'reads.fastq',
      new NativeResponse(body as unknown as BodyInit, { headers: { 'Content-Type': 'text/plain' } })
    );
    console.log('UNKNOWN_LENGTH', JSON.stringify(stats));
    expect.soft(stats.readBytes).toBeLessThanOrEqual(LIMIT + CHUNK_BYTES);
    expect.soft(stats.cancels).toBe(1);
    expect(stats.done).toBe(false);
    expect(body.locked).toBe(false);
  });

  it('stops real gzip expansion near the budget and cancels decoding', async () => {
    const compressed = gzipSync(Buffer.alloc(SOURCE_BYTES, 65));
    const { stats, streams } = countNativeGzipOutput();
    const raw = countedBytes(compressed);
    await mountResponse(
      'reads.fastq.gz',
      new NativeResponse(raw.body as unknown as BodyInit, {
        headers: { 'Content-Type': 'application/gzip', 'Content-Length': String(compressed.byteLength) },
      })
    );
    console.log(
      'GZIP_EXPANSION',
      JSON.stringify({ compressedBytes: compressed.byteLength, limit: LIMIT, rawBody: raw.stats, decodedOutput: stats })
    );
    expect.soft(stats.readBytes).toBeLessThanOrEqual(LIMIT + CHUNK_BYTES);
    expect.soft(stats.cancels).toBe(1);
    await assertReleased(raw.body, streams);
  });

  it('loads and parses an ordinary small gzip sequence through the same native decompressor', async () => {
    vi.stubGlobal('Blob', NativeBlob);
    const source = '>synthetic\nACGT\n';
    const compressed = gzipSync(Buffer.from(source));
    const { stats, streams } = countNativeGzipOutput();
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new NativeResponse(compressed, {
            headers: { 'Content-Type': 'application/gzip', 'Content-Length': String(compressed.byteLength) },
          })
      )
    );
    await renderWithI18n(
      <SequenceViewer filename='synthetic.fasta.gz' contentUrl='/api/artifacts/synthetic-sequence/content' />,
      'en-US'
    );
    await assertSequence('synthetic', 'ACGT');
    await assertReleased(undefined, streams);
    expect(stats).toEqual({ readBytes: Buffer.byteLength(source), cancels: 0, done: true });
  });

  it('keeps the existing early declared-length rejection for ordinary uncompressed content', async () => {
    const { body, stats } = countedBody(SOURCE_BYTES);
    await mountResponse(
      'reads.fastq',
      new NativeResponse(body as unknown as BodyInit, { headers: { 'Content-Length': String(SOURCE_BYTES) } })
    );
    expect(stats.readBytes).toBe(0);
    expect(stats.cancels).toBe(1);
    expect(body.locked).toBe(false);
  });

  it('bounds actual plain bytes even when Content-Length underreports them', async () => {
    const raw = countedBody(SOURCE_BYTES);
    await mountResponse(
      'reads.fastq',
      new NativeResponse(raw.body as unknown as BodyInit, {
        headers: { 'Content-Length': '16' },
      })
    );
    expect(raw.stats.readBytes).toBeLessThanOrEqual(LIMIT + CHUNK_BYTES);
    expect(raw.stats.cancels).toBe(1);
    expect(raw.stats.done).toBe(false);
    await assertReleased(raw.body);
  });

  it('preserves the UTF-8 text-byte budget when replacement characters expand invalid plain bytes', async () => {
    const raw = countedBody(4 * 1024 * 1024, CHUNK_BYTES, 0xff);
    await mountResponse('invalid.fasta', new NativeResponse(raw.body as unknown as BodyInit));
    expect(raw.stats.readBytes).toBeLessThanOrEqual(Math.floor(LIMIT / 3) + CHUNK_BYTES);
    expect(raw.stats.cancels).toBe(1);
    expect(raw.stats.done).toBe(false);
    await assertReleased(raw.body);
  });

  it('bounds raw gzip bytes independently before their valid native expansion exceeds the cap', async () => {
    const input = Buffer.alloc(SOURCE_BYTES);
    let seed = 0x12345678;
    for (let i = 0; i < input.byteLength; i++) {
      seed ^= seed << 13;
      seed ^= seed >>> 17;
      seed ^= seed << 5;
      input[i] = seed & 0xff;
    }
    const compressed = gzipSync(input);
    expect(compressed.byteLength).toBeGreaterThan(LIMIT);
    const raw = countedBytes(compressed);
    const { stats, streams } = countNativeGzipOutput();
    await mountResponse('synthetic.dna.gz', new NativeResponse(raw.body as unknown as BodyInit));
    console.log(
      'RAW_GZIP_BUDGET',
      JSON.stringify({ compressedBytes: compressed.byteLength, raw: raw.stats, decoded: stats })
    );
    expect(raw.stats.readBytes).toBeLessThanOrEqual(LIMIT + CHUNK_BYTES);
    expect(raw.stats.readBytes).toBeLessThan(compressed.byteLength);
    expect(raw.stats.cancels).toBe(1);
    expect(raw.stats.done).toBe(false);
    expect(stats.readBytes).toBeLessThanOrEqual(LIMIT);
    await assertReleased(raw.body, streams);
  });

  it('accepts already transport-decoded gzip without treating compressed Content-Length as decoded size', async () => {
    const source = '>synthetic\nACGT\n';
    const raw = countedBytes(Buffer.from(source));
    const { stats, streams } = countNativeGzipOutput();
    await renderResponse(
      'synthetic.fasta.gz',
      new NativeResponse(raw.body as unknown as BodyInit, {
        headers: { 'Content-Encoding': 'gzip', 'Content-Length': String(LIMIT + 1) },
      })
    );
    await assertSequence('synthetic', 'ACGT');
    expect(stats.readBytes).toBe(0);
    expect(streams).toHaveLength(0);
    expect(raw.stats).toEqual({ readBytes: Buffer.byteLength(source), cancels: 0, done: true });
    await assertReleased(raw.body);
  });

  it('bounds already transport-decoded gzip by actual delivered bytes', async () => {
    const raw = countedBody(SOURCE_BYTES);
    const { streams } = countNativeGzipOutput();
    await mountResponse(
      'reads.fastq.gz',
      new NativeResponse(raw.body as unknown as BodyInit, {
        headers: { 'Content-Encoding': 'gzip', 'Content-Length': '12253' },
      })
    );
    expect(streams).toHaveLength(0);
    expect(raw.stats.readBytes).toBeLessThanOrEqual(LIMIT + CHUNK_BYTES);
    expect(raw.stats.cancels).toBe(1);
    expect(raw.stats.done).toBe(false);
    await assertReleased(raw.body);
  });

  it.each([false, true])('reads the full legitimate FASTA exactly at the cap (gzip=%s)', async (gzip) => {
    const prefix = '>limit\n';
    const source = prefix + 'A'.repeat(LIMIT - prefix.length - 1) + 'C';
    const data = gzip ? gzipSync(Buffer.from(source)) : Buffer.from(source);
    const raw = countedBytes(data);
    const { stats, streams } = countNativeGzipOutput();
    await renderResponse(
      gzip ? 'limit.fasta.gz' : 'limit.fasta',
      new NativeResponse(raw.body as unknown as BodyInit, {
        headers: { 'Content-Length': String(data.byteLength) },
      })
    );
    await waitFor(() => expect(seqVizMock).toHaveBeenCalled(), { timeout: 8000 });
    const props = seqVizMock.mock.lastCall?.[0];
    expect(props?.name).toBe('limit');
    expect(props?.seq).toHaveLength(LIMIT - prefix.length);
    expect(props?.seq).toBe(source.slice(prefix.length));
    expect(raw.stats).toEqual({ readBytes: data.byteLength, cancels: 0, done: true });
    if (gzip) expect(stats).toEqual({ readBytes: LIMIT, cancels: 0, done: true });
    await assertReleased(raw.body, streams);
  });

  it('rejects a valid FASTA one byte over the cap rather than silently truncating it', async () => {
    const raw = countedBytes(Buffer.from('>limit\n' + 'A'.repeat(LIMIT - 6)));
    await mountResponse('limit.fasta', new NativeResponse(raw.body as unknown as BodyInit));
    expect(raw.stats.readBytes).toBe(LIMIT + 1);
    expect(raw.stats.cancels).toBe(1);
    expect(raw.stats.done).toBe(false);
    await assertReleased(raw.body);
  });

  it.each([false, true])('preserves UTF-8 split across stream chunks (gzip=%s)', async (gzip) => {
    const source = '>synthétique\nACGT\n';
    const data = gzip ? gzipSync(Buffer.from(source)) : Buffer.from(source);
    const raw = countedBytes(data, 1);
    const { streams } = countNativeGzipOutput();
    await renderResponse(
      gzip ? 'synthetic.fasta.gz' : 'synthetic.fasta',
      new NativeResponse(raw.body as unknown as BodyInit)
    );
    await assertSequence('synthétique', 'ACGT');
    expect(raw.stats.cancels).toBe(0);
    await assertReleased(raw.body, streams);
  });

  it.each([false, true])('parses complete binary SnapGene input within the read budget (gzip=%s)', async (gzip) => {
    const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
    const data = gzip ? gzipSync(snapGeneFixture()) : snapGeneFixture();
    const raw = countedBytes(data, 1);
    const { streams } = countNativeGzipOutput();
    await renderResponse(
      gzip ? 'synthetic.dna.gz' : 'synthetic.dna',
      new NativeResponse(raw.body as unknown as BodyInit)
    );
    await assertSequence('synthetic', 'ACGT');
    expect(parse).toHaveBeenCalledTimes(1);
    const input = parse.mock.calls[0][0];
    expect(input).toBeInstanceOf(ArrayBuffer);
    expect(new Uint8Array(input as ArrayBuffer)).toEqual(Uint8Array.from(snapGeneFixture()));
    expect(screen.queryByText('Failed to parse sequence file.')).toBeNull();
    expect(raw.stats.cancels).toBe(0);
    await assertReleased(raw.body, streams);
  });

  it('reports malformed gzip truthfully and releases raw and decoded readers', async () => {
    const raw = countedBytes(Buffer.from('not a gzip sequence'));
    const { streams } = countNativeGzipOutput();
    await renderResponse('synthetic.fasta.gz', new NativeResponse(raw.body as unknown as BodyInit));
    await screen.findByText('Failed to parse sequence file.', {}, { timeout: 8000 });
    expect(seqVizMock).not.toHaveBeenCalled();
    await assertReleased(raw.body, streams);
  });

  it('cancels untouched gzip input when native decoding is unavailable', async () => {
    vi.stubGlobal('DecompressionStream', undefined);
    const raw = countedBytes(gzipSync(Buffer.from('>synthetic\nACGT\n')));
    await renderResponse('synthetic.fasta.gz', new NativeResponse(raw.body as unknown as BodyInit));
    await screen.findByText('Failed to initialize the sequence file viewer.', {}, { timeout: 8000 });
    expect(raw.stats.readBytes).toBe(0);
    expect(raw.stats.cancels).toBe(1);
    await assertReleased(raw.body);
  });

  it('rejects invalid gzip UTF-8 instead of altering the scientific source', async () => {
    const raw = countedBytes(gzipSync(Buffer.from([0xff, 0xff])));
    const { streams } = countNativeGzipOutput();
    await renderResponse('synthetic.fasta.gz', new NativeResponse(raw.body as unknown as BodyInit));
    await screen.findByText('Failed to parse sequence file.', {}, { timeout: 8000 });
    expect(seqVizMock).not.toHaveBeenCalled();
    await assertReleased(raw.body, streams);
  });

  it('aborts fetch and cancels a pending plaintext read when replaced, then renders the new sequence', async () => {
    const raw = stalledBody(Buffer.from('>stale\nACGT'));
    const { view, fetchMock } = await renderResponse(
      'stale.fasta',
      new NativeResponse(raw.body as unknown as BodyInit)
    );
    await waitFor(() => expect(raw.stats.waiting).toBe(true), { timeout: 8000 });
    const signal = fetchMock.mock.calls[0][1]?.signal as AbortSignal;
    view.rerender(<SequenceViewer filename='fresh.fasta' content={'>fresh\nTGCA\n'} />);
    expect(signal.aborted).toBe(true);
    await assertSequence('fresh', 'TGCA');
    expect(raw.stats.cancels).toBe(1);
    await assertReleased(raw.body);
    expect(seqVizMock.mock.calls.every(([props]) => props.name === 'fresh')).toBe(true);
  });

  it('aborts fetch and cancels pending native gzip decoding on unmount', async () => {
    const compressed = gzipSync(Buffer.from('>pending\n' + 'A'.repeat(CHUNK_BYTES)));
    const raw = stalledBody(compressed.subarray(0, compressed.byteLength - 8));
    const { stats, streams } = countNativeGzipOutput();
    const { view, fetchMock } = await renderResponse(
      'pending.fasta.gz',
      new NativeResponse(raw.body as unknown as BodyInit)
    );
    await waitFor(
      () => {
        expect(raw.stats.waiting).toBe(true);
        expect(stats.readBytes).toBeGreaterThan(0);
      },
      { timeout: 8000 }
    );
    const signal = fetchMock.mock.calls[0][1]?.signal as AbortSignal;
    view.unmount();
    expect(signal.aborted).toBe(true);
    await waitFor(
      () => {
        expect(raw.stats.cancels).toBe(1);
        expect(stats.cancels).toBe(1);
      },
      { timeout: 8000 }
    );
    await assertReleased(raw.body, streams);
    expect(seqVizMock).not.toHaveBeenCalled();
  });
});
