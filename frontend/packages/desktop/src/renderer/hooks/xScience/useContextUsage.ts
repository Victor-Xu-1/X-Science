/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useState } from 'react';
import {
  ContextUsageRequestError,
  fetchContextUsage,
  isContextUsageRetryable,
  type ContextUsageResult,
} from '@/renderer/services/contextUsage';

export const CONTEXT_USAGE_TIMEOUT_MS = 8000;
export const CONTEXT_USAGE_POLL_MS = 5000;
export const CONTEXT_USAGE_MAX_RETRIES = 3;
type UsageState = (
  | { conversationId: string; status: 'loading' | 'error' }
  | (ContextUsageResult & { conversationId: string })
) & { refreshState?: 'refreshing' | 'retrying' | 'stale' };

/** One bounded request at a time; closing/switching aborts obsolete requests. */
export function useContextUsage(conversationId: string, visible: boolean, active: boolean) {
  const [state, setState] = useState<UsageState>({ conversationId, status: 'loading' });
  const [refresh, setRefresh] = useState(0);
  const retry = useCallback(() => setRefresh((value) => value + 1), []);
  useEffect(() => {
    const resume = () => {
      if ((visible || active) && !document.hidden) retry();
    };
    window.addEventListener('online', resume);
    window.addEventListener('focus', resume);
    document.addEventListener('visibilitychange', resume);
    return () => {
      window.removeEventListener('online', resume);
      window.removeEventListener('focus', resume);
      document.removeEventListener('visibilitychange', resume);
    };
  }, [visible, active, retry]);
  useEffect(() => {
    let disposed = false;
    let poll: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    let deadline: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    setState((previous) =>
      previous.conversationId === conversationId && previous.status === 'available'
        ? { ...previous, refreshState: 'refreshing' }
        : { conversationId, status: conversationId ? 'loading' : 'unavailable' }
    );
    const load = async () => {
      if (!conversationId) return;
      const requestController = new AbortController();
      controller = requestController;
      const stopped = new Promise<never>((_, reject) => {
        requestController.signal.addEventListener(
          'abort',
          () => reject(new DOMException('Context usage request aborted', 'AbortError')),
          { once: true }
        );
      });
      const timeout = new Promise<never>((_, reject) => {
        deadline = setTimeout(() => {
          reject(new ContextUsageRequestError('Context usage request timed out', true));
          requestController.abort();
        }, CONTEXT_USAGE_TIMEOUT_MS);
      });
      try {
        // The deadline settles the UI even if a transport ignores AbortSignal.
        const result = await Promise.race([
          fetchContextUsage(conversationId, requestController.signal),
          stopped,
          timeout,
        ]);
        if (disposed) return;
        failures = 0;
        setState({ ...result, conversationId });
        // Poll only while the user is inspecting usage or the task is active.
        if (visible || active) poll = setTimeout(() => void load(), CONTEXT_USAGE_POLL_MS);
      } catch (error) {
        if (disposed) return;
        const willRetry = (visible || active) && isContextUsageRetryable(error) && failures < CONTEXT_USAGE_MAX_RETRIES;
        setState((previous) =>
          previous.conversationId === conversationId && previous.status === 'available'
            ? { ...previous, refreshState: willRetry ? 'retrying' : 'stale' }
            : { conversationId, status: 'error', refreshState: willRetry ? 'retrying' : 'stale' }
        );
        if (willRetry) poll = setTimeout(() => void load(), 1000 * 2 ** failures++);
        // A bounded failure episode can be restarted by explicit Retry or a
        // fresh visibility/network/runtime transition, never an endless loop.
      } finally {
        clearTimeout(deadline);
      }
    };
    void load();
    return () => {
      disposed = true;
      clearTimeout(poll);
      clearTimeout(deadline);
      controller?.abort();
    };
  }, [conversationId, visible, active, refresh]);
  return {
    state: state.conversationId === conversationId ? state : { conversationId, status: 'loading' as const },
    retry,
  };
}
