import type { PdfReadingBinding, PdfReadingContext, PdfReadingSelection } from '@/common/adapter/pdfReadingContext';
import { samePdfReadingPending, type PdfReadingPendingMutation } from '@/common/adapter/pdfReadingContext';
import type { PdfReadingPendingAccess } from '@/renderer/services/runtime/pdfReadingPendingState';

/** Explicit persistence dependency for controller units; real draft DOM tests own storage behavior. */
export function pdfPendingMemory(initial?: PdfReadingPendingMutation) {
  let current = initial;
  const writes: Array<[PdfReadingPendingMutation | undefined, PdfReadingPendingMutation | undefined]> = [];
  const access: PdfReadingPendingAccess = (pending, expected) => {
    if (!pending && !expected) return { status: 'current', pending: current };
    if (current && !samePdfReadingPending(current, expected ?? pending))
      return { status: 'conflict', pending: current };
    if ((!pending && !current) || samePdfReadingPending(current, pending))
      return { status: 'applied', pending: current };
    writes.push([pending, expected]);
    current = pending;
    return { status: 'applied', pending: current };
  };
  return {
    access,
    writes,
    set: (pending: PdfReadingPendingMutation | undefined) => {
      current = pending;
    },
  };
}

export const pdfBinding = (suffix = '1'): PdfReadingBinding => ({
  binding_id: `binding-${suffix}`,
  artifact_id: `artifact-${suffix}`,
  version_id: `version-${suffix}`,
  source_kind: 'artifact-version',
  name: `paper-${suffix}.pdf`,
  mime_type: 'application/pdf',
  size_bytes: 512,
  checksum: 'a'.repeat(64),
  page_count: 9,
  linked_at: '2026-10-18T01:02:03Z',
});
export const pdfContext = (bindings = [pdfBinding()], revision = 1): PdfReadingContext => ({
  schema: 'x-science.pdf-reading-context.v1',
  scope: {
    frame_id: 'frame',
    frame_incarnation_id: 'frame-inc',
    root_frame_id: 'root',
    root_frame_incarnation_id: 'root-inc',
    project_id: 'project',
  },
  revision,
  bindings,
  changed: false,
});
export const pdfSelection = (): PdfReadingSelection => ({
  schema: 'x-science.pdf-reading-selection.v1',
  expected_frame_incarnation_id: 'frame-inc',
  expected_root_frame_id: 'root',
  expected_root_frame_incarnation_id: 'root-inc',
  expected_revision: 1,
  binding_ids: ['binding-1'],
  active_binding_id: 'binding-1',
  page_number: 4,
});
export const deferredPdf = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
