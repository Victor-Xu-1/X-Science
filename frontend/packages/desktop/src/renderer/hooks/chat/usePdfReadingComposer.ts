import { ipcBridge } from '@/common';
import type {
  PdfReadingPendingMutation,
  PdfReadingSelection,
  PdfReadingSource,
} from '@/common/adapter/pdfReadingContext';
import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { PdfReadingController } from '@/renderer/services/runtime/PdfReadingController';
import { getXScienceArtifactVersionContentUrl } from '@/renderer/services/xScienceArtifacts';
import { usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { composerPdfPreview } from '@/renderer/services/runtime/pdfReadingPreview';
import type { PdfReadingSourcePosition } from '@/renderer/services/runtime/pdfReadingHandoff';
import type { ComposerArtifactContext } from '@/renderer/components/chat/SendBox/composerCompositionModel';
import type { PdfReadingPendingAccess } from '@/renderer/services/runtime/pdfReadingPendingState';

export function usePdfReadingComposer(options: {
  enabled: boolean;
  ownerId: string;
  conversationId: string;
  frameIncarnationId?: string;
  pending?: PdfReadingPendingMutation;
  persistPending: PdfReadingPendingAccess;
}) {
  const preview = usePreviewContext();
  const key = useId();
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const persist = useRef(options.persistPending);
  persist.current = options.persistPending;
  const controller = useMemo(
    () =>
      new PdfReadingController({
        ownerId: options.ownerId,
        frameId: options.conversationId,
        frameIncarnationId: options.frameIncarnationId,
        persistPending: (value, expected) => persist.current(value, expected),
      }),
    [account, options.ownerId, options.conversationId, options.frameIncarnationId]
  );
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  useEffect(
    () =>
      registerRendererAccountReset(`pdf-reading:${key}`, () => {
        controller.dispose();
        setAccount(getRendererAccountScopeToken());
      }),
    [controller, key]
  );
  useEffect(() => {
    controller.activate();
    if (options.enabled && options.ownerId) void controller.load().catch((): undefined => undefined);
    return () => controller.dispose();
  }, [controller, options.enabled, options.ownerId]);
  useEffect(() => {
    controller.restorePending(options.pending);
  }, [controller, options.pending]);
  useEffect(() => {
    if (!options.enabled) return;
    return ipcBridge.conversation.listChanged?.on((event) => {
      const context = controller.getSnapshot().context;
      if (
        event.reading_context_changed &&
        event.conversation_id === options.conversationId &&
        (!context ||
          (event.frame_incarnation_id === context.scope.frame_incarnation_id &&
            event.root_frame_incarnation_id === context.scope.root_frame_incarnation_id))
      ) {
        void controller.load().catch((): undefined => undefined);
      }
    });
  }, [controller, options.conversationId, options.enabled]);
  const port = useMemo(
    () => ({
      ownerId: options.ownerId,
      frameId: options.conversationId,
      projectId: view.context?.scope.project_id,
      context: view.context,
      pending: Boolean(view.pending) || view.blocked,
      link: async (source: PdfReadingSource) => {
        await controller.link([source]);
      },
      unlink: async (bindingId: string) => {
        await controller.unlink(bindingId);
      },
      reportPosition: controller.reportPosition,
    }),
    [controller, options.ownerId, options.conversationId, view.context, view.pending, view.blocked]
  );
  useEffect(() => {
    if (options.enabled && options.ownerId) return preview.registerPdfReadingOwner?.(port);
  }, [options.enabled, options.ownerId, port, preview.registerPdfReadingOwner]);
  const open = (bindingId: string) => {
    const context = controller.getSnapshot().context;
    const binding = context?.bindings.find((item) => item.binding_id === bindingId);
    if (!binding || !context) return;
    const url = getXScienceArtifactVersionContentUrl(binding.version_id);
    preview.openPreview(
      url,
      'pdf',
      {
        artifactId: binding.artifact_id,
        versionId: binding.version_id,
        projectId: context.scope.project_id,
        rootFrameId: context.scope.root_frame_id,
        contentUrl: url,
        file_name: binding.name,
        editable: false,
        targetPdfPage: controller.positionFor(binding.version_id)?.pageNumber,
      },
      { presentation: 'single' }
    );
  };
  const openArtifact = (item: ComposerArtifactContext) => {
    const target = composerPdfPreview(item, view.context?.scope.project_id);
    if (target) preview.openPreview(target.content, 'pdf', target.metadata, { presentation: 'single' });
  };
  const capture = () => controller.capture(preview.activeTab?.metadata?.versionId);
  const captureSend = () => controller.captureSend(preview.activeTab?.metadata?.versionId);
  const prepareInitialSources = async (
    sources: PdfReadingSource[],
    position?: PdfReadingSourcePosition
  ): Promise<PdfReadingSelection | undefined> => {
    await controller.prepareSources(sources);
    if (position) controller.reportPosition(position.source, position);
    return position ? controller.capture(position.source.version_id) : capture();
  };
  return {
    view,
    controller,
    open,
    openArtifact,
    capture,
    captureSend,
    prepareInitialSources,
    port,
  };
}
