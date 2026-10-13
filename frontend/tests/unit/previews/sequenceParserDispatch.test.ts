import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import seqparse from 'seqparse';
import { describe, expect, it } from 'vitest';
import { parseSequenceDocument } from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';

const fixture = (filename: string): string =>
  readFileSync(resolve('tests/fixtures/sequence-joined-location', filename), 'utf8');
const jbei = fixture('dispatch-comment.jbei');
const joined = fixture('joined.gbk');

async function expectNativeDispatch(source: string, filename: string): Promise<void> {
  const native = await seqparse(source, { fileName: filename });
  const document = await parseSequenceDocument(source, filename);
  expect(document).toMatchObject({
    name: native.name,
    seq: native.seq,
    type: native.type,
    annotations: native.annotations,
  });
}

describe('compound-location guard preserves locked parser dispatch priority', () => {
  it.each(['control.jbei', 'control.gbk', 'control.fasta', 'control.txt'])(
    'preserves JBEI root/namespace priority over embedded GenBank comment text: %s',
    async (filename) => {
      await expectNativeDispatch(jbei, filename);
      expect(await parseSequenceDocument(jbei, filename)).toMatchObject({
        name: 'JBEI control with XML comment',
        seq: 'ATGCCCTAA',
        annotations: [],
      });
    }
  );

  it('preserves JBEI namespace-prefix detection after an XML declaration', async () => {
    await expectNativeDispatch(`<?xml version="1.0"?>\n${jbei}`, 'declared.jbei');
  });

  it.each(['priority.seq', 'priority.fa', 'priority.fas', 'priority.fasta'])(
    'preserves the exact native filename-based FASTA priority: %s',
    async (filename) => {
      // This is a dispatch conformance probe, not a claim that GenBank text is
      // biologically valid FASTA. The adapter must preserve the native branch.
      await expectNativeDispatch(joined, filename);
    }
  );

  it.each(['>LOCUS ORIGIN join(4..6,19..24)\nATGCCCTAA', ';LOCUS ORIGIN join(4..6,19..24)\nATGCCCTAA'])(
    'preserves native FASTA prefix priority for header descriptions',
    async (source) => {
      await expectNativeDispatch(source, 'prefix.gbk');
    }
  );

  it.each([
    'sniff.txt',
    'sniff.jbei',
    'sniff.sbol',
    'sniff.fna',
    'sniff.faa',
    'sniff.FASTA',
    'sniff.FASTA.GZ',
    'sniff.GBK',
    'sniff.ape',
  ])('still rejects genuine GenBank content when native GenBank dispatch wins: %s', async (filename) => {
    const native = await seqparse(joined, { fileName: filename });
    expect(native.annotations).toHaveLength(1);
    expect(native.annotations[0]).toMatchObject({ start: 3, end: 6 });
    await expect(parseSequenceDocument(joined, filename)).rejects.toMatchObject({
      code: 'unsupported-sequence-location',
    });
  });

  it('preserves the JBEI dispatch priority for actual ArrayBuffer input', async () => {
    const bytes = new TextEncoder().encode(jbei).buffer;
    const before = new Uint8Array(bytes).slice();
    await expect(parseSequenceDocument(bytes, 'buffer.gbk')).resolves.toMatchObject({
      seq: 'ATGCCCTAA',
      annotations: [],
    });
    expect(new Uint8Array(bytes)).toEqual(before);
  });
});
