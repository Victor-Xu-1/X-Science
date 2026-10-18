import { beforeEach, describe, expect, it, vi } from 'vitest';
import { loadProjectReadingOptions, mutatePdfReadingContext } from '@/renderer/services/runtime/pdfReadingContext';
import { pdfReadingExpected } from '@/common/adapter/pdfReadingContext';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { pdfBinding, pdfContext, deferredPdf } from './pdfReadingFixtures';

const mocks = vi.hoisted(() => ({ http: vi.fn(), artifacts: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: mocks.http }));
vi.mock('@/renderer/services/xScienceGateway', () => ({ loadXScienceProjectArtifacts: mocks.artifacts }));

describe('C12 PDF reading transport', () => {
  beforeEach(() => {
    mocks.http.mockReset();
    mocks.artifacts.mockReset();
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
  });
  it('inspects real immutable Project candidates in bounded batches of 100 without inventing Library sources', async () => {
    mocks.artifacts.mockResolvedValue(
      Array.from({ length: 201 }, (_, index) => ({
        artifactId: `artifact-${index}`,
        versionId: `version-${index}`,
        projectId: 'project',
        filename: `paper-${index}.pdf`,
        sizeBytes: 512,
      }))
    );
    mocks.http.mockImplementation(async (_method, _path, body) => ({
      schema: 'x-science.pdf-reading-candidates.v1',
      scope: pdfContext().scope,
      revision: 1,
      candidates: body.sources.map((source: unknown) => ({
        source,
        eligible: true,
        page_count: 2,
        size_bytes: 512,
        checksum: 'a'.repeat(64),
      })),
    }));
    const result = await loadProjectReadingOptions(pdfContext(), new AbortController().signal);
    expect(result.options).toHaveLength(201);
    expect(mocks.http.mock.calls.map((call) => call[2].sources.length)).toEqual([100, 100, 1]);
    expect(mocks.artifacts).toHaveBeenCalledWith('project', { signal: expect.any(AbortSignal) });
    expect(
      mocks.http.mock.calls.every((call) => call[1] === '/api/conversations/frame/reading-context/candidates')
    ).toBe(true);
  });
  it('rejects a response that substituted a different immutable version or a different scope', async () => {
    const request = {
      schema: 'x-science.pdf-reading-link.v1' as const,
      operation_id: '00000000-0000-4000-8000-000000000001',
      ...pdfReadingExpected(pdfContext([], 0)),
      sources: [{ artifact_id: 'artifact-1', version_id: 'version-1' }],
    };
    mocks.http.mockResolvedValue({ ...pdfContext([pdfBinding('2')]), changed: true });
    await expect(mutatePdfReadingContext('frame', request)).rejects.toThrow('not acknowledged');
    mocks.http.mockResolvedValue({
      ...pdfContext(),
      scope: { ...pdfContext().scope, root_frame_incarnation_id: 'replacement' },
    });
    await expect(mutatePdfReadingContext('frame', request)).rejects.toThrow('acknowledgement changed');
    expect(mocks.http.mock.calls[0][2]).toEqual(request);
  });
  it('does not apply a successful HTTP response after the authenticated account changed', async () => {
    const pending = deferredPdf<unknown>();
    mocks.http.mockReturnValue(pending.promise);
    const request = {
      schema: 'x-science.pdf-reading-unlink.v1' as const,
      operation_id: '00000000-0000-4000-8000-000000000001',
      ...pdfReadingExpected(pdfContext()),
      binding_id: 'binding-1',
    };
    const action = mutatePdfReadingContext('frame', request);
    setRendererAccountOwner('other');
    pending.resolve({ ...pdfContext([], 2), changed: true });
    await expect(action).rejects.toThrow('account changed');
  });
});
