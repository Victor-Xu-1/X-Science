import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PdfReadingController } from '@/renderer/services/runtime/PdfReadingController';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { pdfBinding, pdfContext, deferredPdf } from './pdfReadingFixtures';
import type { PdfReadingContext, PdfReadingPendingMutation } from '@/common/adapter/pdfReadingContext';

describe('C12 PdfReadingController', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
  });
  const fixture = (initial = pdfContext()) => {
    const persist = vi.fn();
    const load = vi.fn().mockResolvedValue(initial);
    const mutate = vi.fn();
    const inspect = vi.fn();
    const controller = new PdfReadingController({
      ownerId: 'owner',
      frameId: 'frame',
      frameIncarnationId: 'frame-inc',
      persistPending: persist,
      load,
      mutate,
      inspect,
    });
    return { controller, persist, load, mutate, inspect };
  };
  it('keeps a lost link ACK as an exact draft request across disposal and never resurrects a later unlink', async () => {
    const first = fixture(pdfContext([], 0));
    await first.controller.load();
    first.mutate.mockRejectedValue(new Error('lost acknowledgement'));
    await expect(first.controller.link([{ artifact_id: 'artifact-1', version_id: 'version-1' }])).rejects.toThrow();
    const pending = first.persist.mock.calls[0][0] as PdfReadingPendingMutation;
    first.controller.dispose();
    const next = fixture(pdfContext([], 2));
    next.controller.restorePending(pending);
    await next.controller.load();
    next.mutate.mockRejectedValue(
      new BackendHttpError({
        method: 'POST',
        path: '/reading-context/link',
        status: 409,
        body: { code: 'PDF_READING_REVISION_STALE', message: 'changed' },
      })
    );
    await expect(next.controller.retry()).rejects.toThrow();
    expect(next.mutate).toHaveBeenCalledWith('frame', pending.request);
    expect(next.controller.getSnapshot().context?.bindings).toEqual([]);
    expect(next.controller.getSnapshot().error).toBe('conflict');
    expect(next.persist).not.toHaveBeenCalled();
    await next.controller.refresh();
    expect(next.persist).toHaveBeenCalledWith(undefined);
  });
  it('freezes the selected version and actual page while later unlink only changes future sends', async () => {
    const f = fixture();
    await f.controller.load();
    f.controller.reportPosition(
      { artifact_id: 'artifact-1', version_id: 'version-1' },
      { pageNumber: 4, pageCount: 9 }
    );
    const captured = f.controller.capture('version-1');
    f.mutate.mockResolvedValue({ ...pdfContext([], 2), changed: true });
    await f.controller.unlink('binding-1');
    expect(captured).toMatchObject({ expected_revision: 1, binding_ids: ['binding-1'], page_number: 4 });
    expect(f.controller.capture()).toBeUndefined();
    expect(f.mutate.mock.calls[0][1]).not.toHaveProperty('delete_file');
  });
  it('persists once before first POST and reuses the same request through unknown retries until confirmed success', async () => {
    const f = fixture(pdfContext([], 0));
    await f.controller.load();
    f.mutate.mockImplementation(async () => {
      expect(f.persist).toHaveBeenCalledTimes(1);
      if (f.mutate.mock.calls.length < 3) throw new Error('acknowledgement unavailable');
      return { ...pdfContext(), changed: true };
    });
    await expect(f.controller.link([{ artifact_id: 'artifact-1', version_id: 'version-1' }])).rejects.toThrow(
      'acknowledgement unavailable'
    );
    const pending = f.controller.getSnapshot().pending!;
    const originalRequest = f.mutate.mock.calls[0][1];
    expect(f.persist.mock.calls[0][0]).toBe(pending);
    await expect(f.controller.retry()).rejects.toThrow('acknowledgement unavailable');
    expect(f.controller.getSnapshot().pending).toBe(pending);
    expect(f.persist).toHaveBeenCalledTimes(1);
    await f.controller.retry();
    expect(f.mutate).toHaveBeenCalledTimes(3);
    expect(f.mutate.mock.calls[1][1]).toBe(originalRequest);
    expect(f.mutate.mock.calls[2][1]).toBe(originalRequest);
    expect(f.persist.mock.calls).toEqual([[pending], [undefined]]);
    expect(f.controller.getSnapshot()).toMatchObject({
      pending: null,
      error: null,
      context: { ...pdfContext(), changed: true },
    });
  });
  it('fences late account ABA, unmount ACK and replacement frame incarnation', async () => {
    const f = fixture(pdfContext([], 0));
    await f.controller.load();
    const late = deferredPdf<PdfReadingContext>();
    f.mutate.mockReturnValue(late.promise);
    const operation = f.controller.link([{ artifact_id: 'artifact-1', version_id: 'version-1' }]);
    setRendererAccountOwner('other');
    setRendererAccountOwner('owner');
    late.resolve({ ...pdfContext(), changed: true });
    await expect(operation).rejects.toThrow('scope changed');
    expect(f.persist).toHaveBeenCalledTimes(1);
    expect(f.controller.getSnapshot().context?.bindings).toEqual([]);
    const replacement = fixture({
      ...pdfContext(),
      scope: { ...pdfContext().scope, frame_incarnation_id: 'replacement' },
    });
    await expect(replacement.controller.load()).rejects.toThrow('scope changed');
  });
  it('single-page uploads remain ordinary attachments and unavailable inspection never silently links', async () => {
    const f = fixture(pdfContext([], 0));
    const source = { artifact_id: 'artifact-1', version_id: 'version-1' };
    f.inspect.mockResolvedValue({
      scope: pdfContext().scope,
      candidates: [{ source, eligible: false, reason: 'single_page' }],
    });
    await f.controller.prepareSources([source]);
    expect(f.mutate).not.toHaveBeenCalled();
    f.inspect.mockResolvedValue({
      scope: pdfContext().scope,
      candidates: [{ source, eligible: false, reason: 'inspection_unavailable' }],
    });
    await expect(f.controller.prepareSources([source])).rejects.toThrow('inspection is unavailable');
    expect(f.mutate).not.toHaveBeenCalled();
  });
  it('keeps earlier confirmed bindings and does not partially link a later multi-source batch when one inspection fails', async () => {
    const f = fixture();
    await f.controller.load();
    const sources = [
      { artifact_id: 'artifact-2', version_id: 'version-2' },
      { artifact_id: 'artifact-3', version_id: 'version-3' },
    ];
    f.inspect.mockResolvedValue({
      scope: pdfContext().scope,
      candidates: [
        { source: sources[0], eligible: true, page_count: 9 },
        { source: sources[1], eligible: false, reason: 'inspection_unavailable' },
      ],
    });
    await expect(f.controller.prepareSources(sources)).rejects.toThrow('inspection is unavailable');
    expect(f.mutate).not.toHaveBeenCalled();
    expect(f.controller.getSnapshot().context).toEqual(pdfContext());
    expect(f.controller.getSnapshot().pending).toBeNull();
  });
  it('links only the inspected immutable upload and prevents duplicate mutation clicks', async () => {
    const f = fixture(pdfContext([], 0));
    await f.controller.load();
    const source = { artifact_id: 'artifact-1', version_id: 'version-1' };
    const late = deferredPdf<PdfReadingContext>();
    f.mutate.mockReturnValue(late.promise);
    f.inspect.mockResolvedValue({ scope: pdfContext().scope, candidates: [{ source, eligible: true, page_count: 9 }] });
    const preparing = f.controller.prepareSources([source]);
    await vi.waitFor(() => expect(f.mutate).toHaveBeenCalledTimes(1));
    await expect(f.controller.link([{ artifact_id: 'artifact-2', version_id: 'version-2' }])).rejects.toThrow(
      'original'
    );
    expect(f.mutate.mock.calls[0][1].sources).toEqual([source]);
    late.resolve({ ...pdfContext([pdfBinding()]), changed: true });
    await preparing;
    expect(f.controller.capture()?.binding_ids).toEqual(['binding-1']);
  });
});
