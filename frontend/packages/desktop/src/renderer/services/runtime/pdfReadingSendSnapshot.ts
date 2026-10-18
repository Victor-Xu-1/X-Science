import {
  pdfReadingExpected,
  samePdfReadingScope,
  type PdfReadingContext,
  type PdfReadingPosition,
  type PdfReadingSelection,
  type PdfReadingSource,
} from '@/common/adapter/pdfReadingContext';

/** A click-time value, never a binding authority or another composer store. */
export type PdfReadingSendSnapshot = Readonly<{
  context: PdfReadingContext | null;
  activeVersionId?: string;
  positions: ReadonlyMap<string, Readonly<PdfReadingPosition>>;
}>;

export class PdfReadingSelectionChangedError extends Error {
  constructor() {
    super('PDF reading selection changed; review the original message before sending');
  }
}

export function unlinkedPdfReadingSources(context: PdfReadingContext, sources: PdfReadingSource[]): PdfReadingSource[] {
  return sources.filter(
    (source) =>
      !context.bindings.some(
        (binding) => binding.artifact_id === source.artifact_id && binding.version_id === source.version_id
      )
  );
}

export function validPdfReadingPosition(position: PdfReadingPosition): boolean {
  return (
    Number.isSafeInteger(position.pageNumber) &&
    Number.isSafeInteger(position.pageCount) &&
    position.pageNumber >= 1 &&
    position.pageCount >= 1 &&
    position.pageNumber <= position.pageCount
  );
}

export function capturePdfReadingSend(
  context: PdfReadingContext | null,
  activeVersionId: string | undefined,
  positions: ReadonlyMap<string, PdfReadingPosition>
): PdfReadingSendSnapshot {
  return {
    context: context ? structuredClone(context) : null,
    activeVersionId,
    positions: new Map([...positions].map(([id, position]) => [id, { ...position }])),
  };
}

export function requirePdfReadingContext(actual: PdfReadingContext | null, expected: PdfReadingContext): void {
  if (
    !actual ||
    !samePdfReadingScope(actual.scope, expected.scope) ||
    actual.revision !== expected.revision ||
    actual.bindings.length !== expected.bindings.length ||
    actual.bindings.some((binding, index) => {
      const original = expected.bindings[index];
      return (
        binding.binding_id !== original.binding_id ||
        binding.artifact_id !== original.artifact_id ||
        binding.version_id !== original.version_id
      );
    })
  )
    throw new PdfReadingSelectionChangedError();
}

export function selectionFromPdfReadingSend(
  snapshot: PdfReadingSendSnapshot,
  context = snapshot.context
): PdfReadingSelection | undefined {
  if (!context?.bindings.length) return undefined;
  const active =
    context.bindings.find((binding) => binding.version_id === snapshot.activeVersionId) ?? context.bindings[0];
  const position = snapshot.positions.get(active.version_id);
  return {
    schema: 'x-science.pdf-reading-selection.v1',
    ...pdfReadingExpected(context),
    binding_ids: context.bindings.map((binding) => binding.binding_id),
    active_binding_id: active.binding_id,
    ...(position && validPdfReadingPosition(position) && position.pageCount === active.page_count
      ? { page_number: position.pageNumber }
      : {}),
  };
}
