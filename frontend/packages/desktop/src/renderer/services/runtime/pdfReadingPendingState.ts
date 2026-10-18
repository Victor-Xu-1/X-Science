import {
  decodePdfReadingPending,
  samePdfReadingPending,
  type PdfReadingPendingMutation,
  type PdfReadingScope,
} from '@/common/adapter/pdfReadingContext';

/** Feedback from the existing durable draft owner; never a server receipt. */
export type PdfReadingPendingResult =
  | { status: 'current' | 'applied' | 'conflict'; pending?: PdfReadingPendingMutation }
  | { status: 'unavailable' };

/** No arguments: observe. New pending: reserve. Expected: settle that exact request. */
export type PdfReadingPendingAccess = (
  pending?: PdfReadingPendingMutation,
  expected?: PdfReadingPendingMutation
) => PdfReadingPendingResult;

export type PdfReadingPendingProjection = {
  pending: PdfReadingPendingMutation | null;
  blocked: boolean;
  error: 'load' | 'uncertain' | 'conflict' | 'mutation' | null;
};

export class PdfReadingPendingError extends Error {
  readonly code: 'PDF_READING_DRAFT_UNAVAILABLE' | 'PDF_READING_DRAFT_CONFLICT';
  constructor(unavailable: boolean) {
    super(
      unavailable ? 'PDF reading recovery could not be persisted' : 'Resolve the current PDF reading request first'
    );
    this.code = unavailable ? 'PDF_READING_DRAFT_UNAVAILABLE' : 'PDF_READING_DRAFT_CONFLICT';
  }
}

export function pendingMatchesComposer(
  pending: PdfReadingPendingMutation,
  owner: { ownerId: string; frameId: string; frameIncarnationId?: string },
  scope?: PdfReadingScope
): boolean {
  return (
    pending.ownerId === owner.ownerId &&
    pending.frameId === owner.frameId &&
    (!owner.frameIncarnationId || pending.request.expected_frame_incarnation_id === owner.frameIncarnationId) &&
    (!scope ||
      (pending.request.expected_frame_incarnation_id === scope.frame_incarnation_id &&
        pending.request.expected_root_frame_id === scope.root_frame_id &&
        pending.request.expected_root_frame_incarnation_id === scope.root_frame_incarnation_id))
  );
}

/** Pure UI projection of owner feedback; it reads/writes no storage. */
export function projectPdfReadingPending(
  result: PdfReadingPendingResult,
  current: PdfReadingPendingProjection,
  owner: Parameters<typeof pendingMatchesComposer>[1],
  scope?: PdfReadingScope,
  fallback?: PdfReadingPendingMutation
): PdfReadingPendingProjection {
  if (!result || result.status === 'unavailable')
    return { pending: fallback ?? current.pending, blocked: true, error: 'mutation' };
  const pending = decodePdfReadingPending(result.pending);
  if (result.pending && (!pending || !pendingMatchesComposer(pending, owner, scope)))
    return { pending: null, blocked: true, error: 'conflict' };
  const same = samePdfReadingPending(current.pending, pending);
  return {
    pending: same ? current.pending : (pending ?? null),
    blocked: false,
    error: pending ? (same ? (current.error ?? 'uncertain') : 'uncertain') : null,
  };
}
