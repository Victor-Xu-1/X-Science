import { parseSequenceDocument } from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';
import seqparse from 'seqparse';
import { describe, expect, it } from 'vitest';

describe('sequence molecule presentation model', () => {
  it('preserves a protein FASTA first-record identifier and native amino-acid type', async () => {
    const document = await parseSequenceDocument(
      '>db|SYNTHETIC001|Example protein description\nMPEPTIDEQF\n>other|SYNTHETIC002|Second record\nACGT',
      'sequence.fasta'
    );

    expect(document).toMatchObject({
      name: 'db|SYNTHETIC001|Example protein description',
      seq: 'MPEPTIDEQF',
      type: 'aa',
      annotations: [],
      format: 'fasta',
    });
  });

  it.each([
    ['ACGTN', 'dna'],
    ['ACGUN', 'rna'],
    ['ACGTRYSWKMBDHVN', 'unknown'],
  ] as const)(
    'preserves native %s molecule classification without guessing from its filename',
    async (sequence, type) => {
      const document = await parseSequenceDocument(`>control\n${sequence}`, 'ambiguous.fasta');

      expect(document.seq).toBe(sequence);
      expect(document.type).toBe(type);
    }
  );

  it('preserves the complete FASTA header and original protein bytes from ArrayBuffer input', async () => {
    const source = new TextEncoder().encode('>db|SYNTHETIC003|Example/metadata\nMPEPTIDEQF').buffer;
    const before = new Uint8Array(source).slice();
    const document = await parseSequenceDocument(source, 'buffer.fasta');

    expect(document.name).toBe('db|SYNTHETIC003|Example/metadata');
    expect(document.type).toBe('aa');
    expect(document.seq).toBe('MPEPTIDEQF');
    expect(new Uint8Array(source)).toEqual(before);
  });

  it.each([
    ['>empty|old header\n>actual|protein header\nMPEPTIDEQF', 'actual'],
    ['>|header-without-native-prefix\nMPEPTIDEQF\n>actual|dna header\nACGT', 'actual'],
    ['>same|first header\nMPEPTIDEQF\n>same|later header\nMPEPTIDEQF', 'same|first header'],
  ])(
    'keeps the native selected identity and only recovers a provable first-record header: %s',
    async (source, name) => {
      const native = await seqparse(source, { fileName: 'records.fasta' });
      const document = await parseSequenceDocument(source, 'records.fasta');

      expect(document.name).toBe(name);
      expect(document.seq).toBe(native.seq);
      expect(document.type).toBe(native.type);
      expect(document.annotations).toEqual(native.annotations);
    }
  );

  it.each(['record.txt', 'record.fasta'])(
    'keeps raw-prefix FASTA sniffing for complete identifiers: %s',
    async (filename) => {
      const document = await parseSequenceDocument('>actual|protein header\nMPEPTIDEQF', filename);
      expect(document.name).toBe('actual|protein header');
      expect(document.type).toBe('aa');
    }
  );

  it('uses the native FASTA filename dispatch for leading whitespace rather than treating it as a raw prefix', async () => {
    const document = await parseSequenceDocument(' \n>actual|protein header\nMPEPTIDEQF', 'record.fasta');
    expect(document.name).toBe('actual|protein header');
    expect(document.type).toBe('aa');
  });

  it('retains the native selected name when a candidate has an ambiguous earlier dispatch', async () => {
    const source = `>empty|old\n${'\n'.repeat(210)}>actual|protein :seq="http://jbei.org/sequence"\nMPEPTIDEQF`;
    const native = await seqparse(source, { fileName: 'record.fasta' });
    const document = await parseSequenceDocument(source, 'record.fasta');
    expect(document).toMatchObject(native);
  });
});
