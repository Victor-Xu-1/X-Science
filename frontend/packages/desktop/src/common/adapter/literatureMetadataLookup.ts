import { LITERATURE_METADATA_SNAPSHOT_BYTES, literatureMetadataEncodedBytes } from './literatureMetadataCapacity';

export type LiteratureMetadataLookupScheme = 'doi' | 'pmid';

type LiteratureDraftFields = {
  reference: string;
  doi: string;
  title: string;
};

type LiteratureDraftMetadata = Record<string, unknown> & { authors: string[]; year?: string };

export type LiteratureMetadataDraft = LiteratureDraftFields &
  (
    | {
        schema: 'x-science.literature-metadata-draft.v1';
        metadata: LiteratureDraftMetadata & { doi: string };
      }
    | {
        schema: 'x-science.literature-pmid-draft.v1';
        pmid: string;
        metadata: LiteratureDraftMetadata & { pmid: string; doi?: string };
      }
  );

const scalarKeys = new Set([
  'doi',
  'item_type',
  'year',
  'container_title',
  'url',
  'abstract',
  'volume',
  'issue',
  'pages',
  'publisher',
]);
const record = (value: unknown): value is Record<string, unknown> =>
  Boolean(value && typeof value === 'object' && !Array.isArray(value));
const bounded = (value: unknown, maximum: number): value is string =>
  typeof value === 'string' && new TextEncoder().encode(value).length <= maximum && !value.includes('\0');
const invalid = (): never => {
  throw new Error('Invalid Literature metadata draft');
};

/** Matches the PMID reference forms accepted by the Go lookup endpoint. */
function normalizePMIDLookupReference(reference: string): string | undefined {
  if (/\p{Cc}/u.test(reference)) return undefined;
  const value = reference.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '').replace(/^PMID *: */i, '');
  const pmid =
    /^https?:\/\/(?:www\.)?pubmed\.ncbi\.nlm\.nih\.gov\/([0-9]+)\/?(?:[?#][\s\S]*)?$/iu.exec(value)?.[1] ?? value;
  return /^[1-9][0-9]{0,8}$/.test(pmid) ? pmid : undefined;
}

/** A proposal is not a catalogue item, receipt, attachment or execution fact. */
export function decodeLiteratureMetadataDraft(
  value: unknown,
  reference: string,
  scheme: LiteratureMetadataLookupScheme = 'doi'
): LiteratureMetadataDraft {
  const pmidLookup = scheme === 'pmid';
  if (
    !record(value) ||
    Object.keys(value).length !== (pmidLookup ? 6 : 5) ||
    value.schema !== (pmidLookup ? 'x-science.literature-pmid-draft.v1' : 'x-science.literature-metadata-draft.v1') ||
    value.reference !== reference ||
    !bounded(value.reference, 8192) ||
    !bounded(value.doi, 2048) ||
    (value.doi === '' ? !pmidLookup : !/^10\.\d{4,9}\/\S+$/.test(value.doi)) ||
    /\p{Cc}/u.test(value.doi) ||
    !bounded(value.title, 4096) ||
    !value.title.trim() ||
    !record(value.metadata)
  )
    return invalid();
  const metadata = value.metadata;
  const expectedPMID = pmidLookup ? normalizePMIDLookupReference(reference) : undefined;
  if (
    (pmidLookup
      ? !expectedPMID ||
        value.pmid !== expectedPMID ||
        metadata.pmid !== expectedPMID ||
        metadata.doi !== (value.doi || undefined)
      : metadata.doi !== value.doi) ||
    !Array.isArray(metadata.authors) ||
    metadata.authors.length > 1000 ||
    metadata.authors.some((author) => !bounded(author, 1000) || !author.trim())
  )
    return invalid();
  for (const [key, field] of Object.entries(metadata)) {
    if (key === 'authors' || (pmidLookup && key === 'pmid')) continue;
    if (!scalarKeys.has(key) || !bounded(field, key === 'abstract' ? 64 * 1024 : 4096)) return invalid();
  }
  if (
    metadata.year !== undefined &&
    (typeof metadata.year !== 'string' || !/^\d{4}$/.test(metadata.year) || Number(metadata.year) < 1)
  )
    return invalid();
  if (literatureMetadataEncodedBytes(JSON.stringify(metadata)) > LITERATURE_METADATA_SNAPSHOT_BYTES) return invalid();
  return value as LiteratureMetadataDraft;
}
