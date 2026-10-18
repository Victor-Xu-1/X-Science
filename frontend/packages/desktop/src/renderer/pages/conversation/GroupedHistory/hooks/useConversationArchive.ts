import type { TChatConversation } from '@/common/config/storage';
import { Message } from '@arco-design/web-react';
import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  getArchiveOperationsSnapshot,
  isArchiveOperationPending,
  runConversationArchive,
  subscribeArchiveOperations,
} from '@/renderer/services/runtime/conversationArchiveOperations';

export function useConversationArchive() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const path = useRef(location.pathname);
  path.current = location.pathname;
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const state = useSyncExternalStore(subscribeArchiveOperations, getArchiveOperationsSnapshot);
  const archiveConversation = useCallback(
    async (conversation: TChatConversation) => {
      const scope = getRendererAccountScopeToken();
      const origin = path.current;
      try {
        await runConversationArchive(conversation, true);
        if (
          mounted.current &&
          scope === getRendererAccountScopeToken() &&
          path.current === origin &&
          origin === `/conversation/${conversation.id}`
        )
          void navigate('/guid', { replace: true });
      } catch {
        if (mounted.current && scope === getRendererAccountScopeToken())
          Message.error(t('conversation.history.archiveFailed'));
      }
    },
    [navigate, t]
  );
  const isArchivePending = useCallback(
    (conversation: TChatConversation) => isArchiveOperationPending(conversation, state),
    [state]
  );
  return { archiveConversation, isArchivePending };
}
