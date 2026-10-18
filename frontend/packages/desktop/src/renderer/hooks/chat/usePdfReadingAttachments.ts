import type { ComposerContextItem } from '@/renderer/components/chat/SendBox/composerCompositionModel';
import type { PdfReadingController } from '@/renderer/services/runtime/PdfReadingController';
import { suggestPdfReading } from '@/renderer/services/runtime/pdfReadingHandoff';
import { useEffect, useRef, useState } from 'react';

export function usePdfReadingAttachments(
  controller: PdfReadingController,
  items: ComposerContextItem[],
  disabled: boolean,
  hasOtherFiles: boolean
) {
  const [preparing, setPreparing] = useState(false);
  const active = useRef(false);
  const lifetime = useRef<object>({});
  useEffect(() => {
    const token = {};
    lifetime.current = token;
    active.current = false;
    setPreparing(false);
    return () => {
      if (lifetime.current === token) lifetime.current = {};
    };
  }, [controller]);
  const context = controller.getSnapshot().context;
  const artifacts = items.filter((item) => item.kind === 'artifact');
  const suggested =
    !disabled &&
    !hasOtherFiles &&
    Boolean(context?.bindings.length) &&
    suggestPdfReading(
      artifacts.map((item) => ({ name: item.label, size: item.sizeBytes, contentType: item.contentType })),
      3 - (context?.bindings.length ?? 0)
    );
  const prepare = async () => {
    if (!suggested) return;
    if (active.current) throw new Error('PDF reading preparation is already pending');
    const token = lifetime.current;
    active.current = true;
    setPreparing(true);
    try {
      await controller.prepareSources(
        artifacts.map((item) => ({ artifact_id: item.artifactId, version_id: item.versionId }))
      );
      if (lifetime.current !== token) throw new Error('PDF reading composer changed');
    } finally {
      if (lifetime.current === token) {
        active.current = false;
        setPreparing(false);
      }
    }
  };
  return { suggested, preparing, prepare };
}
