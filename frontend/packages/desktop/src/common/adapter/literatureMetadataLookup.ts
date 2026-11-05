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
  if (
    (pmidLookup
      ? !bounded(value.pmid, 9) ||
        !/^[1-9][0-9]{0,8}$/.test(value.pmid) ||
        metadata.pmid !== value.pmid ||
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
