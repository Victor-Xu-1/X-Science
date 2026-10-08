/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import type { IMessageSearchItem } from '@/common/types/conversation/messageSearch';
import { useCallback, useLayoutEffect, useRef, useState } from 'react';

const PAGE_SIZE = 30;
const SEARCH_DEBOUNCE_MS = 120;
const EMPTY_ITEMS: IMessageSearchItem[] = [];
const EMPTY_STATE = {
  query: null as string | null,
  items: EMPTY_ITEMS,
  page: 0,
  hasMore: false,
  loading: false,
  loadingMore: false,
  loadFailed: false,
};

interface ConversationSearchResults {
  items: IMessageSearchItem[];
  loading: boolean;
  loadingMore: boolean;
  loadFailed: boolean;
  loadMore: () => void;
  retry: () => void;
}

/** One query/request owner; presentation never exposes another query's results. */
export function useConversationSearchResults({
  visible,
  keyword,
  rankItems,
}: {
  visible: boolean;
  keyword: string;
  rankItems: (items: IMessageSearchItem[], query: string) => IMessageSearchItem[];
}): ConversationSearchResults {
  const query = keyword.trim();
  const [state, setState] = useState(EMPTY_STATE);
  const generation = useRef(0);
  const inFlight = useRef<number | null>(null);
  const current = state.query === query;
  const loading = visible && (!current || state.loading);

  const runSearch = useCallback(
    async (page: number, append: boolean): Promise<void> => {
      if (!visible || (append && inFlight.current !== null)) return;
      const request = ++generation.current;
      inFlight.current = request;
      setState((previous) =>
        append && previous.query === query
          ? { ...previous, loadingMore: true, loadFailed: false }
          : { ...EMPTY_STATE, query, loading: true }
      );
      try {
        const result = await ipcBridge.database.searchConversationMessages.invoke({
          keyword: query,
          page,
          page_size: PAGE_SIZE,
        });
        if (generation.current !== request) return;
        const ranked = rankItems(result.items, query);
        setState((previous) => ({
          ...EMPTY_STATE,
          query,
          items: append && previous.query === query ? [...previous.items, ...ranked] : ranked,
          page,
          hasMore: result.has_more,
        }));
      } catch (error) {
        if (generation.current !== request) return;
        console.error('[ConversationSearchPopover] Search failed:', error);
        setState((previous) =>
          append && previous.query === query
            ? { ...previous, loading: false, loadingMore: false, loadFailed: true }
            : { ...EMPTY_STATE, query, loadFailed: true }
        );
      } finally {
        if (inFlight.current === request) inFlight.current = null;
      }
    },
    [query, rankItems, visible]
  );

  useLayoutEffect(() => {
    // Invalidate at commit, not after the debounce timer. An old promise may
    // settle while the next query is waiting; it must not regain UI authority.
    generation.current += 1;
    inFlight.current = null;
    if (!visible) return;
    setState({ ...EMPTY_STATE, loading: true });
    const timer = window.setTimeout(
      (): void => {
        void runSearch(0, false);
      },
      query ? SEARCH_DEBOUNCE_MS : 0
    );
    return () => {
      window.clearTimeout(timer);
      generation.current += 1;
      inFlight.current = null;
    };
  }, [query, runSearch, visible]);

  const loadMore = useCallback(() => {
    if (!visible || loading || state.loadingMore || state.loadFailed || !current || !state.hasMore) return;
    void runSearch(state.page + 1, true);
  }, [current, loading, runSearch, state, visible]);
  const retry = useCallback((): void => {
    void runSearch(0, false);
  }, [runSearch]);

  return {
    items: current ? state.items : EMPTY_ITEMS,
    loading,
    loadingMore: current && state.loadingMore,
    loadFailed: current && state.loadFailed,
    loadMore,
    retry,
  };
}
