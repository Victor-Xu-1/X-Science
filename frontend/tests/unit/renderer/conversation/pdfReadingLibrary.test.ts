import { describe, expect, it } from 'vitest';
import {
  decodePdfReadingBinding,
  decodePdfReadingContext,
  decodePdfReadingMutation,
  decodePdfReadingSource,
  pdfReadingExpected,
  pdfReadingSourceKey,
} from '@/common/adapter/pdfReadingContext';
import { boundPdfPreview } from '@/renderer/services/runtime/pdfReadingPreview';
import { capturePdfReadingSend, selectionFromPdfReadingSend } from '@/renderer/services/runtime/pdfReadingSendSnapshot';
import { pdfBinding, pdfContext } from './pdfReadingFixtures';

const librarySource = {
  source_kind: 'literature-attachment-version',
  item_id: 'paper-item',
  attachment_id: 'paper-attachment',
  version_id: 'version-1',
} as const;
const libraryBinding = () => {
  const { artifact_id: _artifact, ...binding } = pdfBinding();
  return { ...binding, ...librarySource, binding_id: 'library-binding' };
};

describe('L382 independent Library reading wire', () => {
  it('preserves real Library identity through candidate, binding and immutable mutation decoders', () => {
    expect(decodePdfReadingSource(librarySource)).toEqual(librarySource);
    expect(decodePdfReadingBinding(libraryBinding())).toEqual(libraryBinding());
    const request = {
      schema: 'x-science.pdf-reading-link.v1',
      operation_id: '00000000-0000-4000-8000-000000000001',
      ...pdfReadingExpected(pdfContext()),
      sources: [librarySource],
    };
    expect(decodePdfReadingMutation(request)).toEqual(request);
  });
  it('keeps source namespaces separate while rejecting mixed or conflicting ownership identities', () => {
    for (const source of [
      { ...librarySource, artifact_id: 'forged-artifact' },
      { ...librarySource, item_id: '' },
      { ...librarySource, attachment_id: undefined as unknown },
      { artifact_id: 'artifact-1', version_id: 'version-1', item_id: 'forged-item' },
      { artifact_id: 'artifact-1', version_id: 'version-1', source_kind: 'unknown' },
    ]) {
      expect(() => decodePdfReadingSource(source)).toThrow('Invalid PDF reading contract');
    }
    const context = { ...pdfContext(), bindings: [pdfBinding(), libraryBinding()] };
    expect(decodePdfReadingContext(context).bindings).toHaveLength(2);
    expect(() =>
      decodePdfReadingContext({
        ...context,
        bindings: [libraryBinding(), { ...libraryBinding(), binding_id: 'other' }],
      })
    ).toThrow('Invalid PDF reading contract');
  });
  it('opens the original independent version with its frame proof and preserves its own page position', () => {
    const context = decodePdfReadingContext({ ...pdfContext(), bindings: [pdfBinding(), libraryBinding()] });
    const library = context.bindings[1];
    const target = boundPdfPreview(library, context, 4);
    expect(target.content).toBe('/api/literature/attachment-versions/version-1/content?frame_id=frame');
    expect(target.metadata).not.toHaveProperty('artifactId');
    expect(target.metadata.pdfReadingSource).toEqual(librarySource);
    const snapshot = capturePdfReadingSend(
      context,
      pdfReadingSourceKey(library),
      new Map([
        [pdfReadingSourceKey(context.bindings[0]), { pageNumber: 2, pageCount: 9 }],
        [pdfReadingSourceKey(library), { pageNumber: 4, pageCount: 9 }],
      ])
    );
    expect(selectionFromPdfReadingSend(snapshot)).toMatchObject({
      active_binding_id: 'library-binding',
      page_number: 4,
    });
  });
});
