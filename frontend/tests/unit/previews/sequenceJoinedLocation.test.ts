import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { parseSequenceDocument } from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';
import {
  ScientificPreviewError,
  scientificPreviewErrorKey,
} from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewError';

const fixture = (name: string): string =>
  readFileSync(resolve('tests/fixtures/sequence-joined-location', `${name}.gbk`), 'utf8');
const contiguous = fixture('contiguous');
const withLocation = (location: string): string =>
  contiguous.replace('CDS             4..12', `CDS             ${location}`);

// These locations are valid compound features. Rejecting them is deliberate:
// the locked parser/viewer contract cannot preserve compound selection semantics.
describe('GenBank compound feature-location accuracy boundary', () => {
  it.each(['joined', 'reverse', 'origin'])(
    'rejects the complete %s fixture rather than returning a truncated feature',
    async (name) => {
      await expect(parseSequenceDocument(fixture(name), `${name}.gbk`)).rejects.toMatchObject({
        code: 'unsupported-sequence-location',
      });
    }
  );

  it.each(['sample.gb', 'sample.gbk', 'sample.genbank', 'sample.gbk.gz', 'sample.GENBANK.GZ', 'sample.txt'])(
    'rejects joined locations on the real text parser route for %s',
    async (filename) => {
      await expect(parseSequenceDocument(fixture('joined'), filename)).rejects.toMatchObject({
        code: 'unsupported-sequence-location',
      });
    }
  );

  it('rejects the complete binary-buffer text input without changing the source bytes', async () => {
    const input = new TextEncoder().encode(fixture('joined')).buffer;
    const before = new Uint8Array(input).slice();
    await expect(parseSequenceDocument(input, 'joined.gbk')).rejects.toMatchObject({
      code: 'unsupported-sequence-location',
    });
    expect(new Uint8Array(input)).toEqual(before);
  });

  it.each([
    'join(4..6,\n                     19..24)',
    'complement(\n                     join(4..6,19..24))',
    'join\n                     (4..6,19..24)',
    'order(4..6,19..24)',
    'complement(order(4..6,19..24))',
    'join(<4..6,19..>24)',
    'join(4..6,19..24,28..30)',
  ])('rejects wrapped, ordered, and fuzzy compound locations: %s', async (location) => {
    await expect(parseSequenceDocument(withLocation(location), 'wrapped.gbk')).rejects.toMatchObject({
      code: 'unsupported-sequence-location',
    });
  });

  it('rejects CRLF feature tables and a compound location after another feature', async () => {
    const input = contiguous
      .replace('ORIGIN', '     misc_feature    join(4..6,19..24)\n                     /label="later"\nORIGIN')
      .replace(/\n/g, '\r\n');
    await expect(parseSequenceDocument(input, 'later.gbk')).rejects.toMatchObject({
      code: 'unsupported-sequence-location',
    });
  });

  it.each(['4..12', 'complement(4..12)', '<4..>12', '4', '36'])(
    'preserves the native simple location %s',
    async (location) => {
      const parsed = await parseSequenceDocument(withLocation(location), 'simple.gbk');
      expect(parsed.seq).toBe('aaaatgccctaagggtttaaacccgggtttaaaccc');
      expect(parsed.annotations).toHaveLength(1);
      expect(parsed.annotations[0].direction).toBe(location.startsWith('complement') ? -1 : 1);
    }
  );

  it('does not mistake join/order text in headers, comments, names, or wrapped qualifiers for a compound location', async () => {
    const input = contiguous
      .replace(
        'DEFINITION  Benign independent synthetic CDS fixture.',
        'DEFINITION  Description of join(4..6,19..24).\nCOMMENT     order(4..6,19..24) is plain explanatory text.'
      )
      .replace(
        '/gene="contiguous_CDS"',
        '/gene="join(4..6,19..24)"\n                     /note="Example locations include\n                     join(4..6,19..24) and order(4..6,19..24)."'
      );
    const parsed = await parseSequenceDocument(input, 'commented.gbk');
    expect(parsed.annotations).toHaveLength(1);
    expect(parsed.annotations[0]).toMatchObject({ start: 3, end: 12, direction: 1 });
  });

  it('does not mistake compound-looking FASTA header text for GenBank locations', async () => {
    const parsed = await parseSequenceDocument('>join(4..6,19..24)\nATGCCCTAA', 'plain.fasta');
    expect(parsed.seq).toBe('ATGCCCTAA');
    expect(parsed.annotations).toEqual([]);
  });

  it('maps the accuracy rejection to its actionable preview error', () => {
    expect(scientificPreviewErrorKey(new ScientificPreviewError('unsupported-sequence-location'))).toBe(
      'preview.scientific.errors.unsupportedSequenceLocation'
    );
  });
});
