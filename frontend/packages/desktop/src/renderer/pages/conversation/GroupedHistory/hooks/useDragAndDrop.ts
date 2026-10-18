/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { DragEndEvent, DragStartEvent } from '@dnd-kit/core';
import { PointerSensor, useSensor, useSensors } from '@dnd-kit/core';
import { arrayMove } from '@dnd-kit/sortable';
import { Message } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import { reorderPinnedConversations } from '@/renderer/services/runtime/conversationPresentation';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import type { TChatConversation } from '@/common/config/storage';
import { useLayoutContext } from '@/renderer/hooks/context/LayoutContext';
import { emitter } from '@/renderer/utils/emitter';
import { useCallback, useEffect, useRef, useState } from 'react';

type UseDragAndDropParams = {
  pinnedConversations: TChatConversation[];
  batchMode: boolean;
  collapsed: boolean;
  pinnedListComplete?: boolean;
};

export const useDragAndDrop = ({
  pinnedConversations,
  batchMode,
  collapsed,
  pinnedListComplete = true,
}: UseDragAndDropParams) => {
  const { t } = useTranslation();
  const savingRef = useRef<symbol | null>(null);
  const [saving, setSaving] = useState(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    const reset = registerRendererAccountReset('pinned-order-action', () => {
      savingRef.current = null;
      setSaving(false);
      setActiveId(null);
      setActiveConversation(null);
      isDraggingRef.current = false;
    });
    return () => {
      mounted.current = false;
      reset();
    };
  }, []);
  const layout = useLayoutContext();
  const isMobile = layout?.isMobile ?? false;
  const [activeId, setActiveId] = useState<string | null>(null);
  const [activeConversation, setActiveConversation] = useState<TChatConversation | null>(null);
  const isDraggingRef = useRef(false);

  const isDragEnabled = !batchMode && !collapsed && !isMobile && pinnedListComplete && !saving;

  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: {
        distance: 8,
      },
    })
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const id = String(event.active.id);
      setActiveId(id);
      isDraggingRef.current = true;

      const conv = pinnedConversations.find((c) => c.id === id);
      setActiveConversation(conv ?? null);
    },
    [pinnedConversations]
  );

  const handleDragEnd = useCallback(
    async (event: DragEndEvent) => {
      const { active, over } = event;
      setActiveId(null);
      setActiveConversation(null);
      isDraggingRef.current = false;

      if (!over || active.id === over.id || savingRef.current || !pinnedListComplete) return;

      const activeIdStr = String(active.id);
      const overIdStr = String(over.id);

      const oldIndex = pinnedConversations.findIndex((item) => item.id === activeIdStr);
      const newIndex = pinnedConversations.findIndex((item) => item.id === overIdStr);

      if (oldIndex === -1 || newIndex === -1) return;

      const scope = getRendererAccountScopeToken();
      const operation = Symbol('pinned-order');
      savingRef.current = operation;
      setSaving(true);
      try {
        await reorderPinnedConversations(arrayMove(pinnedConversations, oldIndex, newIndex));
      } catch {
        if (mounted.current && scope === getRendererAccountScopeToken())
          Message.error(t('conversation.history.pinFailed'));
      } finally {
        if (savingRef.current === operation) {
          savingRef.current = null;
          if (mounted.current && scope === getRendererAccountScopeToken()) {
            setSaving(false);
            emitter.emit('chat.history.refresh');
          }
        }
      }
    },
    [pinnedConversations, pinnedListComplete, t]
  );

  const handleDragCancel = useCallback(() => {
    setActiveId(null);
    setActiveConversation(null);
    isDraggingRef.current = false;
  }, []);

  return {
    sensors,
    activeId,
    activeConversation,
    handleDragStart,
    handleDragEnd,
    handleDragCancel,
    isDragEnabled,
  };
};
