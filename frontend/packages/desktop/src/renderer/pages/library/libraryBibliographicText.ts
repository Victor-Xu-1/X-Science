/** Shared text projection for stored Library items and discovered candidates. */
export const libraryAuthors = (item: { metadata: Record<string, unknown> }): string => {
  const value = item.metadata.authors;
  return typeof value === 'string'
    ? value
    : Array.isArray(value)
      ? value.filter((author): author is string => typeof author === 'string').join('; ')
      : '';
};

export const libraryMetadataText = (value: unknown): string =>
  typeof value === 'string' ? value : typeof value === 'number' && Number.isSafeInteger(value) ? String(value) : '';
