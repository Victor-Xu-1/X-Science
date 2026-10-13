import type { Annotation, Seq } from 'seqparse';
import { ScientificPreviewError } from './scientificPreviewError';

export type SequenceFormat = 'genbank' | 'fasta' | 'fastq' | 'snapgene' | 'sbol' | 'jbei' | 'unknown';

export type FastqQualityStats = {
  min: number;
  max: number;
  mean: number;
};

export type SequenceDocument = {
  name: string;
  seq: string;
  type: Seq['type'];
  annotations: Annotation[];
  format: SequenceFormat;
  recordCount?: number;
  qualityStats?: FastqQualityStats;
};

export function resolveSequenceFormat(filename: string): SequenceFormat {
  const normalized = stripCompressionSuffix(filename.toLowerCase());
  if (hasExtension(normalized, ['.gb', '.gbk', '.genbank'])) return 'genbank';
  if (hasExtension(normalized, ['.fasta', '.fa', '.fna', '.fas', '.faa'])) return 'fasta';
  if (hasExtension(normalized, ['.fastq', '.fq'])) return 'fastq';
  if (normalized.endsWith('.dna')) return 'snapgene';
  if (normalized.endsWith('.sbol')) return 'sbol';
  if (normalized.endsWith('.jbei')) return 'jbei';
  return 'unknown';
}

export async function parseSequenceDocument(input: string | ArrayBuffer, filename: string): Promise<SequenceDocument> {
  const format = resolveSequenceFormat(filename);
  const uncompressedFilename = stripCompressionSuffix(filename);
  const parserFilename = format === 'snapgene' ? uncompressedFilename.replace(/\.dna$/i, '.dna') : uncompressedFilename;
  if (format === 'fastq') return parseFastqDocument(input, parserFilename);

  const parserInput = input instanceof ArrayBuffer ? decodeSequenceParserInput(input, format) : input;
  if (usesGenBankParser(parserInput, parserFilename)) {
    assertSupportedGenBankLocations(parserInput);
  }

  const { default: seqparse } = await import('seqparse');
  const parsed =
    input instanceof ArrayBuffer
      ? await seqparse(parserInput, { fileName: parserFilename, source: input })
      : await seqparse(parserInput, { fileName: parserFilename });

  if (!parsed?.seq) throw new ScientificPreviewError('empty-content');
  return {
    name: parsed.name || parserFilename.replace(/\.[^/.]+$/, ''),
    seq: parsed.seq,
    type: parsed.type,
    annotations: parsed.annotations ?? [],
    format,
  };
}

function usesGenBankParser(source: string, filename: string): boolean {
  // Mirror the locked seqparse dispatch predicates through its GenBank branch.
  // JBEI and FASTA take priority, even if their text contains a GenBank example
  // or their filename suggests GenBank. Keep filename checks case-sensitive,
  // as in seqparse; resolveSequenceFormat alone does not identify this branch.
  if (source.substring(0, 200).includes(':seq="http://jbei.org/sequence"') || source.startsWith('<seq:seq')) {
    return false;
  }
  if (source.startsWith('>') || source.startsWith(';') || hasExtension(filename, ['.seq', '.fa', '.fas', '.fasta'])) {
    return false;
  }
  return (
    (source.includes('LOCUS') && source.includes('ORIGIN')) ||
    hasExtension(filename, ['.gb', '.gbk', '.genbank', '.ape'])
  );
}

function assertSupportedGenBankLocations(source: string): void {
  let inFeatures = false;
  let inLocation = false;
  let location = '';
  const checkLocation = (): void => {
    // The locked parser and SeqViz selection API have one start/end range.
    // Splitting a compound feature or using its bounding interval would change
    // its biological order/selection, so reject instead of silently truncating.
    if (/(?:^|[,(])(?:join|order)\(/i.test(location.replace(/\s+/g, ''))) {
      throw new ScientificPreviewError('unsupported-sequence-location');
    }
    location = '';
  };

  for (const line of source.split(/\r?\n/)) {
    if (/^FEATURES(?:\s|$)/.test(line)) {
      checkLocation();
      inFeatures = true;
      inLocation = false;
      continue;
    }
    if (!inFeatures) continue;
    // GenBank feature keys occupy columns 6–20; locations and qualifiers begin
    // at column 22. Non-indented lines end the feature table (e.g. ORIGIN).
    if (/^\S/.test(line)) {
      checkLocation();
      inFeatures = false;
      inLocation = false;
      continue;
    }
    if (line.slice(0, 5) !== '     ') continue;
    const featureKey = line.slice(5, 21).trim();
    const value = line.slice(21).trim();
    if (featureKey) {
      checkLocation();
      inLocation = true;
      location = value;
    } else if (inLocation && value.startsWith('/')) {
      checkLocation();
      // Ignore the entire qualifier block, including wrapped quoted text.
      inLocation = false;
    } else if (inLocation) {
      location += value;
    }
  }
  checkLocation();
}

function decodeSequenceParserInput(input: ArrayBuffer, format: SequenceFormat): string {
  if (format !== 'snapgene') return new TextDecoder('utf-8').decode(input);

  const bytes = new Uint8Array(input);
  const header = bytes.subarray(0, 19);
  const view = new DataView(input);
  if (
    header.byteLength !== 19 ||
    header[0] !== 9 ||
    view.getUint32(1) !== 14 ||
    !header.subarray(5, 13).every((byte, index) => byte === 'SnapGene'.charCodeAt(index))
  ) {
    throw new ScientificPreviewError('parse-failed');
  }
  // The native parser clamps short reads, so validate every packet before it
  // can treat a truncated payload as a complete sequence or skip an overrun.
  let offset = header.byteLength;
  while (offset < bytes.byteLength) {
    if (bytes.byteLength - offset < 5) throw new ScientificPreviewError('parse-failed');
    const type = bytes[offset];
    const length = view.getUint32(offset + 1);
    offset += 5;
    if (length > bytes.byteLength - offset || (type === 0 && length < 1)) {
      throw new ScientificPreviewError('parse-failed');
    }
    offset += length;
  }
  // The public API requires text and sniffs it before its native filename branch.
  // Use only the validated file header for dispatch so Notes/features cannot select
  // a text parser. The exact complete input remains its scientific source buffer.
  return new TextDecoder('utf-8').decode(header);
}

async function parseFastqDocument(input: string | ArrayBuffer, filename: string): Promise<SequenceDocument> {
  const source =
    typeof input === 'string' ? input : new TextDecoder('utf-8', { fatal: true }).decode(new Uint8Array(input));
  const records = parseFastqRecords(source);
  const first = records.first;
  if (!first) throw new ScientificPreviewError('empty-content');

  const parsedType = inferSequenceType(first.sequence);
  return {
    name: first.name || filename.replace(/\.[^/.]+$/, ''),
    seq: first.sequence,
    type: parsedType,
    annotations: [],
    format: 'fastq',
    recordCount: records.count,
    qualityStats: records.qualityStats,
  };
}

type FastqRecord = { name: string; sequence: string };

function parseFastqRecords(source: string): {
  count: number;
  first: FastqRecord | null;
  qualityStats: FastqQualityStats;
} {
  const lines = source.replace(/^\uFEFF/, '').split(/\r?\n/);
  let index = 0;
  let count = 0;
  let qualityCount = 0;
  let qualitySum = 0;
  let qualityMin = Number.POSITIVE_INFINITY;
  let qualityMax = Number.NEGATIVE_INFINITY;
  let first: FastqRecord | null = null;

  while (index < lines.length) {
    if (lines[index] === '') {
      index += 1;
      continue;
    }
    const header = lines[index];
    if (!header.startsWith('@')) throw new ScientificPreviewError('parse-failed');
    index += 1;

    const sequenceLines: string[] = [];
    while (index < lines.length && !lines[index].startsWith('+')) {
      if (!lines[index]) throw new ScientificPreviewError('parse-failed');
      sequenceLines.push(lines[index].replace(/\s+/g, ''));
      index += 1;
    }
    if (index >= lines.length || sequenceLines.length === 0) throw new ScientificPreviewError('parse-failed');
    const sequence = sequenceLines.join('').toUpperCase();
    index += 1;

    const qualityLines: string[] = [];
    let qualityLength = 0;
    while (index < lines.length && qualityLength < sequence.length) {
      const qualityLine = lines[index];
      qualityLines.push(qualityLine);
      qualityLength += qualityLine.length;
      index += 1;
    }
    if (qualityLength !== sequence.length) throw new ScientificPreviewError('parse-failed');

    const quality = qualityLines.join('');
    for (const character of quality) {
      const score = Math.max(0, character.charCodeAt(0) - 33);
      qualityCount += 1;
      qualitySum += score;
      qualityMin = Math.min(qualityMin, score);
      qualityMax = Math.max(qualityMax, score);
    }

    count += 1;
    first ??= { name: header.slice(1).trim().split(/\s+/, 1)[0] ?? '', sequence };
  }

  if (count === 0 || qualityCount === 0) throw new ScientificPreviewError('empty-content');
  return {
    count,
    first,
    qualityStats: {
      min: qualityMin,
      max: qualityMax,
      mean: Number((qualitySum / qualityCount).toFixed(2)),
    },
  };
}

function inferSequenceType(sequence: string): Seq['type'] {
  if (/^[ACGTNURYKMSWBDHV.-]+$/i.test(sequence)) return sequence.includes('U') ? 'rna' : 'dna';
  if (/^[A-Z*.-]+$/i.test(sequence)) return 'aa';
  return 'unknown';
}

function hasExtension(filename: string, extensions: string[]): boolean {
  return extensions.some((extension) => filename.endsWith(extension));
}

function stripCompressionSuffix(filename: string): string {
  return filename.toLowerCase().endsWith('.gz') ? filename.slice(0, -3) : filename;
}
