import * as seqparseApi from 'seqparse';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { getContentTypeByExtension } from '@/renderer/pages/conversation/Preview/fileUtils';
import {
  parseSequenceDocument,
  resolveSequenceFormat,
} from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';

// A synthesized two-packet native file containing only arbitrary ACGT.
// Header: type 9, 14-byte payload, SnapGene cookie, DNA type 1, zero versions.
// DNA: type 0, 5-byte payload, topology flag followed by four ASCII bases.
function nativeFixture(topology = 0): ArrayBuffer {
  const bytes = Uint8Array.from(Buffer.from('090000000e536e617047656e6500010000000000000000050041434754', 'hex'));
  bytes[24] = topology;
  return bytes.buffer;
}

const markerNotes = {
  genbank: '<Notes><Comment>LOCUS and ORIGIN terminology in harmless note</Comment></Notes>',
  jbei: '<Notes xmlns:seq="http://jbei.org/sequence"><Comment>harmless marker</Comment></Notes>',
  fasta: '<Notes><Comment>\n>synthetic-fasta-marker\nTTTT\n</Comment></Notes>',
};
const features =
  '<Features><Feature name="LOCUS ORIGIN harmless feature" type="misc_feature" directionality="1"><Segment range="1-2"/></Feature><Feature name="neutral" type="misc_feature" directionality="2"><Segment range="3-4"/></Feature></Features>';
function withPacket(type: number, payload: string): ArrayBuffer {
  const bytes = Buffer.from(payload);
  const header = Buffer.alloc(5);
  header[0] = type;
  header.writeUInt32BE(bytes.byteLength, 1);
  return Uint8Array.from(Buffer.concat([Buffer.from(nativeFixture()), header, bytes])).buffer;
}

const genbank = `LOCUS       SYNTHETIC                  4 bp    DNA     linear SYN 12-OCT-2026
DEFINITION  Harmless arbitrary synthetic four-base sequence.
ACCESSION   SYNTHETIC
FEATURES             Location/Qualifiers
     source          1..4
ORIGIN
        1 acgt
//`;

afterEach(() => vi.restoreAllMocks());

describe('native SnapGene public parser convention', () => {
  it.each([
    'synthetic-four-bases.dna',
    'synthetic-four-bases.dna.gz',
    'synthetic-four-bases.DNA',
    'synthetic-four-bases.DNA.GZ',
  ])('advertises %s as a sequence/SnapGene format', (filename) => {
    expect(getContentTypeByExtension(filename)).toBe('sequence');
    expect(resolveSequenceFormat(filename)).toBe('snapgene');
  });

  it.each([
    ['synthetic-four-bases.dna', 0],
    ['synthetic-four-bases.dna', 1],
    ['synthetic-four-bases.dna.gz', 0],
    ['synthetic-four-bases.dna.gz', 1],
  ] as const)('parses exact native bases for %s with topology flag %s', async (filename, topology) => {
    const source = nativeFixture(topology);
    const original = new Uint8Array(source).slice();
    const decode = vi.spyOn(TextDecoder.prototype, 'decode');
    const parsed = await parseSequenceDocument(source, filename);
    expect(parsed).toMatchObject({
      name: 'synthetic-four-bases',
      seq: 'ACGT',
      type: 'dna',
      format: 'snapgene',
      annotations: [],
    });
    expect(decode).toHaveBeenCalledOnce();
    expect((decode.mock.calls[0][0] as Uint8Array).buffer).toBe(source);
    expect((decode.mock.calls[0][0] as Uint8Array).byteLength).toBe(19);
    const text = decode.mock.results[0].value;
    expect(text).toBe(Buffer.from(source, 0, 19).toString('utf8'));
    expect(text).not.toBe('');
    expect(new Uint8Array(source)).toEqual(original);
    expect(new Uint8Array(source)[24]).toBe(topology);
    // The locked parser discards topology; this checks preservation of its
    // native input flag, not topology support in its output or the viewer.
  });

  it.each(['SynthETIC.DNA', 'SynthETIC.DNA.GZ'])('preserves name casing when dispatching %s', async (filename) => {
    const parsed = await parseSequenceDocument(nativeFixture(), filename);
    expect(parsed).toMatchObject({ name: 'SynthETIC', seq: 'ACGT', format: 'snapgene' });
  });

  it.each([
    ['genbank', 'synthetic.dna'],
    ['genbank', 'synthetic.dna.gz'],
    ['jbei', 'synthetic.dna'],
    ['jbei', 'synthetic.dna.gz'],
    ['fasta', 'synthetic.dna'],
    ['fasta', 'synthetic.dna.gz'],
  ] as const)('keeps native dispatch for %s marker Notes in %s', async (marker, filename) => {
    const source = withPacket(6, markerNotes[marker]);
    const original = new Uint8Array(source).slice();
    const parsed = await parseSequenceDocument(source, filename);
    expect(parsed).toMatchObject({ name: 'synthetic', seq: 'ACGT', type: 'dna', format: 'snapgene', annotations: [] });
    expect(new Uint8Array(source)).toEqual(original);
  });

  it.each(['synthetic.dna', 'synthetic.dna.gz'])(
    'parses all native features beyond the dispatch header for %s',
    async (filename) => {
      const source = withPacket(10, features);
      const original = new Uint8Array(source).slice();
      const parsed = await parseSequenceDocument(source, filename);
      expect(parsed.seq).toBe('ACGT');
      expect(parsed.annotations).toEqual([
        {
          color: undefined,
          direction: 1,
          start: 0,
          end: 1,
          name: 'LOCUS ORIGIN harmless feature',
          type: 'misc_feature',
        },
        { color: undefined, direction: -1, start: 2, end: 3, name: 'neutral', type: 'misc_feature' },
      ]);
      expect(new Uint8Array(source)).toEqual(original);
    }
  );

  it('keeps binary bytes authoritative when the public API receives file text', async () => {
    const source = nativeFixture();
    const text = Buffer.from(source).toString('utf8');
    new Uint8Array(source).set(Buffer.from('TTTT'), 25);
    const parsed = await seqparseApi.default(text, { fileName: 'synthetic.dna', source });
    expect(parsed.seq).toBe('TTTT');
  });

  it('decodes a non-UTF-8 auxiliary packet without changing its binary source', async () => {
    const source = Uint8Array.from([...new Uint8Array(nativeFixture()), 2, 0, 0, 0, 1, 255]).buffer;
    const original = new Uint8Array(source).slice();
    const decode = vi.spyOn(TextDecoder.prototype, 'decode');
    const parsed = await parseSequenceDocument(source, 'synthetic.dna');
    expect(parsed.seq).toBe('ACGT');
    expect(decode).toHaveBeenCalledOnce();
    expect((decode.mock.calls[0][0] as Uint8Array).buffer).toBe(source);
    expect((decode.mock.calls[0][0] as Uint8Array).byteLength).toBe(19);
    expect(decode.mock.results[0].value).toBe(Buffer.from(source, 0, 19).toString('utf8'));
    expect(new Uint8Array(source)).toEqual(original);
  });

  it.each([
    'empty',
    'bad-cookie',
    'truncated-header',
    'header-only',
    'wrong-packet-type',
    'wrong-header-length',
    'fasta-text',
    'genbank-text',
    'jbei-text',
  ])('rejects %s native input instead of presenting a synthetic successful sequence', async (kind) => {
    let source = nativeFixture();
    if (kind === 'empty') source = new ArrayBuffer(0);
    if (kind === 'bad-cookie') new Uint8Array(source)[5] = 88;
    if (kind === 'truncated-header') source = source.slice(0, 10);
    if (kind === 'header-only') source = source.slice(0, 19);
    if (kind === 'wrong-packet-type') new Uint8Array(source)[0] = 62;
    if (kind === 'wrong-header-length') new Uint8Array(source)[4] = 13;
    if (kind === 'fasta-text') source = Uint8Array.from(Buffer.from('>synthetic\nACGT\n')).buffer;
    if (kind === 'genbank-text') source = Uint8Array.from(Buffer.from(genbank)).buffer;
    if (kind === 'jbei-text')
      source = Uint8Array.from(Buffer.from('<seq:seq xmlns:seq="http://jbei.org/sequence"/>')).buffer;
    await expect(parseSequenceDocument(source, 'synthetic.dna')).rejects.toThrow();
  });

  it.each([false, true])('preserves real FASTA parsing (binary=%s)', async (binary) => {
    const source = '>synthetic-four-bases\nACGT\n';
    const parsed = await parseSequenceDocument(
      binary ? Uint8Array.from(Buffer.from(source)).buffer : source,
      'synthetic-four-bases.fasta'
    );
    expect(parsed).toMatchObject({ name: 'synthetic-four-bases', seq: 'ACGT', type: 'dna', format: 'fasta' });
  });

  it.each([false, true])('preserves real GenBank parsing (binary=%s)', async (binary) => {
    const parsed = await parseSequenceDocument(
      binary ? Uint8Array.from(Buffer.from(genbank)).buffer : genbank,
      'synthetic-four-bases.gbk'
    );
    expect(parsed).toMatchObject({ name: 'SYNTHETIC', seq: 'acgt', type: 'dna', format: 'genbank' });
  });

  it.each([false, true])('preserves real FASTQ parsing (binary=%s)', async (binary) => {
    const source = '@synthetic-four-bases\nACGT\n+\nIIII\n';
    const parsed = await parseSequenceDocument(
      binary ? Uint8Array.from(Buffer.from(source)).buffer : source,
      'synthetic-four-bases.fastq'
    );
    expect(parsed).toMatchObject({
      name: 'synthetic-four-bases',
      seq: 'ACGT',
      type: 'dna',
      format: 'fastq',
      recordCount: 1,
      qualityStats: { min: 40, max: 40, mean: 40 },
    });
  });
});
