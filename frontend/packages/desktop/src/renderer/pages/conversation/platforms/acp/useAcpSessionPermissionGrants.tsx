import type { TChatConversation } from '@/common/config/storage';
import MobileActionSheet, { type MobileActionSheetEntry } from '@/renderer/components/chat/MobileActionSheet';
import { useSessionPermissionGrantsController } from '@/renderer/hooks/xScience/runtime/useSessionPermissionGrants';
import React, { useEffect, useState } from 'react';

/** Adapts this composer identity to the shared grant controller and its existing mobile presentation. */
export function useAcpSessionPermissionGrants(
  conversationId: string,
  ownerId: string,
  conversation: TChatConversation | undefined,
  enabled: boolean
) {
  const [isMobileSheetOpen, setIsMobileSheetOpen] = useState(false);
  const grantConversation =
    enabled && conversation?.type === 'acp' && conversation.id === conversationId ? conversation : null;
  const grantsController = useSessionPermissionGrantsController(
    grantConversation &&
      typeof grantConversation.extra.frame_incarnation_id === 'string' &&
      typeof grantConversation.extra.root_frame_id === 'string'
      ? {
          ownerId,
          frameId: conversationId,
          frameIncarnationId: grantConversation.extra.frame_incarnation_id,
          rootFrameId: grantConversation.extra.root_frame_id,
        }
      : null
  );
  useEffect(() => {
    setIsMobileSheetOpen(false);
  }, [grantsController]);

  // A render helper keeps the existing controlled sheet and hidden attachment input
  // together; it does not introduce a second mobile or permission state owner.
  const renderMobileSheet = (
    isMobile: boolean,
    entries: MobileActionSheetEntry[],
    hiddenInput: React.ReactNode,
    title: string
  ) =>
    isMobile ? (
      <>
        <MobileActionSheet
          key={grantsController?.presentationKey ?? `${ownerId}:${conversationId}`}
          open={isMobileSheetOpen}
          onClose={() => setIsMobileSheetOpen(false)}
          title={title}
          entries={entries}
        />
        {hiddenInput}
      </>
    ) : null;

  return { grantsController, setIsMobileSheetOpen, renderMobileSheet };
}
