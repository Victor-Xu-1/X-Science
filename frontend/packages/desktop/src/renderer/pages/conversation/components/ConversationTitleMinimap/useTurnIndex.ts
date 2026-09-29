import { useCallback, useEffect, useMemo, useState } from 'react';
import type { TMessage } from '@/common/chat/chatLib';
import * as ipcBridge from '@/common/adapter/ipcBridge';
import {
  SYNON_BIOMED_BRANCH_SELECTION_EVENT,
  type SynonBiomedBranchSelectionEventDetail,
} from '@/renderer/services/synonBiomedConversationBranches';
import type { TurnPreviewItem } from './minimapTypes';
import { loadConversationTurnIndex, mergeLiveTurnPreviews } from './turnIndex';

export function useTurnIndex(conversationId: string | undefined, messages: TMessage[]) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<{ owner: string; items: TurnPreviewItem[]; failed: boolean }>();
  const owner = `${conversationId ?? ''}:${revision}`;
  const retry = useCallback(() => setRevision((value) => value + 1), []);
  useEffect(() => {
    const selection = (event: Event) => {
      if ((event as CustomEvent<SynonBiomedBranchSelectionEventDetail>).detail?.conversationId === conversationId)
        retry();
    };
    window.addEventListener(SYNON_BIOMED_BRANCH_SELECTION_EVENT, selection);
    const unsubscribe = ipcBridge.conversation.historyRebased.on((payload) => {
      if (payload.conversation_id === conversationId) retry();
    });
    return () => {
      window.removeEventListener(SYNON_BIOMED_BRANCH_SELECTION_EVENT, selection);
      unsubscribe();
    };
  }, [conversationId, retry]);
  useEffect(() => {
    if (!conversationId) return;
    const controller = new AbortController();
    void loadConversationTurnIndex(conversationId, { signal: controller.signal })
      .then((items) => {
        if (!controller.signal.aborted) setState({ owner, items, failed: false });
      })
      .catch(() => {
        if (!controller.signal.aborted) setState({ owner, items: [], failed: true });
      });
    return () => controller.abort();
  }, [conversationId, owner]);
  const current = state?.owner === owner ? state : undefined;
  const items = useMemo(() => mergeLiveTurnPreviews(current?.items ?? [], messages), [current?.items, messages]);
  return { items, loading: Boolean(conversationId && !current), failed: current?.failed ?? false, retry };
}
