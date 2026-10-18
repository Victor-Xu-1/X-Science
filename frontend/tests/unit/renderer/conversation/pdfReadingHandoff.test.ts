import { describe, expect, it, vi } from 'vitest';
import { prepareInitialPdfReading, suggestPdfReading } from '@/renderer/services/runtime/pdfReadingHandoff';
import { decodePdfReadingProjection, decodePdfReadingSelection } from '@/common/adapter/pdfReadingContext';
import { pdfContext, pdfSelection, deferredPdf } from './pdfReadingFixtures';

describe('C12 reading handoff and immutable evidence', () => {
  it('stores selection before first message POST and retries without consulting a changed binding head', async () => {
    const original: Record<string, unknown> = {
      loading_id: 'original-message',
      input: 'this page',
      pdf_reading_owner_id: 'owner',
      pdf_reading_sources: [{ artifact_id: 'artifact-1', version_id: 'version-1' }],
    };
    const prepare = vi.fn().mockResolvedValue(pdfSelection());
    const persist = vi.fn();
    expect(await prepareInitialPdfReading(original, 'owner', prepare, () => true, persist)).toEqual(pdfSelection());
    expect(persist).toHaveBeenCalledWith(
      expect.objectContaining({
        loading_id: 'original-message',
        pdf_reading_prepared: true,
        reading_context: pdfSelection(),
      })
    );
    prepare.mockRejectedValue(new Error('unlinked after acceptance'));
    expect(await prepareInitialPdfReading(structuredClone(original), 'owner', prepare, () => true, persist)).toEqual(
      pdfSelection()
    );
    expect(prepare).toHaveBeenCalledTimes(1);
  });
  it('does not persist a late preparation or treat a foreign draft as a current request', async () => {
    const pending = deferredPdf<ReturnType<typeof pdfSelection>>();
    let current = true;
    const persist = vi.fn();
    const original = { pdf_reading_owner_id: 'owner', pdf_reading_sources: [{ artifact_id: 'a', version_id: 'v' }] };
    const action = prepareInitialPdfReading(
      original,
      'owner',
      () => pending.promise,
      () => current,
      persist
    );
    current = false;
    pending.resolve(pdfSelection());
    await expect(action).rejects.toThrow('owner changed');
    expect(persist).not.toHaveBeenCalled();
    await expect(prepareInitialPdfReading(original, 'other', vi.fn(), () => true, persist)).rejects.toThrow(
      'owner changed'
    );
  });
  it('suggests only bounded PDF attachments without claiming they have been parsed', () => {
    expect(suggestPdfReading([{ name: 'paper.pdf', size: 512 }])).toBe(true);
    expect(suggestPdfReading([{ name: 'paper.pdf', size: 50 * 1024 * 1024 + 1 }])).toBe(false);
    expect(suggestPdfReading([{ name: 'paper.pdf' }, { name: 'data.csv' }])).toBe(false);
    expect(suggestPdfReading(Array.from({ length: 4 }, () => ({ name: 'paper.pdf' })))).toBe(false);
    expect(suggestPdfReading([{ name: 'paper.pdf' }], 0)).toBe(false);
  });
  it('retains historical original identities and flags malformed evidence without dropping message coordinates', () => {
    const snapshot = {
      ...pdfContext(),
      schema: 'x-science.pdf-reading-snapshot.v1',
      active_binding_id: 'binding-1',
      page_number: 4,
    };
    expect(decodePdfReadingProjection({ pdf_reading_context: snapshot }).pdf_reading_context).toMatchObject({
      page_number: 4,
      bindings: [{ version_id: 'version-1' }],
    });
    expect(decodePdfReadingProjection({ pdf_reading_context: { ...snapshot, page_number: 99 } })).toEqual({
      pdf_reading_context_unavailable: true,
    });
    expect(() => decodePdfReadingSelection({ ...pdfSelection(), binding_ids: ['binding-1', 'binding-1'] })).toThrow();
  });
});
