import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadStructureContent } from '@/renderer/pages/conversation/Preview/components/viewers/structureSource';
import { ScientificPreviewError } from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewError';

const input = () => ({
  contentUrl: '/api/artifacts/structure/versions/source-v1',
  filename: 'complex.pdb',
  format: 'pdb',
  signal: new AbortController().signal,
});

afterEach(() => vi.unstubAllGlobals());

describe('structure source read errors', () => {
  it('classifies a rejected fetch as a read failure rather than corrupt structure data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(loadStructureContent(input())).rejects.toMatchObject({ code: 'request-failed' });
  });

  it('classifies a failed response-body read as a read failure', async () => {
    const response = new Response('partial structure');
    vi.spyOn(response, 'text').mockRejectedValue(new TypeError('Connection interrupted'));
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response));
    await expect(loadStructureContent(input())).rejects.toMatchObject({ code: 'request-failed' });
  });

  it('keeps an HTTP status and the original source limit as distinct typed errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(new Response('', { status: 503 }))
        .mockResolvedValueOnce(new Response('', { headers: { 'content-length': String(65 * 1024 * 1024) } }))
    );
    await expect(loadStructureContent(input())).rejects.toMatchObject({
      code: 'request-failed',
      details: { status: 503 },
    });
    await expect(loadStructureContent(input())).rejects.toMatchObject({
      code: 'too-large',
      details: { limit: '64 MB' },
    });
  });

  it('preserves cancellation rather than turning an aborted owner into a visible error', async () => {
    const controller = new AbortController();
    const reason = new DOMException('Cancelled', 'AbortError');
    controller.abort();
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(reason));
    await expect(loadStructureContent({ ...input(), signal: controller.signal })).rejects.toBe(reason);
  });

  it('keeps binary input byte-identical and sends the current owner signal', async () => {
    const bytes = new Uint8Array([0, 255, 1, 128]);
    const request = { ...input(), filename: 'structure.bcif', format: 'mmcif' };
    const read = vi.fn().mockResolvedValue(new Response(bytes));
    vi.stubGlobal('fetch', read);
    expect(new Uint8Array((await loadStructureContent(request)) as ArrayBuffer)).toEqual(bytes);
    expect(read).toHaveBeenCalledWith(request.contentUrl, expect.objectContaining({ signal: request.signal }));
  });

  it('does not invent network reads for inline or missing content', async () => {
    const read = vi.fn();
    vi.stubGlobal('fetch', read);
    expect(await loadStructureContent({ ...input(), contentUrl: undefined, content: 'inline original' })).toBe(
      'inline original'
    );
    await expect(loadStructureContent({ ...input(), contentUrl: undefined })).rejects.toBeInstanceOf(
      ScientificPreviewError
    );
    expect(read).not.toHaveBeenCalled();
  });
});
