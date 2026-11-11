const columns = [
  'item_id',
  'title',
  'authors',
  'year',
  'item_type',
  'publication',
  'volume',
  'issue',
  'pages',
  'doi',
  'pmid',
  'pmcid',
  'arxiv_id',
  'url',
  'abstract',
  'language',
] as const;
export const literatureCsvHeader = columns.map((column) => `"${column}"`).join(',') + '\r\n';

const invalid = (): never => {
  throw new Error('Invalid Library CSV export response');
};

// Bind each decoded row to the requested ID, including the formatter's inert
// spreadsheet prefix. Metadata is never re-formatted by this transport codec.
function encodedIdText(id: string): string {
  // eslint-disable-next-line no-control-regex -- The CSV contract includes control-prefixed formula text.
  const shield = /^[\s\u0000-\u001f]*[=+@＝＋＠－-]/u.test(id) || /^[\t\r\n]/u.test(id);
  return (shield ? "'" : '') + id.replace(/\r\n|\r|\n/gu, '\r\n');
}

function readCell(content: string, start: number, capture: boolean): { next: number; value: string } {
  if (content[start] !== '"') return invalid();
  let value = '';
  for (let offset = start + 1; offset < content.length; offset++) {
    const current = content[offset];
    if (current === '"') {
      if (content[offset + 1] !== '"') return { next: offset + 1, value };
      if (capture) value += '"';
      offset++;
    } else if (current === '\r') {
      if (content[offset + 1] !== '\n') return invalid();
      if (capture) value += '\r\n';
      offset++;
    } else {
      if (current === '\n') return invalid();
      if (capture) value += current;
    }
  }
  return invalid();
}

/** Strict complete-batch framing; quoted multiline cells are not line-split. */
export function validateLiteratureCsvBatch(content: string, ids: readonly string[]): void {
  if (!content.startsWith(literatureCsvHeader)) return invalid();
  let offset = literatureCsvHeader.length;
  for (const id of ids) {
    for (let column = 0; column < columns.length; column++) {
      const cell = readCell(content, offset, column === 0);
      if (column === 0 && cell.value !== encodedIdText(id)) return invalid();
      const delimiter = column === columns.length - 1 ? '\r\n' : ',';
      if (!content.startsWith(delimiter, cell.next)) return invalid();
      offset = cell.next + delimiter.length;
    }
  }
  if (offset !== content.length) return invalid();
}
