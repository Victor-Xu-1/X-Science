import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PdfReadingController } from '@/renderer/services/runtime/PdfReadingController';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { pdfBinding, pdfContext, deferredPdf, pdfPendingMemory } from './pdfReadingFixtures';
import type { PdfReadingContext, PdfReadingPendingMutation } from '@/common/adapter/pdfReadingContext';

describe('C12 PdfReadingController', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
  });
  const fixture = (initial = pdfContext()) => {
    const pendingMemory = pdfPendingMemory();
    const persist = vi.fn(pendingMemory.access);
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
    return { controller, persist, pendingMemory, load, mutate, inspect };
  };
  it('keeps a lost link ACK as an exact draft request across disposal and never resurrects a later unlink', async () => {
    const first = fixture(pdfContext([], 0));
    await first.controller.load();
    first.mutate.mockRejectedValue(new Error('lost acknowledgement'));
    await expect(first.controller.link([{ artifact_id: 'artifact-1', version_id: 'version-1' }])).rejects.toThrow();
    const pending = first.persist.mock.calls[0][0] as PdfReadingPendingMutation;
    first.controller.dispose();
    const next = fixture(pdfContext([], 2));
    next.pendingMemory.set(pending);
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
    expect(next.pendingMemory.writes).toEqual([]);
    expect(next.persist.mock.calls.every((args) => args.length === 0)).toBe(true);
    await next.controller.refresh();
    expect(next.persist).toHaveBeenCalledWith(undefined, pending);
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
      expect(f.pendingMemory.writes).toHaveLength(1);
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
    expect(f.pendingMemory.writes).toHaveLength(1);
    await f.controller.retry();
    expect(f.mutate).toHaveBeenCalledTimes(3);
    expect(f.mutate.mock.calls[1][1]).toBe(originalRequest);
    expect(f.mutate.mock.calls[2][1]).toBe(originalRequest);
    expect(f.persist.mock.calls.filter(([next, expected]) => next !== undefined || expected !== undefined)).toEqual([
      [pending],
      [undefined, pending],
    ]);
    expect(f.pendingMemory.writes).toEqual([
      [pending, undefined],
      [undefined, pending],
    ]);
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
  it('rejects foreign or stale-incarnation pending authority before transport', async () => {
    const original = fixture();
    await original.controller.load();
    original.mutate.mockRejectedValue(new Error('unknown ACK'));
    await expect(original.controller.unlink('binding-1')).rejects.toThrow();
    const pending = original.controller.getSnapshot().pending!;
    for (const stale of [
      { ...pending, ownerId: 'foreign' },
      { ...pending, request: { ...pending.request, expected_frame_incarnation_id: 'old-frame' } },
      { ...pending, request: { ...pending.request, expected_root_frame_incarnation_id: 'old-root' } },
    ]) {
      const next = fixture();
      await next.controller.load();
      next.pendingMemory.set(stale);
      next.controller.restorePending(stale);
      expect(next.controller.getSnapshot()).toMatchObject({ blocked: true, pending: null, error: 'conflict' });
      await expect(next.controller.link([{ artifact_id: 'artifact-2', version_id: 'version-2' }])).rejects.toThrow();
      expect(next.mutate).not.toHaveBeenCalled();
      expect(next.pendingMemory.writes).toEqual([]);
      expect(next.pendingMemory.access()).toEqual({ status: 'current', pending: stale });
    }
  });
  it('maps the clicked active unlinked PDF and page only through its own confirmed link', async () => {
    const f = fixture();
    await f.controller.load();
    const source = { artifact_id: 'artifact-2', version_id: 'version-2' };
    f.controller.reportPosition(source, { pageNumber: 4, pageCount: 9 });
    const snapshot = f.controller.captureSend(source.version_id);
    const inspected = deferredPdf<{
      scope: PdfReadingContext['scope'];
      candidates: Array<{ source: typeof source; eligible: boolean }>;
    }>();
    f.inspect.mockReturnValue(inspected.promise);
    f.mutate.mockResolvedValue({ ...pdfContext([pdfBinding(), pdfBinding('2')], 2), changed: true });
    const preparing = f.controller.prepareSend(snapshot, [source]);
    await vi.waitFor(() => expect(f.inspect).toHaveBeenCalledOnce());
    f.controller.reportPosition(source, { pageNumber: 8, pageCount: 9 });
    inspected.resolve({ scope: pdfContext().scope, candidates: [{ source, eligible: true }] });
    expect(await preparing).toMatchObject({
      expected_revision: 2,
      binding_ids: ['binding-1', 'binding-2'],
      active_binding_id: 'binding-2',
      page_number: 4,
    });
    expect(f.mutate.mock.calls[0][1]).toMatchObject({ expected_revision: 1, sources: [source] });
    expect(snapshot.context?.revision).toBe(1);
  });
  it('rejects external unlink during preparation instead of replacing the frozen selection', async () => {
    const f = fixture();
    await f.controller.load();
    const source = { artifact_id: 'artifact-2', version_id: 'version-2' };
    const snapshot = f.controller.captureSend('version-1');
    const inspected = deferredPdf<{
      scope: PdfReadingContext['scope'];
      candidates: Array<{ source: typeof source; eligible: boolean }>;
    }>();
    f.inspect.mockReturnValue(inspected.promise);
    const preparing = f.controller.prepareSend(snapshot, [source]);
    const rejected = expect(preparing).rejects.toThrow('selection changed');
    await vi.waitFor(() => expect(f.inspect).toHaveBeenCalledOnce());
    f.mutate.mockResolvedValue({ ...pdfContext([], 2), changed: true });
    await f.controller.unlink('binding-1');
    inspected.resolve({ scope: pdfContext().scope, candidates: [{ source, eligible: true }] });
    await rejected;
    expect(f.mutate).toHaveBeenCalledOnce();
    expect(f.mutate.mock.calls[0][1].schema).toBe('x-science.pdf-reading-unlink.v1');
    expect(snapshot.context?.bindings).toEqual([pdfBinding()]);
    expect(f.controller.getSnapshot().error).toBe('conflict');
  });
  it('does not add reading bindings when optional bootstrap finishes after the send capture', async () => {
    const f = fixture();
    const bootstrap = deferredPdf<PdfReadingContext>();
    f.load.mockReturnValue(bootstrap.promise);
    const loading = f.controller.load();
    const snapshot = f.controller.captureSend('version-1');
    const selection = f.controller.prepareSend(snapshot, []);
    bootstrap.resolve(pdfContext());
    await loading;
    expect(await selection).toBeUndefined();
    expect(snapshot.context).toBeNull();
    expect(f.inspect).not.toHaveBeenCalled();
    expect(f.mutate).not.toHaveBeenCalled();
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
