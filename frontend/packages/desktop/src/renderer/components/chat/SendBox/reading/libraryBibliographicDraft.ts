import type { LiteratureCommand, LiteratureItem, LiteratureMetadataPatch } from '@/common/adapter/literatureLibrary';

export const libraryReferenceTypes = [
  'journalArticle',
  'review',
  'preprint',
  'conferencePaper',
  'book',
  'bookSection',
  'thesis',
  'report',
  'dataset',
  'standard',
  'patent',
  'webpage',
  'document',
] as const;

/** Existing provider keys and one scalar key per additional bibliographic field. */
export const libraryScalarKeys = [
  'item_type',
  'container_title',
  'url',
  'abstract',
  'volume',
  'issue',
  'pages',
  'publisher',
  'publisher_place',
  'edition',
] as const;
export const libraryAdvancedScalarKeys = [
  'volume',
  'issue',
  'pages',
  'publisher',
  'publisher_place',
  'edition',
] as const;
export type LibraryScalarKey = (typeof libraryScalarKeys)[number];
export type LibraryScalarDraft = Record<LibraryScalarKey, string>;
export type LibraryBasicDraft = { title: string; authors: string; year: string; doi: string };

export function projectLibraryScalars(metadata: LiteratureItem['metadata']): LibraryScalarDraft {
  return Object.fromEntries(
    libraryScalarKeys.map((key) => [key, typeof metadata[key] === 'string' ? metadata[key] : ''])
  ) as LibraryScalarDraft;
}

export function projectLibraryBasic(item: LiteratureItem): LibraryBasicDraft {
  return {
    title: item.title,
    authors:
      typeof item.metadata.authors === 'string'
        ? item.metadata.authors
        : Array.isArray(item.metadata.authors)
          ? item.metadata.authors.filter((author): author is string => typeof author === 'string').join('; ')
          : '',
    year:
      typeof item.metadata.year === 'string' || typeof item.metadata.year === 'number'
        ? String(item.metadata.year)
        : '',
    doi: typeof item.metadata.doi === 'string' ? item.metadata.doi : '',
  };
}

/** Build only intentional field actions; unedited objects/numeric lexemes never enter the payload. */
export function buildLibraryBibliographicEdit(
  item: LiteratureItem,
  basic: LibraryBasicDraft,
  scalars: LibraryScalarDraft,
  touched: ReadonlySet<LibraryScalarKey>
): LiteratureCommand {
  const originalBasic = projectLibraryBasic(item);
  const set: Record<string, string> = {};
  const remove: string[] = [];
  const change = (key: string, value: string) => {
    const next = value.trim();
    if (next) set[key] = next;
    else if (Object.hasOwn(item.metadata, key)) remove.push(key);
  };
  for (const key of ['authors', 'year', 'doi'] as const)
    if (basic[key] !== originalBasic[key] && basic[key].trim() !== originalBasic[key].trim()) change(key, basic[key]);
  for (const key of libraryScalarKeys) {
    if (!touched.has(key)) continue;
    const previous = item.metadata[key];
    if (typeof previous === 'string' && scalars[key].trim() === previous.trim()) continue;
    change(key, scalars[key]);
  }
  const patch: LiteratureMetadataPatch = {
    ...(Object.keys(set).length ? { set } : {}),
    ...(remove.length ? { remove } : {}),
  };
  const identity = { operation_id: crypto.randomUUID(), item_id: item.id, expected_revision: item.metadata_revision };
  return Object.keys(patch).length
    ? {
        kind: 'edit-metadata',
        ...identity,
        ...(basic.title.trim() !== item.title ? { title: basic.title.trim() } : {}),
        metadata_patch: patch,
      }
    : { kind: 'edit', ...identity, title: basic.title.trim() };
}
