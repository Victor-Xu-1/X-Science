import seqparse from 'seqparse';
import { describe, expect, it } from 'vitest';
import { parseSequenceDocument } from '@/renderer/pages/conversation/Preview/components/viewers/sequenceModel';
import { fixtureBytes, packetFixtures } from './snapgenePacketFixtures';

describe('native SnapGene packet framing', () => {
  it.each(packetFixtures)('validates $id before presenting a sequence', async (fixture) => {
    const source = Uint8Array.from(fixtureBytes(fixture)).buffer;
    const original = new Uint8Array(source).slice();
    if (fixture.outcome === 'sequence') {
      await expect(parseSequenceDocument(source, 'SynthETIC.DNA.GZ')).resolves.toMatchObject({
        name: 'SynthETIC',
        seq: 'ACGTTGCA',
        type: 'dna',
        format: 'snapgene',
        annotations: [],
      });
    } else if (fixture.outcome === 'parser-error') {
      // Framing validation must not suppress a real semantic parser error,
      // including when a complete feature packet appears without DNA.
      const parserError = await seqparse(new TextDecoder().decode(source.slice(0, 19)), {
        fileName: 'synthetic.dna',
        source,
      }).catch((reason: Error) => reason);
      expect(parserError).toBeInstanceOf(Error);
      await expect(parseSequenceDocument(source, 'synthetic.dna')).rejects.toMatchObject({
        name: (parserError as Error).name,
        message: (parserError as Error).message,
      });
    } else {
      await expect(parseSequenceDocument(source, 'synthetic.dna')).rejects.toMatchObject({
        code: fixture.outcome,
      });
    }
    expect(new Uint8Array(source)).toEqual(original);
  });
});
