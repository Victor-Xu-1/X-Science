import { act, renderHook, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useGuidPdfReading } from '@/renderer/pages/guid/hooks/useGuidPdfReading';
import type { ComposerContextItem } from '@/renderer/components/chat/SendBox/composerCompositionModel';
import type { PdfReadingPreviewOwner } from '@/renderer/services/runtime/pdfReadingPreview';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const preview = vi.hoisted(() => ({
  owner: null as PdfReadingPreviewOwner | null,
  activeTab: { metadata: { projectId: 'project', versionId: 'version' } },
  register: vi.fn(),
}));
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  useOptionalPreviewContext: () => ({ activeTab: preview.activeTab, registerPdfReadingOwner: preview.register }),
}));
describe('C12 Guid PDF draft port', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    preview.owner = null;
    preview.register.mockImplementation((owner: PdfReadingPreviewOwner) => {
      preview.owner = owner;
      return () => {
        if (preview.owner === owner) preview.owner = null;
      };
    });
  });
  it('uses the existing draft for preview selection/page changes and unlink never deletes the attachment', async () => {
    const { result } = renderHook(() => {
      const [items, setItems] = useState<ComposerContextItem[]>([]);
      const [enabled, setEnabled] = useState(true);
      const position = useGuidPdfReading('owner', undefined, items, setItems, false, setEnabled);
      return { items, position: position.position, enabled };
    });
    await waitFor(() => expect(preview.owner).not.toBeNull());
    const source = { artifact_id: 'artifact', version_id: 'version' };
    await act(async () => {
      await preview.owner!.link(source, { name: 'original.pdf', position: { pageNumber: 3, pageCount: 9 } });
    });
    expect(result.current.items).toEqual([
      expect.objectContaining({
        artifactId: 'artifact',
        versionId: 'version',
        projectId: 'project',
        readingSelected: true,
      }),
    ]);
    expect(preview.owner!.context).toBeNull();
    act(() => preview.owner!.reportPosition(source, { pageNumber: 6, pageCount: 9 }));
    expect(result.current.position).toEqual({ source, pageNumber: 6, pageCount: 9 });
    await act(async () => {
      await preview.owner!.unlinkDraft!(source);
    });
    expect(result.current.items).toHaveLength(1);
    expect(result.current.items[0]).toMatchObject({ artifactId: 'artifact', versionId: 'version' });
    expect(result.current.items[0]).not.toHaveProperty('readingSelected', true);
    expect(result.current.enabled).toBe(false);
  });
  it('rejects old draft actions after account or owner replacement without a server mutation', async () => {
    const setItems = vi.fn();
    const setEnabled = vi.fn();
    const { rerender } = renderHook(
      ({ owner }) => useGuidPdfReading(owner, 'project', [], setItems, false, setEnabled),
      { initialProps: { owner: 'owner' } }
    );
    const old = preview.owner!;
    rerender({ owner: 'other' });
    await expect(
      old.link({ artifact_id: 'a', version_id: 'v' }, { name: 'paper.pdf', position: { pageNumber: 1, pageCount: 2 } })
    ).rejects.toThrow('draft changed');
    expect(setItems).not.toHaveBeenCalled();
  });
});
