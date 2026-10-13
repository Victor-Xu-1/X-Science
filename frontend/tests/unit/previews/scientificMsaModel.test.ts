import {
  parseScientificAlignment,
  resolveScientificAlignmentFormat,
} from '@/renderer/pages/conversation/Preview/components/viewers/scientificMsaModel';
import { describe, expect, it } from 'vitest';

const stockholmRecord = (rows: Array<[string, string]>, version = '1.0') =>
  `# STOCKHOLM ${version}\n${rows.map(([name, sequence]) => `${name} ${sequence}`).join('\n')}\n//\n`;

const insertAfterStockholmHeader = (record: string, insertedLines: string) => {
  const headerEnd = record.indexOf('\n');
  if (headerEnd === -1) {
    throw new Error('Stockholm test record must include a header newline');
  }
  return `${record.slice(0, headerEnd + 1)}${insertedLines}${record.slice(headerEnd + 1)}`;
};

describe('Stockholm fixture insertion', () => {
  it('inserts once after the header while preserving all remaining record bytes', () => {
    const record = '# STOCKHOLM 1.0\nalpha AC-G\nbeta ACUG\n//\n';
    expect(insertAfterStockholmHeader(record, '# inserted comment\n')).toBe(
      '# STOCKHOLM 1.0\n# inserted comment\nalpha AC-G\nbeta ACUG\n//\n'
    );
  });

  it('preserves an empty record and a CRLF header boundary', () => {
    expect(insertAfterStockholmHeader(stockholmRecord([]), '# inserted comment\n')).toBe(
      '# STOCKHOLM 1.0\n# inserted comment\n\n//\n'
    );
    expect(insertAfterStockholmHeader('# STOCKHOLM 1.0\r\nalpha AC-G\r\n//\r\n', '# inserted comment\r\n')).toBe(
      '# STOCKHOLM 1.0\r\n# inserted comment\r\nalpha AC-G\r\n//\r\n'
    );
  });

  it('requires a newline-terminated fixture header', () => {
    expect(() => insertAfterStockholmHeader('# STOCKHOLM 1.0', '# inserted comment\n')).toThrow(
      'Stockholm test record must include a header newline'
    );
  });
});

describe('scientific MSA model', () => {
  it('parses aligned FASTA records and normalizes dot gaps', () => {
    const model = parseScientificAlignment('>alpha\nAC.E\n>beta description\nACFE\n', 'aligned.fasta');

    expect(model).toEqual({
      format: 'fasta',
      sequences: [
        { name: 'alpha', sequence: 'AC-E' },
        { name: 'beta description', sequence: 'ACFE' },
      ],
      positionCount: 4,
      isNucleotide: false,
    });
  });

  it('combines repeated sequence blocks in CLUSTAL alignments', () => {
    const model = parseScientificAlignment(
      'CLUSTAL W\n\nalpha AC-D\nbeta  ACTD\n       ** *\n\nalpha EF\nbeta  EF\n',
      'alignment.aln'
    );

    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-DEF' },
      { name: 'beta', sequence: 'ACTDEF' },
    ]);
    expect(model.positionCount).toBe(6);
  });

  it('parses Stockholm records and detects nucleotide alignments', () => {
    const model = parseScientificAlignment('# STOCKHOLM 1.0\nseq1 ACG-U\nseq2 ACGUU\n//\n', 'alignment.sto');

    expect(model.format).toBe('stockholm');
    expect(model.isNucleotide).toBe(true);
    expect(model.sequences).toHaveLength(2);
  });

  it('preserves interleaved blocks inside one Stockholm record', () => {
    const model = parseScientificAlignment(
      '# STOCKHOLM 1.0\n#=GF ID family_one\nalpha ac.g\nbeta ACUG\n#=GC RF xxxx\n\nalpha UU-C\nbeta UUGC\n//\n',
      'alignment.sto'
    );

    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-GUU-C' },
      { name: 'beta', sequence: 'ACUGUUGC' },
    ]);
    expect(model.positionCount).toBe(8);
    expect(model.isNucleotide).toBe(true);
  });

  it.each(['1.0', '1.1', '1.9'])('parses a single Stockholm record with minor version %s', (version) => {
    const model = parseScientificAlignment(
      stockholmRecord(
        [
          ['alpha', 'ac.g'],
          ['beta', 'ACUG'],
        ],
        version
      ),
      'rna.sto'
    );
    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-G' },
      { name: 'beta', sequence: 'ACUG' },
    ]);
    expect(model.positionCount).toBe(4);
  });

  it('preserves single-record interleaving with a nonzero Stockholm minor version', () => {
    const model = parseScientificAlignment(
      '# STOCKHOLM 1.9\nalpha ac_g\nbeta ACUG\n\nalpha uu~c\nbeta UUGC\n//\n',
      'rna.sto'
    );
    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-GUU-C' },
      { name: 'beta', sequence: 'ACUGUUGC' },
    ]);
    expect(model.positionCount).toBe(8);
  });

  it.each([
    ['1.0', '1.1'],
    ['1.1', '1.0'],
    ['1.1', '1.9'],
    ['1.9', '1.9'],
  ])('rejects independent Stockholm records with minor versions %s and %s', (firstVersion, secondVersion) => {
    const first = stockholmRecord(
      [
        ['alpha', 'AC-G'],
        ['beta', 'ACUG'],
      ],
      firstVersion
    );
    const second = stockholmRecord(
      [
        ['alpha', 'UU-C'],
        ['beta', 'UUGC'],
      ],
      secondVersion
    );
    expect(() => parseScientificAlignment(first + second, 'families.sto')).toThrow('multiple-alignments');
  });

  it.each(['0.9', '2.0', '2.1'])('rejects an unsupported Stockholm major version %s', (version) => {
    expect(() =>
      parseScientificAlignment(
        stockholmRecord(
          [
            ['alpha', 'AC-G'],
            ['beta', 'ACUG'],
          ],
          version
        ),
        'rna.sto'
      )
    ).toThrow('parse-failed');
  });

  it('rejects an unsupported version header inside a valid Stockholm record', () => {
    expect(() =>
      parseScientificAlignment('# STOCKHOLM 1.0\nalpha AC-G\nbeta ACUG\n# STOCKHOLM 2.0\n//\n', 'rna.sto')
    ).toThrow('parse-failed');
  });

  it.each(['1.', '1.x'])('rejects an entire version-shaped header with malformed minor %s', (version) => {
    const record = stockholmRecord([
      ['alpha', 'AC-G'],
      ['beta', 'ACUG'],
    ]);
    const header = `# STOCKHOLM ${version}\n`;
    for (const input of [
      stockholmRecord(
        [
          ['alpha', 'AC-G'],
          ['beta', 'ACUG'],
        ],
        version
      ),
      insertAfterStockholmHeader(record, header),
      `${record}${header}`,
    ]) {
      expect(() => parseScientificAlignment(input, 'invalid.sto')).toThrow('parse-failed');
    }
  });

  it.each([
    [
      'shared names',
      [
        ['alpha', 'UU-C'],
        ['beta', 'UUGC'],
      ],
    ],
    [
      'distinct names',
      [
        ['gamma', 'UU-C'],
        ['delta', 'UUGC'],
      ],
    ],
    ['empty second record', []],
  ] as Array<[string, Array<[string, string]>]>)(
    'rejects multiple Stockholm records with %s instead of combining families',
    (_description, secondRows) => {
      const first = stockholmRecord([
        ['alpha', 'AC-G'],
        ['beta', 'ACUG'],
      ]);
      expect(() => parseScientificAlignment(first + stockholmRecord(secondRows), 'families.sto')).toThrow(
        'multiple-alignments'
      );
    }
  );

  it('rejects a leading empty Stockholm record followed by a populated record', () => {
    const populated = stockholmRecord([
      ['alpha', 'AC-G'],
      ['beta', 'ACUG'],
    ]);
    expect(() => parseScientificAlignment(stockholmRecord([]) + populated, 'families.sto')).toThrow(
      'multiple-alignments'
    );
  });

  it('accepts blank lines and comments after the single Stockholm terminator', () => {
    const record = stockholmRecord([
      ['alpha', 'AC-G'],
      ['beta', 'ACUG'],
    ]);
    const model = parseScientificAlignment(`# file comment\n\n${record}\n# trailing comment\n`, 'alignment.stk');
    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-G' },
      { name: 'beta', sequence: 'ACUG' },
    ]);
    expect(model.positionCount).toBe(4);
  });

  it.each([
    '# STOCKHOLM file generated for inspection',
    '# STOCKHOLM 1.0 file generated for inspection',
    '# STOCKHOLM 2026 export notes',
    '# STOCKHOLM format comment ending in 1.0',
    '# STOCKHOLM',
    '# STOCKHOLM-family annotation',
  ])('preserves ordinary Stockholm-word comments before, inside and after a record: %s', (comment) => {
    const record = stockholmRecord([
      ['alpha', 'AC-GU'],
      ['beta', 'ACCGU'],
    ]);
    const inputs = [
      `${comment}\n${record}`,
      insertAfterStockholmHeader(record, `${comment}\n`),
      `${record}${comment}\n`,
    ];
    for (const input of inputs) {
      const model = parseScientificAlignment(input, 'comments.sto');
      expect(model.sequences).toEqual([
        { name: 'alpha', sequence: 'AC-GU' },
        { name: 'beta', sequence: 'ACCGU' },
      ]);
      expect(model.positionCount).toBe(5);
    }
  });

  it('preserves Stockholm-word comments between legitimate interleaved blocks', () => {
    const model = parseScientificAlignment(
      '# STOCKHOLM 1.9\nalpha ac_g\nbeta ACUG\n\n# STOCKHOLM file generated for inspection\nalpha uu~c\nbeta UUGC\n//\n',
      'comments.sto'
    );
    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-GUU-C' },
      { name: 'beta', sequence: 'ACUGUUGC' },
    ]);
    expect(model.positionCount).toBe(8);
  });

  it('still rejects real independent headers separated by ordinary Stockholm-word comments', () => {
    const first = stockholmRecord(
      [
        ['alpha', 'AC-G'],
        ['beta', 'ACUG'],
      ],
      '1.1'
    );
    const second = stockholmRecord(
      [
        ['alpha', 'UU-C'],
        ['beta', 'UUGC'],
      ],
      '1.9'
    );
    expect(() =>
      parseScientificAlignment(`${first}# STOCKHOLM file generated for inspection\n${second}`, 'families.sto')
    ).toThrow('multiple-alignments');
  });

  it.each(['-', '.', '_', '~'])('retains all Stockholm rows and columns with %s gaps', (gap) => {
    const model = parseScientificAlignment(
      stockholmRecord([
        ['alpha', `ac${gap}g`],
        ['beta', 'ACUG'],
        ['gamma', 'ACUG'],
      ]),
      'alignment.stockholm'
    );
    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-G' },
      { name: 'beta', sequence: 'ACUG' },
      { name: 'gamma', sequence: 'ACUG' },
    ]);
    expect(model.positionCount).toBe(4);
    expect(model.isNucleotide).toBe(true);
  });

  it('retains alternative gaps in a two-row interleaved Stockholm alignment', () => {
    const model = parseScientificAlignment(
      '# STOCKHOLM 1.0\nalpha ac_g\nbeta ACUG\n\nalpha uu~c\nbeta UUGC\n//\n',
      'alignment.sto.gz'
    );
    expect(model.sequences).toEqual([
      { name: 'alpha', sequence: 'AC-GUU-C' },
      { name: 'beta', sequence: 'ACUGUUGC' },
    ]);
    expect(model.positionCount).toBe(8);
  });

  it.each([
    '',
    '# STOCKHOLM 1.0\n//\n',
    '# STOCKHOLM 1.0\nalpha ACGU\nbeta ACGU\n',
    'alpha ACGU\nbeta ACGU\n//\n',
    '# STOCKHOLM 1.0\nalpha AC!G\nbeta ACUG\ngamma ACUG\n//\n',
    '# STOCKHOLM 1.0\nalpha AC-G\nbeta ACUG\n//\ngamma ACUG\n',
  ])('rejects empty, incomplete or malformed Stockholm input: %j', (content) => {
    expect(() => parseScientificAlignment(content, 'invalid.sto')).toThrow('parse-failed');
  });

  it.each(['_', '~'])('does not extend FASTA or CLUSTAL gap semantics to %s', (gap) => {
    expect(() => parseScientificAlignment(`>alpha\nAC${gap}G\n>beta\nACUG\n`, 'alignment.fasta')).toThrow(
      'parse-failed'
    );
    expect(() => parseScientificAlignment(`CLUSTAL W\nalpha AC${gap}G\nbeta ACUG\n`, 'alignment.aln')).toThrow(
      'parse-failed'
    );
  });

  it('rejects malformed or non-aligned content with a user-facing error', () => {
    expect(() => parseScientificAlignment('>only\nACGT\n', 'alignment.fasta')).toThrow('parse-failed');
    expect(() => parseScientificAlignment('>one\nACGT\n>two\nACGTA\n', 'alignment.fasta')).toThrow('parse-failed');
  });

  it('resolves the v1.1 alignment extension matrix', () => {
    expect(resolveScientificAlignmentFormat('result.clustalw')).toBe('clustal');
    expect(resolveScientificAlignmentFormat('result.stockholm')).toBe('stockholm');
    expect(resolveScientificAlignmentFormat('result.mfa')).toBe('fasta');
    expect(resolveScientificAlignmentFormat('alignment.aln.gz')).toBe('clustal');
    expect(resolveScientificAlignmentFormat('aligned.fasta.gz')).toBe('fasta');
  });
});
