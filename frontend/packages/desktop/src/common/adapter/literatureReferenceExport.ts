export const literatureExportFormats = {
  bibtex: { extension: 'bib', mimeType: 'application/x-bibtex;charset=utf-8' },
  ris: { extension: 'ris', mimeType: 'application/x-research-info-systems;charset=utf-8' },
} as const;
export type LiteratureExportFormat = keyof typeof literatureExportFormats;
export type LiteratureReferenceExport = {
  schema: 'x-science.literature-export.v1';
  item_ids: string[];
  format: LiteratureExportFormat;
  content: string;
};
export function isLiteratureExportFormat(value: unknown): value is LiteratureExportFormat {
  return value === 'bibtex' || value === 'ris';
}
export function decodeLiteratureReferenceExport(
  value: unknown,
  itemIds: readonly string[],
  format: LiteratureExportFormat
): LiteratureReferenceExport {
  const result = value as Partial<LiteratureReferenceExport> | null;
  if (
    !result ||
    result.schema !== 'x-science.literature-export.v1' ||
    result.format !== format ||
    !Array.isArray(result.item_ids) ||
    result.item_ids.length !== itemIds.length ||
    result.item_ids.some((id, index) => id !== itemIds[index]) ||
    typeof result.content !== 'string' ||
    !result.content.trim()
  )
    throw new Error('Invalid Library reference export response');
  return result as LiteratureReferenceExport;
}
export function literatureExportFilename(stem: string, format: LiteratureExportFormat): string {
  const normalized = Array.from(stem.normalize('NFKC'), (character) => {
    const code = character.codePointAt(0)!;
    return code < 32 || code === 127 ? ' ' : character;
  }).join('');
  const safe =
    normalized
      .trim()
      .replace(/[/:*?"<>|\\]/gu, '-')
      .replace(/\s+/gu, ' ')
      .slice(0, 80) || 'references';
  return `${safe}.${literatureExportFormats[format].extension}`;
}
