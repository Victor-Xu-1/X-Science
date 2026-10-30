export type LiteratureBibtex = {
  schema: 'x-science.literature-bibtex.v1';
  item_ids: string[];
  bibtex: string;
};

export function decodeLiteratureBibtex(value: unknown, itemIds: readonly string[]): LiteratureBibtex {
  const result = value as Partial<LiteratureBibtex> | null;
  if (
    !result ||
    result.schema !== 'x-science.literature-bibtex.v1' ||
    !Array.isArray(result.item_ids) ||
    result.item_ids.length !== itemIds.length ||
    result.item_ids.some((id, index) => id !== itemIds[index]) ||
    typeof result.bibtex !== 'string' ||
    !result.bibtex.trim()
  )
    throw new Error('Invalid Library BibTeX export response');
  return result as LiteratureBibtex;
}
