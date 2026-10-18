import { fromApiPaginatedConversations } from '@/common/adapter/apiModelMapper';
import { httpRequest } from '@/common/adapter/httpBridge';
import type { PaginatedResult } from '@/common/adapter/ipcDatabaseClient';
import type { TChatConversation } from '@/common/config/storage';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { isConversationArchived, type ArchivedConversation } from '@/renderer/services/runtime/conversationArchive';
import { addEventListener } from '@/renderer/utils/emitter';
import { useCallback, useEffect, useId, useRef, useState } from 'react';

type ArchivedPage = PaginatedResult<ArchivedConversation>;
type State = {
  items: ArchivedConversation[];
  cursor: string | null;
  hasMore: boolean;
  loading: boolean;
  error: boolean;
};
const initial = (): State => ({ items: [], cursor: null, hasMore: false, loading: true, error: false });

export async function loadArchivedConversationPage(cursor: string | null, signal: AbortSignal): Promise<ArchivedPage> {
  const query = new URLSearchParams({ section: 'archived', limit: '50' });
  if (cursor) query.set('cursor', cursor);
  const page = await httpRequest<PaginatedResult<TChatConversation>>('GET', `/api/conversations?${query}`, undefined, {
    signal,
    timeoutMs: 15_000,
  });
  if (
    !page ||
    !Array.isArray(page.items) ||
    page.items.length > 50 ||
    typeof page.has_more !== 'boolean' ||
    !Number.isSafeInteger(page.total) ||
    page.total < 0 ||
    (page.has_more && (typeof page.next_cursor !== 'string' || !page.next_cursor || page.next_cursor === cursor)) ||
    page.items.some((item) => !item || typeof item.id !== 'string' || !item.extra || !isConversationArchived(item))
  )
    throw new Error('Invalid archived conversation page');
  return fromApiPaginatedConversations({ ...page, items: page.items.filter(isConversationArchived) });
}

/** One bounded server section, no local archive membership or silent empty fallback. */
export function useArchivedConversations() {
  const [state, setState] = useState<State>(initial);
  const [scope, setScope] = useState(getRendererAccountScopeToken);
  const componentId = useId();
  const read = useRef<AbortController | null>(null);
  const sequence = useRef(0);
  const failedCursor = useRef<string | null>(null);
  const loading = useRef(false);
  const load = useCallback(
    async (cursor: string | null) => {
      if (scope !== getRendererAccountScopeToken()) return;
      if (cursor && loading.current) return;
      const request = ++sequence.current;
      read.current?.abort();
      const controller = new AbortController();
      read.current = controller;
      loading.current = true;
      failedCursor.current = cursor;
      setState((current) => ({ ...current, loading: true, error: false }));
      const current = () =>
        !controller.signal.aborted && request === sequence.current && scope === getRendererAccountScopeToken();
      try {
        const page = await loadArchivedConversationPage(cursor, controller.signal);
        if (!current()) return;
        setState((previous) => {
          const items = cursor ? [...previous.items] : [];
          const byId = new Map(items.map((item) => [item.id, item]));
          for (const item of page.items) byId.set(item.id, item);
          return {
            items: [...byId.values()],
            cursor: page.next_cursor ?? null,
            hasMore: page.has_more,
            loading: false,
            error: false,
          };
        });
      } catch {
        if (current()) setState((previous) => ({ ...previous, loading: false, error: true }));
      } finally {
        if (request === sequence.current) loading.current = false;
      }
    },
    [scope]
  );
  useEffect(
    () =>
      registerRendererAccountReset(`archived-list:${componentId}`, () => {
        sequence.current += 1;
        read.current?.abort();
        loading.current = false;
        setState(initial());
        setScope(getRendererAccountScopeToken());
      }),
    [componentId]
  );
  useEffect(() => {
    void load(null);
    return () => {
      sequence.current += 1;
      read.current?.abort();
      loading.current = false;
    };
  }, [load]);
  useEffect(
    () =>
      addEventListener('chat.history.refresh', () => {
        void load(null);
      }),
    [load]
  );
  return {
    ...state,
    refresh: () => load(null),
    loadMore: () => load(state.cursor),
    retry: () => load(failedCursor.current),
  };
}
