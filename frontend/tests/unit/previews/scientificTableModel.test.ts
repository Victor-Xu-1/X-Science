import {
  getScientificTableColumns,
  MAX_TABLE_PREVIEW_ROWS,
  parseScientificTable,
} from '@/renderer/pages/conversation/Preview/components/viewers/scientificTableModel';
import { gzipSync } from 'node:zlib';
import { describe, expect, it } from 'vitest';

describe('scientific table model', () => {
  it('parses delimited scientific data with SheetJS and preserves rectangular rows', async () => {
    const data = new TextEncoder().encode('gene,,score\nSTAT6,kinase,0.91\nCRBN,,0.83').buffer;
    const sheets = await parseScientificTable(data, { filename: 'scores.csv' });

    expect(sheets).toHaveLength(1);
    expect(sheets[0].rows).toEqual([
      ['gene', '', 'score'],
      ['STAT6', 'kinase', '0.91'],
      ['CRBN', '', '0.83'],
    ]);
    expect(sheets[0].sampled).toBe(false);
    expect(getScientificTableColumns(sheets[0])).toEqual(['gene', 'B', 'score']);
  });

  it('decodes UTF-8 delimited artifacts exactly instead of treating bytes as Windows-1252', async () => {
    const data = new TextEncoder().encode('unit,claim\nÅngström,mechanism — confirmed\n中文,NEK7–NLRP3').buffer;

    const sheets = await parseScientificTable(data, { filename: 'evidence.csv' });

    expect(sheets[0].rows).toEqual([
      ['unit', 'claim'],
      ['Ångström', 'mechanism — confirmed'],
      ['中文', 'NEK7–NLRP3'],
    ]);
  });

  it('bounds very wide delimited previews while keeping the first cells useful', async () => {
    const header = Array.from({ length: 140 }, (_, index) => `sample_${index + 1}`).join(',');
    const row = Array.from({ length: 140 }, (_, index) => String(index + 1)).join(',');
    const data = new TextEncoder().encode(`${header}\n${row}`).buffer;

    const sheets = await parseScientificTable(data, { filename: 'matrix.csv' });

    expect(sheets[0].rows).toHaveLength(2);
    expect(sheets[0].rows[0]).toHaveLength(120);
    expect(sheets[0].rows[0][0]).toBe('sample_1');
    expect(sheets[0].rows[0][119]).toBe('sample_120');
    expect(sheets[0].sampled).toBe(true);
  });

  it('decompresses only a bounded prefix of large gzip tables and keeps complete rows', async () => {
    const longCell = Array.from(
      { length: 2_500 },
      (_, index) => `${index.toString(36)}-${(index * 7919).toString(36)}`
    ).join('|');
    const csv = ['id,value', ...Array.from({ length: 360 }, (_, index) => `${index + 1},${longCell}`)].join('\n');
    const compressed = gzipSync(csv);
    const data = compressed.buffer.slice(compressed.byteOffset, compressed.byteOffset + compressed.byteLength);

    const sheets = await parseScientificTable(data, { filename: 'large.csv.gz' });

    expect(sheets[0].sampled).toBe(true);
    expect(sheets[0].rows.length).toBeLessThanOrEqual(MAX_TABLE_PREVIEW_ROWS + 1);
    expect(sheets[0].rows[0]).toEqual(['id', 'value']);
    expect(sheets[0].rows.at(-1)).toHaveLength(2);
  });

  it('parses SAM headers away from alignment rows and names optional tags', async () => {
    const sam = [
      '@HD\tVN:1.6\tSO:coordinate',
      '@SQ\tSN:chr1\tLN:248956422',
      'read-001\t0\tchr1\t101\t60\t4M\t*\t0\t0\tACGT\tIIII\tNM:i:0\tAS:i:4',
    ].join('\n');
    const data = new TextEncoder().encode(sam).buffer;

    const sheets = await parseScientificTable(data, { filename: 'variants.sam' });

    expect(sheets[0]).toEqual({
      name: 'alignments',
      rows: [
        ['QNAME', 'FLAG', 'RNAME', 'POS', 'MAPQ', 'CIGAR', 'RNEXT', 'PNEXT', 'TLEN', 'SEQ', 'QUAL', 'TAG_1', 'TAG_2'],
        ['read-001', '0', 'chr1', '101', '60', '4M', '*', '0', '0', 'ACGT', 'IIII', 'NM:i:0', 'AS:i:4'],
      ],
      sampled: false,
    });
  });

  it('decompresses SAM gzip input without exposing binary bytes to the table parser', async () => {
    const sam = '@HD\tVN:1.6\nread-001\t0\tchr1\t1\t20\t4M\t*\t0\t0\tACGT\tIIII';
    const compressed = gzipSync(sam);
    const data = compressed.buffer.slice(compressed.byteOffset, compressed.byteOffset + compressed.byteLength);

    const sheets = await parseScientificTable(data, { filename: 'reads.sam.gz' });

    expect(sheets[0].rows[1]).toEqual(['read-001', '0', 'chr1', '1', '20', '4M', '*', '0', '0', 'ACGT', 'IIII']);
  });

  it('keeps a usable preview when a bounded UTF-8 prefix ends inside a multibyte character', async () => {
    const byteLimit = 4 * 1024 * 1024;
    const prefix = 'id,value\n1,ok\n2,';
    const padding = 'x'.repeat(byteLimit - 1 - new TextEncoder().encode(prefix).byteLength);
    const data = new TextEncoder().encode(`${prefix}${padding}中文`).buffer;

    const sheets = await parseScientificTable(data, { filename: 'large.csv' });

    expect(sheets[0].sampled).toBe(true);
    expect(sheets[0].rows[0]).toEqual(['id', 'value']);
    expect(sheets[0].rows).toEqual([
      ['id', 'value'],
      ['1', 'ok'],
    ]);
  });
});

const DELIMITED_BYTE_LIMIT = 4 * 1024 * 1024;
const DELIMITED_FILENAMES = ['samples.csv', 'samples.tsv', 'samples.csv.gz', 'samples.tsv.gz'];

function bufferOf(bytes: Uint8Array): ArrayBuffer {
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
}

async function parseDelimited(source: string, filename: string) {
  const bytes = new TextEncoder().encode(source);
  return parseScientificTable(bufferOf(filename.endsWith('.gz') ? gzipSync(bytes) : bytes), { filename });
}

// Place a chosen suffix exactly at the byte cap, without a large static fixture.
function atByteCap(prefix: string, beforeCap: string, afterCap: string): string {
  const paddingLength = DELIMITED_BYTE_LIMIT - new TextEncoder().encode(prefix + beforeCap).byteLength;
  return `${prefix}${'x'.repeat(paddingLength)}${beforeCap}${afterCap}`;
}

describe('known-truncated delimited record boundaries', () => {
  it.each(DELIMITED_FILENAMES)('%s omits an incomplete quoted sample after complete records', async (filename) => {
    const delimiter = filename.includes('.tsv') ? '\t' : ',';
    const newline = filename.includes('.tsv') ? '\r\n' : '\n';
    const completeRows = [
      ['sample', 'note', 'value'],
      ['00123', `quoted${delimiter}delimiter`, '0.1234567890123456789'],
      ['00001', `escaped "quote"${newline}continued`, '0'],
      ['00002', '', 'NA'],
    ];
    const prefix = [
      completeRows[0].join(delimiter),
      `00123${delimiter}"quoted${delimiter}delimiter"${delimiter}0.1234567890123456789`,
      `00001${delimiter}"escaped ""quote""${newline}continued"${delimiter}0`,
      `00002${delimiter}${delimiter}NA`,
      `00124${delimiter}"`,
    ].join(newline);
    const source = atByteCap(prefix, `${newline}${'y'.repeat(100)}`, `continued"${delimiter}0.999${newline}`);

    const sheets = await parseDelimited(source, filename);

    expect(sheets).toHaveLength(1);
    expect(sheets[0].sampled).toBe(true);
    expect(sheets[0].rows).toEqual(completeRows);
  });

  it.each(['\n', '\r\n'])('keeps logical boundaries for %j multiline fields', async (newline) => {
    const prefix = `sample,note,value${newline}00123,valid,0.5${newline}00124,"`;
    const source = atByteCap(prefix, `""escaped quote${newline}`, `continued",0.999${newline}`);

    const sheets = await parseDelimited(source, 'quoted.csv');

    expect(sheets[0].rows).toEqual([
      ['sample', 'note', 'value'],
      ['00123', 'valid', '0.5'],
    ]);
    expect(sheets[0].sampled).toBe(true);
  });

  it.each([
    ['before a closing quote', '\n', '",0.999\n'],
    ['after a closing quote', '\n"', ',0.999\n'],
    ['inside an escaped quote pair', '\n"', '"continued",0.999\n'],
    ['after an escaped quote pair', '""\n', 'continued",0.999\n'],
  ])('drops the partial row when the cap is %s', async (_position, beforeCap, afterCap) => {
    const source = atByteCap('sample,note,value\n00123,valid,0.5\n00124,"', beforeCap, afterCap);

    const sheets = await parseDelimited(source, 'quote-edge.csv');

    expect(sheets[0].rows).toEqual([
      ['sample', 'note', 'value'],
      ['00123', 'valid', '0.5'],
    ]);
    expect(sheets[0].sampled).toBe(true);
  });

  it.each(['"', ''])('keeps a complete row before a quoted field opening at the cap (%j)', async (opening) => {
    const source = atByteCap(
      'sample,note,value\n00123,',
      `,0.5\n00124,${opening}`,
      `${opening ? '' : '"'}next\nline",0.999\n`
    );

    const sheets = await parseDelimited(source, 'opening-edge.csv');

    expect(sheets[0].rows).toHaveLength(2);
    expect(sheets[0].rows[1][0]).toBe('00123');
    expect(sheets[0].rows[1][2]).toBe('0.5');
    expect(sheets[0].sampled).toBe(true);
  });

  it('keeps a complete escaped-quote multiline record ending at the cap', async () => {
    const source = atByteCap('sample,note,value\n00123,"', '""quote\nline",0.5\r\n', '00124,partial,0.999');

    const sheets = await parseDelimited(source, 'complete-edge.csv');

    expect(sheets[0].rows).toHaveLength(2);
    expect(sheets[0].rows[1][0]).toBe('00123');
    expect(sheets[0].rows[1][1]).toMatch(/"quote\nline$/);
    expect(sheets[0].rows[1][2]).toBe('0.5');
    expect(sheets[0].sampled).toBe(true);
  });

  it.each(['unquoted', 'quoted'])('returns no sheet when the first %s record is incomplete', async (kind) => {
    const prefix = kind === 'quoted' ? '"first\n' : 'first,';
    const source = atByteCap(prefix, '', kind === 'quoted' ? '",value\n' : ',value\n');

    expect(await parseDelimited(source, 'first-partial.csv')).toEqual([]);
  });

  it('keeps only the header when no complete data record fits', async () => {
    const source = atByteCap('sample,note,value\n00124,"', '\n', 'continued",0.999\n');

    const sheets = await parseDelimited(source, 'header-only.csv');

    expect(sheets[0].rows).toEqual([['sample', 'note', 'value']]);
    expect(sheets[0].sampled).toBe(true);
  });

  it.each(['utf-16le', 'utf-16be'])('omits a partial quoted record after %s decoding', async (encoding) => {
    const prefix = 'sample,note,value\r\n00123,中文,0.5\r\n00124,"';
    const padding = 'x'.repeat(DELIMITED_BYTE_LIMIT / 2 - 2 - prefix.length);
    const source = `${prefix}${padding}\n中文",0.999\r\n`;
    const encoded = Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from(source, 'utf16le')]);
    if (encoding === 'utf-16be') encoded.swap16();

    const sheets = await parseScientificTable(bufferOf(encoded), { filename: 'utf16.csv' });

    expect(sheets[0].rows).toEqual([
      ['sample', 'note', 'value'],
      ['00123', '中文', '0.5'],
    ]);
    expect(sheets[0].sampled).toBe(true);
  });

  it('retains SAM physical-line semantics when an alignment contains a quote', async () => {
    const complete = 'read-001\t0\tchr1\t101\t60\t4M\t*\t0\t0\tACGT\tIIII\tCO:Z:"note\n';
    const source = `${complete}read-002\t${'x'.repeat(DELIMITED_BYTE_LIMIT)}\n`;
    const sheets = await parseDelimited(source, 'reads.sam');

    expect(sheets[0].rows).toHaveLength(2);
    expect(sheets[0].rows[1][0]).toBe('read-001');
    expect(sheets[0].rows[1].at(-1)).toBe('CO:Z:"note');
    expect(sheets[0].sampled).toBe(true);
  });
});

describe('complete delimited source compatibility', () => {
  it.each(DELIMITED_FILENAMES)('%s keeps a valid final multiline record without a newline', async (filename) => {
    const delimiter = filename.includes('.tsv') ? '\t' : ',';
    const source = `sample${delimiter}note${delimiter}value\n00123${delimiter}"delimiter${delimiter} and ""quote""\ncontinued"${delimiter}0.999`;
    const sheets = await parseDelimited(source, filename);

    expect(sheets[0].rows).toEqual([
      ['sample', 'note', 'value'],
      ['00123', `delimiter${delimiter} and "quote"\ncontinued`, '0.999'],
    ]);
    expect(sheets[0].sampled).toBe(false);
  });

  it.each(['exact.csv', 'exact.csv.gz'])(
    '%s keeps an unterminated-by-newline final row when EOF is exactly at the cap',
    async (filename) => {
      const prefix = 'sample,note\n00123,';
      const source = atByteCap(prefix, '', '');
      const sheets = await parseDelimited(source, filename);

      expect(sheets[0].rows).toHaveLength(2);
      expect(sheets[0].rows[1][0]).toBe('00123');
      expect(sheets[0].rows[1][1]).toHaveLength(DELIMITED_BYTE_LIMIT - prefix.length);
      expect(sheets[0].sampled).toBe(false);
    }
  );

  it('preserves leading zeros, high precision, zero, empty cells and NA as distinct original text', async () => {
    const source =
      'sample_id,effect,p_value,count,missing\n00123,0.1234567890123456789,1e-50,9007199254740993,\n00001,0.0000000000000000123456789,0,12,NA';
    const sheets = await parseDelimited(source, 'precision.csv');

    expect(sheets[0].rows).toEqual([
      ['sample_id', 'effect', 'p_value', 'count', 'missing'],
      ['00123', '0.1234567890123456789', '1e-50', '9007199254740993', ''],
      ['00001', '0.0000000000000000123456789', '0', '12', 'NA'],
    ]);
  });
});

const SEPARATOR_DIRECTIVE_CASES = DELIMITED_FILENAMES.flatMap((filename) =>
  [
    ['LF', '\n'],
    ['CRLF', '\r\n'],
    ['CR', '\r'],
  ].map(([name, newline]) => [filename, name, newline])
);

describe('SheetJS separator directive compatibility', () => {
  it.each(SEPARATOR_DIRECTIVE_CASES)(
    '%s honors a %s sep directive when dropping an incomplete record',
    async (filename, _name, newline) => {
      const prefix = `sep=;${newline}sample;note;value\n00123;valid;0.5\n00124;"`;
      const source = atByteCap(prefix, `\n${'y'.repeat(100)}`, 'continued";0.999\n');

      const sheets = await parseDelimited(source, filename);

      expect(sheets[0].rows).toHaveLength(2);
      expect(sheets[0].rows).toEqual([
        ['sample', 'note', 'value'],
        ['00123', 'valid', '0.5'],
      ]);
      expect(sheets[0].sampled).toBe(true);
    }
  );

  it.each(SEPARATOR_DIRECTIVE_CASES)(
    '%s retains complete files with a %s sep directive',
    async (filename, _name, newline) => {
      const source = `sep=;${newline}sample;note;value\n00123;"quoted;delimiter and ""quote""\ncontinued";0.999`;

      const sheets = await parseDelimited(source, filename);

      expect(sheets[0].rows).toEqual([
        ['sample', 'note', 'value'],
        ['00123', 'quoted;delimiter and "quote"\ncontinued', '0.999'],
      ]);
      expect(sheets[0].sampled).toBe(false);
    }
  );
});

it.each(DELIMITED_FILENAMES)(
  '%s omits an incomplete record with quoted-field reopening tolerated by SheetJS',
  async (filename) => {
    const delimiter = filename.includes('.tsv') ? '\t' : ',';
    const source = atByteCap(
      `sample${delimiter}note${delimiter}value\n00123${delimiter}valid${delimiter}0.5\n00124${delimiter}"`,
      '" reopened "inside\n' + 'y'.repeat(90),
      'y'.repeat(200) + `"${delimiter}0.999\n`
    );
    // This is tolerant parser compatibility, not an RFC-valid CSV fixture.
    const XLSX = await import('xlsx-republish');
    const workbook = XLSX.read(source, { type: 'string', cellDates: true, FS: delimiter });
    const fullRows = XLSX.utils.sheet_to_json<string[]>(workbook.Sheets[workbook.SheetNames[0]], {
      header: 1,
      raw: false,
    });
    expect(fullRows[2][0]).toBe('00124');
    expect(fullRows[2][2]).toBe('0.999');

    const sheets = await parseDelimited(source, filename);

    expect(sheets[0].rows).toHaveLength(2);
    expect(sheets[0].rows).toEqual([
      ['sample', 'note', 'value'],
      ['00123', 'valid', '0.5'],
    ]);
    expect(sheets[0].sampled).toBe(true);
  }
);

it.each(['sep=xx\n', 'sep=;extra\n'])(
  'preserves a malformed sep declaration %j while dropping its partial sample',
  async (declaration) => {
    const source = atByteCap(
      `${declaration}sample;note;value\n00123;valid;0.5\n00124;"`,
      `\n${'y'.repeat(100)}`,
      'continued";0.999\n'
    );

    const sheets = await parseDelimited(source, 'malformed-sep.csv');

    expect(sheets[0].rows).toHaveLength(3);
    expect(sheets[0].rows).toEqual([
      Array.from({ length: 3 }, (_, index) => declaration.trim().split(';')[index] ?? ''),
      ['sample', 'note', 'value'],
      ['00123', 'valid', '0.5'],
    ]);
    expect(sheets[0].sampled).toBe(true);
  }
);

it('keeps the originally inferred separator after trimming a malformed directive source', async () => {
  const declaration = 'sep=xx' + ','.repeat(16);
  const prefix = `${declaration}\nsample;note;value\n00123;"first\nsecond";0.5\n`;
  const source = prefix + '00124;' + 'a;'.repeat(800) + 'x'.repeat(DELIMITED_BYTE_LIMIT) + ';0.999\n';
  const XLSX = await import('xlsx-republish');
  const readRows = (text: string) => {
    const workbook = XLSX.read(text, { type: 'string', cellDates: true, FS: ',' });
    return XLSX.utils.sheet_to_json<string[]>(workbook.Sheets[workbook.SheetNames[0]], { header: 1, raw: false });
  };
  expect(readRows(source)[2]).toEqual(['00123', 'first\nsecond', '0.5']);
  // A shorter prefix makes SheetJS choose comma instead of the original semicolon.
  expect(readRows(prefix)[1][0]).toBe('sample;note;value');

  const sheets = await parseDelimited(source, 'inferred-sep.csv');

  expect(sheets[0].rows).toHaveLength(3);
  expect(sheets[0].rows).toEqual([
    [declaration, '', ''],
    ['sample', 'note', 'value'],
    ['00123', 'first\nsecond', '0.5'],
  ]);
  expect(sheets[0].sampled).toBe(true);
});

it('returns no sheet when a separator control line is followed only by a partial first record', async () => {
  const source = atByteCap('sep=;\n"first\n', '', 'continued";0.999\n');

  expect(await parseDelimited(source, 'directive-first-partial.csv')).toEqual([]);
});
