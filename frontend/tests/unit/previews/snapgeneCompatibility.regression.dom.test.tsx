import React from 'react';
import {
  DecompressionStream as NativeDecompressionStream,
  ReadableStream as NativeReadableStream,
} from 'node:stream/web';
import { gzipSync } from 'node:zlib';
import { cleanup, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import SequenceViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceSequenceViewer';
import * as sequenceModel from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';

vi.mock('@arco-design/web-react', () => ({ Spin: () => <div>loading</div> }));
const seqVizMock = vi.hoisted(() => vi.fn());
vi.mock('seqviz', () => ({
  SeqViz: (props: Record<string, unknown>) => {
    seqVizMock(props);
    return <div data-testid='seqviz'>{String(props.name)}</div>;
  },
}));

const NativeResponse = globalThis.Response;
const LIMIT = 10 * 1024 * 1024;
const CHUNK_BYTES = 64 * 1024;

// Minimal complete native SnapGene packets with arbitrary, non-biological bases.
function nativeFixture(topology = 0) {
  const source = Buffer.from('090000000e536e617047656e6500010000000000000000050041434754', 'hex');
  source[24] = topology;
  return source;
}

const markerNotes = {
  genbank: '<Notes><Comment>LOCUS and ORIGIN terminology in harmless note</Comment></Notes>',
  jbei: '<Notes xmlns:seq="http://jbei.org/sequence"><Comment>harmless marker</Comment></Notes>',
  fasta: '<Notes><Comment>\n>synthetic-fasta-marker\nTTTT\n</Comment></Notes>',
};
const features =
  '<Features><Feature name="LOCUS ORIGIN harmless feature" type="misc_feature" directionality="1"><Segment range="1-2"/></Feature><Feature name="neutral" type="misc_feature" directionality="2"><Segment range="3-4"/></Feature></Features>';
function withPacket(type: number, payload: string) {
  const bytes = Buffer.from(payload);
  const header = Buffer.alloc(5);
  header[0] = type;
  header.writeUInt32BE(bytes.byteLength, 1);
  return Buffer.concat([nativeFixture(), header, bytes]);
}

function countedBytes(source: Uint8Array) {
  const stats = { readBytes: 0, cancels: 0, done: false };
  const body = new NativeReadableStream<Uint8Array>(
    {
      pull(controller) {
        if (stats.readBytes === source.byteLength) {
          stats.done = true;
          controller.close();
          return;
        }
        const end = Math.min(stats.readBytes + CHUNK_BYTES, source.byteLength);
        controller.enqueue(source.subarray(stats.readBytes, end));
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

async function mount(filename: string, source: Uint8Array) {
  const raw = countedBytes(source);
  const response = new NativeResponse(raw.body as unknown as BodyInit);
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => response)
  );
  await renderWithI18n(<SequenceViewer filename={filename} contentUrl='/test/synthetic/content' />, 'en-US');
  return { ...raw, response };
}

beforeEach(() => {
  seqVizMock.mockClear();
  vi.stubGlobal('DecompressionStream', NativeDecompressionStream);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('SnapGene preview through actual model and bounded native reads', () => {
  it.each([
    [false, 0],
    [false, 1],
    [true, 0],
    [true, 1],
  ] as const)('visualizes complete native ACGT (gzip=%s, topology flag=%s)', async (gzip, topology) => {
    const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
    const fixture = nativeFixture(topology);
    const filename = gzip ? 'synthetic.dna.gz' : 'synthetic.dna';
    const raw = await mount(filename, gzip ? gzipSync(fixture) : fixture);
    await waitFor(() =>
      expect(seqVizMock).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'synthetic', seq: 'ACGT', seqType: 'dna' })
      )
    );
    expect(parse).toHaveBeenCalledOnce();
    const [source, receivedFilename] = parse.mock.calls[0];
    expect(receivedFilename).toBe(filename);
    expect(source).toBeInstanceOf(ArrayBuffer);
    expect(new Uint8Array(source as ArrayBuffer)).toEqual(Uint8Array.from(fixture));
    expect(new Uint8Array(source as ArrayBuffer)[24]).toBe(topology);
    expect(screen.queryByText('Failed to parse sequence file.')).toBeNull();
    expect(await screen.findByText('4 bp · 0 annotations')).toBeInTheDocument();
    expect(raw.stats).toMatchObject({ cancels: 0, done: true });
    await waitFor(() => expect(raw.body.locked).toBe(false));
  });

  it.each([false, true])('loads uppercase native extensions without changing name casing (gzip=%s)', async (gzip) => {
    const fixture = nativeFixture();
    const raw = await mount(gzip ? 'SynthETIC.DNA.GZ' : 'SynthETIC.DNA', gzip ? gzipSync(fixture) : fixture);
    await waitFor(() =>
      expect(seqVizMock).toHaveBeenCalledWith(expect.objectContaining({ name: 'SynthETIC', seq: 'ACGT' }))
    );
    await waitFor(() => expect(raw.body.locked).toBe(false));
  });

  it.each([
    [false, 'genbank'],
    [true, 'genbank'],
    [false, 'jbei'],
    [true, 'jbei'],
    [false, 'fasta'],
    [true, 'fasta'],
  ] as const)('preserves native DNA despite %s gzip / %s Notes markers', async (gzip, marker) => {
    const fixture = withPacket(6, markerNotes[marker]);
    const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
    const raw = await mount(gzip ? 'synthetic.dna.gz' : 'synthetic.dna', gzip ? gzipSync(fixture) : fixture);
    await waitFor(() =>
      expect(seqVizMock).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'synthetic', seq: 'ACGT', seqType: 'dna' })
      )
    );
    expect(new Uint8Array(parse.mock.calls[0][0] as ArrayBuffer)).toEqual(Uint8Array.from(fixture));
    expect(screen.queryByText('Failed to parse sequence file.')).toBeNull();
    expect(raw.stats).toMatchObject({ cancels: 0, done: true });
    await waitFor(() => expect(raw.body.locked).toBe(false));
  });

  it.each([false, true])(
    'visualizes complete real native annotations beyond the dispatch header (gzip=%s)',
    async (gzip) => {
      const fixture = withPacket(10, features);
      const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
      const raw = await mount(gzip ? 'synthetic.dna.gz' : 'synthetic.dna', gzip ? gzipSync(fixture) : fixture);
      await waitFor(() =>
        expect(seqVizMock).toHaveBeenCalledWith(
          expect.objectContaining({
            seq: 'ACGT',
            annotations: [
              expect.objectContaining({ name: 'LOCUS ORIGIN harmless feature', direction: 1, start: 0, end: 1 }),
              expect.objectContaining({ name: 'neutral', direction: -1, start: 2, end: 3 }),
            ],
          })
        )
      );
      expect(new Uint8Array(parse.mock.calls[0][0] as ArrayBuffer)).toEqual(Uint8Array.from(fixture));
      expect(await screen.findByText('4 bp · 2 annotations')).toBeInTheDocument();
      await waitFor(() => expect(raw.body.locked).toBe(false));
    }
  );

  it.each([false, true])('preserves non-UTF-8 auxiliary native bytes (gzip=%s)', async (gzip) => {
    const fixture = Buffer.concat([nativeFixture(), Buffer.from([2, 0, 0, 0, 1, 255])]);
    const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
    const raw = await mount(gzip ? 'synthetic.dna.gz' : 'synthetic.dna', gzip ? gzipSync(fixture) : fixture);
    await waitFor(() => expect(seqVizMock).toHaveBeenCalledWith(expect.objectContaining({ seq: 'ACGT' })));
    expect(new Uint8Array(parse.mock.calls[0][0] as ArrayBuffer)).toEqual(Uint8Array.from(fixture));
    await waitFor(() => expect(raw.body.locked).toBe(false));
  });

  it.each([
    [false, 'empty'],
    [true, 'empty'],
    [false, 'bad-cookie'],
    [true, 'bad-cookie'],
    [false, 'truncated-header'],
    [true, 'truncated-header'],
    [false, 'header-only'],
    [true, 'header-only'],
    [false, 'wrong-packet-type'],
    [true, 'wrong-packet-type'],
    [false, 'wrong-header-length'],
    [true, 'wrong-header-length'],
    [false, 'fasta-text'],
    [true, 'fasta-text'],
    [false, 'genbank-text'],
    [true, 'genbank-text'],
    [false, 'jbei-text'],
    [true, 'jbei-text'],
  ] as const)('rejects %s gzip / %s native input without visualization', async (gzip, kind) => {
    let source = nativeFixture();
    if (kind === 'empty') source = Buffer.alloc(0);
    if (kind === 'bad-cookie') source[5] = 88;
    if (kind === 'truncated-header') source = source.subarray(0, 10);
    if (kind === 'header-only') source = source.subarray(0, 19);
    if (kind === 'wrong-packet-type') source[0] = 62;
    if (kind === 'wrong-header-length') source[4] = 13;
    if (kind === 'fasta-text') source = Buffer.from('>synthetic\nACGT\n');
    if (kind === 'genbank-text') source = Buffer.from('LOCUS       SYNTHETIC\nORIGIN\n        1 acgt\n//');
    if (kind === 'jbei-text') source = Buffer.from('<seq:seq xmlns:seq="http://jbei.org/sequence"/>');
    const raw = await mount(gzip ? 'synthetic.dna.gz' : 'synthetic.dna', gzip ? gzipSync(source) : source);
    await screen.findByText(/(?:Failed to parse sequence file\.|sequence file is empty\.)/);
    expect(seqVizMock).not.toHaveBeenCalled();
    await waitFor(() => expect(raw.body.locked).toBe(false));
  });

  it.each([false, true])('rejects oversized complete native DNA before model text decoding (gzip=%s)', async (gzip) => {
    const parse = vi.spyOn(sequenceModel, 'parseSequenceDocument');
    const fixture = Buffer.concat([nativeFixture().subarray(0, 25), Buffer.alloc(LIMIT, 65)]);
    fixture.writeUInt32BE(LIMIT + 1, 20);
    const raw = await mount(gzip ? 'oversized.dna.gz' : 'oversized.dna', gzip ? gzipSync(fixture) : fixture);
    await screen.findByText('sequence file exceeds the 10 MB preview limit.', {}, { timeout: 8000 });
    expect(parse).not.toHaveBeenCalled();
    expect(seqVizMock).not.toHaveBeenCalled();
    if (!gzip) {
      expect(raw.stats.readBytes).toBeLessThanOrEqual(LIMIT + CHUNK_BYTES);
      expect(raw.stats.cancels).toBe(1);
      expect(raw.stats.done).toBe(false);
    }
    await waitFor(() => expect(raw.body.locked).toBe(false));
  });
});
