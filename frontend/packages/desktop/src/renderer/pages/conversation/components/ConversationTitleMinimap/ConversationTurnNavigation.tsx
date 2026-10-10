import React, { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import type { TMessage } from '@/common/chat/chatLib';
import { dispatchChatMessageJump } from '@/renderer/utils/chat/chatMinimapEvents';
import ConversationTurnRail from './ConversationTurnRail';
import { useTurnIndex } from './useTurnIndex';

export default function ConversationTurnNavigation({
  conversationId,
  messages,
  viewport,
  windowBranchId,
}: {
  conversationId?: string;
  messages: TMessage[];
  viewport: HTMLDivElement | null;
  windowBranchId?: string;
}) {
  const { t } = useTranslation();
  const { items, loading, failed, retry } = useTurnIndex(conversationId, messages, windowBranchId);
  const onJump = useCallback(
    (messageId: string) => {
      if (!conversationId) return;
      dispatchChatMessageJump({
        conversation_id: conversationId,
        messageId,
        align: 'start',
        behavior: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
      });
    },
    [conversationId]
  );
  if (!conversationId || loading) return null;
  if (failed)
    return (
      <button type='button' className='absolute left-8px bottom-8px text-t-secondary text-12px' onClick={retry}>
        {t('conversation.minimap.retryNavigation')}
      </button>
    );
  return <ConversationTurnRail key={conversationId} items={items} viewport={viewport} onJump={onJump} />;
}
