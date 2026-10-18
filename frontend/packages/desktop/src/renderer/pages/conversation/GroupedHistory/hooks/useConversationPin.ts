import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { setConversationPinned } from '@/renderer/services/runtime/conversationPresentation';
import { emitter } from '@/renderer/utils/emitter';
import { Message } from '@arco-design/web-react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

export function useConversationPin() {
  const { t } = useTranslation();
  const requests = useRef(new Set<string>());
  const [pendingPins, setPendingPins] = useState(new Set<string>());
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const togglePin = useCallback(
    async (conversation: TChatConversation) => {
      const scope = getRendererAccountScopeToken();
      const key = `${scope}:${conversation.id}`;
      if (requests.current.has(key)) return;
      requests.current.add(key);
      setPendingPins(new Set(requests.current));
      const current = () => mounted.current && scope === getRendererAccountScopeToken();
      try {
        await setConversationPinned(conversation, !conversation.extra.pinned);
        if (current()) emitter.emit('chat.history.refresh');
      } catch {
        if (current()) {
          Message.error(t('conversation.history.pinFailed'));
          emitter.emit('chat.history.refresh');
        }
      } finally {
        requests.current.delete(key);
        if (current()) setPendingPins(new Set(requests.current));
      }
    },
    [t]
  );
  const isPinPending = useCallback(
    (id: string) => pendingPins.has(`${getRendererAccountScopeToken()}:${id}`),
    [pendingPins]
  );
  return { togglePin, isPinPending };
}
