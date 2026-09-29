import { useCallback, useEffect, useMemo, useState } from 'react';
import type { TMessage } from '@/common/chat/chatLib';
import * as ipcBridge from '@/common/adapter/ipcBridge';
import {
  SYNON_BIOMED_BRANCH_SELECTION_EVENT,
  type SynonBiomedBranchSelectionEventDetail,
} from '@/renderer/services/synonBiomedConversationBranches';
import { loadConversationTurnIndex, mergeLiveTurnPreviews, type ConversationTurnIndex } from './turnIndex';

export function useTurnIndex(conversationId: string | undefined, messages: TMessage[], windowBranchId?: string) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<ConversationTurnIndex & { owner: string; failed: boolean }>();
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
      .then((index) => {
        if (!controller.signal.aborted) setState({ owner, ...index, failed: false });
      })
      .catch(() => {
        if (!controller.signal.aborted) setState({ owner, items: [], failed: true });
      });
    return () => controller.abort();
  }, [conversationId, owner]);
  const current = state?.owner === owner ? state : undefined;
  const ready = Boolean(current && !current.failed && current.branchId === windowBranchId);
  useEffect(() => {
    if (!ready) return;
    // The visible message window can be replaced by an anchor jump. Keep its
    // accepted turns in this owner-fenced index, not only in the render result.
    setState((previous) => {
      if (previous?.owner !== owner || previous.failed || previous.branchId !== windowBranchId) return previous;
      const items = mergeLiveTurnPreviews(previous.items, messages);
      return items === previous.items ? previous : { ...previous, items };
    });
  }, [messages, owner, ready, windowBranchId]);
  const items = useMemo(
    () => (ready ? mergeLiveTurnPreviews(current!.items, messages) : (current?.items ?? [])),
    [current?.items, messages, ready]
  );
  return { items, loading: Boolean(conversationId && !current), failed: current?.failed ?? false, retry };
}
