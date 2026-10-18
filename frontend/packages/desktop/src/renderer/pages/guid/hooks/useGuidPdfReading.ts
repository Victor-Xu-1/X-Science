import { useEffect, useMemo, type Dispatch, type SetStateAction } from 'react';
import type { PdfReadingSource } from '@/common/adapter/pdfReadingContext';
import {
  normalizeComposerContextItems,
  type ComposerContextItem,
  type ComposerArtifactContext,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';
import { useOptionalPreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { useComposerLifetime } from '@/renderer/hooks/chat/useComposerLifetime';
import { composerPdfPreview } from '@/renderer/services/runtime/pdfReadingPreview';

/** Before a frame exists, preview contributes only to the existing draft.
 * The first-message controller admits actual bindings after creation/upload. */
export function useGuidPdfReading(
  ownerId: string,
  selectedProjectId: string | undefined,
  items: ComposerContextItem[],
  setItems: Dispatch<SetStateAction<ComposerContextItem[]>>,
  pending: boolean,
  setEnabled: (enabled: boolean) => void,
  allowPreviewProject = true
) {
  const preview = useOptionalPreviewContext();
  const register = preview?.registerPdfReadingOwner;
  const projects = new Set(
    items
      .filter((item) => item.kind === 'artifact')
      .map((item) => item.projectId)
      .filter(Boolean)
  );
  const projectId =
    selectedProjectId ??
    (projects.size === 1
      ? [...projects][0]
      : projects.size === 0 && allowPreviewProject
        ? preview?.activeTab?.metadata?.projectId
        : undefined);
  const capture = useComposerLifetime(`${ownerId}:${projectId ?? ''}`);
  const selected = useMemo(
    () =>
      items.filter(
        (item): item is ComposerArtifactContext => item.kind === 'artifact' && item.readingSelected === true
      ),
    [items]
  );
  useEffect(() => {
    if (!register || !ownerId || !projectId) return;
    const isCurrent = capture();
    const matches = (item: ComposerContextItem, source: PdfReadingSource): item is ComposerArtifactContext =>
      item.kind === 'artifact' && item.artifactId === source.artifact_id && item.versionId === source.version_id;
    return register({
      ownerId,
      frameId: 'draft',
      projectId,
      context: null,
      pending,
      draftSources: selected.map((item) => ({ artifact_id: item.artifactId, version_id: item.versionId })),
      link: async (source, detail) => {
        if (!isCurrent() || pending || !detail?.name || !detail.position || detail.position.pageCount <= 1)
          throw new Error('PDF draft changed');
        if (selected.length >= 3 && !selected.some((item) => matches(item, source)))
          throw new Error('PDF reading limit reached');
        setEnabled(true);
        setItems((current) => {
          const existing = current.find((item) => matches(item, source));
          if (
            !(existing?.kind === 'artifact' && existing.readingSelected) &&
            current.filter((item) => item.kind === 'artifact' && item.readingSelected).length >= 3
          )
            return current;
          const next = existing ?? {
            kind: 'artifact' as const,
            artifactId: source.artifact_id,
            versionId: source.version_id,
            projectId,
            label: detail.name,
            contentType: 'application/pdf',
          };
          return normalizeComposerContextItems([
            ...current.filter((item) => !matches(item, source)),
            { ...next, readingSelected: true, readingPosition: detail.position },
          ]);
        });
      },
      unlink: async () => {
        throw new Error('A draft has no canonical PDF binding');
      },
      unlinkDraft: async (source) => {
        if (!isCurrent() || pending) throw new Error('PDF draft changed');
        if (selected.length <= 1) setEnabled(false);
        setItems((current) =>
          current.map((item): ComposerContextItem =>
            matches(item, source) ? { ...item, readingSelected: undefined, readingPosition: undefined } : item
          )
        );
      },
      reportPosition: (source, position) => {
        if (!isCurrent()) return;
        setItems((current) => {
          const item = current.find((candidate) => matches(candidate, source));
          if (
            item?.kind !== 'artifact' ||
            !item.readingSelected ||
            (item.readingPosition?.pageNumber === position.pageNumber &&
              item.readingPosition?.pageCount === position.pageCount)
          )
            return current;
          return normalizeComposerContextItems(
            current.map((candidate) =>
              matches(candidate, source) ? { ...candidate, readingPosition: position } : candidate
            )
          );
        });
      },
    });
  }, [capture, ownerId, projectId, pending, selected, setItems, setEnabled, register]);
  const active = selected.find((item) => item.versionId === preview?.activeTab?.metadata?.versionId) ?? selected[0];
  return {
    position: active?.readingPosition
      ? { source: { artifact_id: active.artifactId, version_id: active.versionId }, ...active.readingPosition }
      : undefined,
    openArtifact: (item: ComposerArtifactContext) => {
      const target = composerPdfPreview(item, projectId);
      if (target) preview?.openPreview(target.content, 'pdf', target.metadata, { presentation: 'single' });
    },
  };
}
