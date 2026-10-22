import {
  decodePdfReadingSelection,
  decodePdfReadingSource,
  pdfReadingSourceKey,
  samePdfReadingSource,
  MAX_READING_PDFS,
  MAX_READING_PDF_BYTES,
  type PdfReadingSelection,
  type PdfReadingSource,
  type PdfReadingPosition,
} from '@/common/adapter/pdfReadingContext';

export type PdfReadingAttachment = { name: string; size?: number; contentType?: string };
export type PdfReadingSourcePosition = PdfReadingPosition & { source: PdfReadingSource };

/** This is only a suggestion. The server inspects the immutable uploaded PDF. */
export function suggestPdfReading(attachments: readonly PdfReadingAttachment[], available = MAX_READING_PDFS): boolean {
  return (
    attachments.length > 0 &&
    attachments.length <= available &&
    attachments.every(
      (file) =>
        (file.name.toLowerCase().endsWith('.pdf') ||
          file.contentType?.split(';')[0].toLowerCase() === 'application/pdf') &&
        (file.size === undefined || file.size <= MAX_READING_PDF_BYTES)
    )
  );
}

export function decodePdfReadingSources(value: unknown): PdfReadingSource[] {
  if (!Array.isArray(value) || value.length < 1 || value.length > MAX_READING_PDFS)
    throw new Error('Invalid PDF reading draft');
  const sources = value.map(decodePdfReadingSource);
  if (new Set(sources.map(pdfReadingSourceKey)).size !== sources.length) throw new Error('Duplicate PDF reading draft');
  return sources;
}

/** The existing initial-message handoff retains the original request. Once a
 * selection is prepared, a lost message ACK never reads a newer binding head. */
export async function prepareInitialPdfReading(
  initial: Record<string, unknown>,
  ownerId: string,
  prepare:
    | ((sources: PdfReadingSource[], position?: PdfReadingSourcePosition) => Promise<PdfReadingSelection | undefined>)
    | undefined,
  isCurrent: () => boolean,
  persist: (value: Record<string, unknown>) => void
): Promise<PdfReadingSelection | undefined> {
  if (initial.pdf_reading_owner_id !== undefined && initial.pdf_reading_owner_id !== ownerId) {
    throw new Error('PDF reading draft owner changed');
  }
  if (initial.reading_context !== undefined) return decodePdfReadingSelection(initial.reading_context);
  if (initial.pdf_reading_prepared === true || initial.pdf_reading_sources === undefined) return undefined;
  if (!prepare) throw new Error('PDF reading composer is unavailable');
  const sources = decodePdfReadingSources(initial.pdf_reading_sources);
  let position: PdfReadingSourcePosition | undefined;
  if (initial.pdf_reading_position !== undefined) {
    const raw = initial.pdf_reading_position as Partial<PdfReadingSourcePosition>;
    const source = decodePdfReadingSource(raw?.source);
    if (
      !Number.isSafeInteger(raw.pageNumber) ||
      !Number.isSafeInteger(raw.pageCount) ||
      Number(raw.pageNumber) < 1 ||
      Number(raw.pageCount) < 2 ||
      Number(raw.pageNumber) > Number(raw.pageCount) ||
      !sources.some((item) => samePdfReadingSource(item, source))
    )
      throw new Error('Invalid PDF reading draft position');
    position = { source, pageNumber: Number(raw.pageNumber), pageCount: Number(raw.pageCount) };
  }
  const selection = await prepare(sources, position);
  if (!isCurrent()) throw new Error('PDF reading draft owner changed');
  const next = {
    ...initial,
    pdf_reading_prepared: true,
    ...(selection ? { reading_context: decodePdfReadingSelection(selection) } : {}),
  };
  persist(next);
  Object.assign(initial, next);
  return selection;
}
