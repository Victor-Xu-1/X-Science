import type { TFunction } from 'i18next';

/** Shared text projection for stored Library items and discovered candidates. */
export const libraryAuthors = (item: { metadata: Record<string, unknown> }, t: TFunction): string => {
  const { authors, first_author } = item.metadata;
  const full =
    typeof authors === 'string'
      ? authors
      : Array.isArray(authors)
        ? authors.filter((author): author is string => typeof author === 'string' && Boolean(author.trim())).join('; ')
        : '';
  if (full.trim()) return full;
  const first = typeof first_author === 'string' ? first_author.trim() : '';
  return first ? t('conversation.reading.libraryFirstAuthor', { name: first }) : '';
};

export const libraryMetadataText = (value: unknown): string =>
  typeof value === 'string' ? value : typeof value === 'number' && Number.isSafeInteger(value) ? String(value) : '';
