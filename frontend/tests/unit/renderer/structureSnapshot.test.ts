import { afterEach, describe, expect, it, vi } from 'vitest';
import { saveStructureSnapshot } from '@/renderer/pages/conversation/Preview/components/viewers/structureSnapshot';

const png =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';

afterEach(() => vi.unstubAllGlobals());

describe('native structure snapshot persistence', () => {
  it('branches from the immutable source using the existing authenticated binary API', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ artifact_id: 'image', version_id: 'image-v1' }), { status: 201 })
      );
    vi.stubGlobal('fetch', fetchMock);
    await expect(
      saveStructureSnapshot('/api/artifacts/structure/versions/structure-v1', 'complex.pdb', png)
    ).resolves.toEqual({ artifact_id: 'image', version_id: 'image-v1' });
    const [path, request] = fetchMock.mock.calls[0];
    expect(path).toContain('/api/artifacts/structure/versions/binary?');
    expect(path).toContain('parent_version_id=structure-v1');
    expect(path).toContain('branch_as_filename=complex-snapshot.png');
    expect(request.credentials).toBe('include');
    const file = request.body.get('file') as File;
    expect(file.type).toBe('image/png');
    expect(file.size).toBeGreaterThan(8);
  });

  it('rejects mutable sources and non-rendered bytes without writing', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    await expect(saveStructureSnapshot('/api/artifacts/structure', 'complex.pdb', png)).rejects.toThrow(
      'VERSION_REQUIRED'
    );
    await expect(
      saveStructureSnapshot('/api/artifacts/structure/versions/v1', 'complex.pdb', 'data:image/png;base64,YmFk')
    ).rejects.toThrow('PNG_REQUIRED');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('does not disguise a failed save as a successful download', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 403 })));
    await expect(saveStructureSnapshot('/api/artifacts/structure/versions/v1', 'complex.pdb', png)).rejects.toThrow();
  });
});
