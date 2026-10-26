/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import {
  ContextUsageRequestError,
  fetchContextUsage,
  isContextUsageRetryable,
  type ContextUsageResult,
} from '@/renderer/services/contextUsage';

export const CONTEXT_USAGE_TIMEOUT_MS = 8000;
export const CONTEXT_USAGE_POLL_MS = 5000;
export const CONTEXT_USAGE_MAX_RETRIES = 3;
export type ContextUsageState = (
  | { conversationId: string; status: 'loading' | 'error' }
  | (ContextUsageResult & { conversationId: string })
) & {
  refreshState?: 'refreshing' | 'retrying' | 'stale';
  /** Request state is separate from a retained compact snapshot or inspection. */
  inspectionRequest?: 'loading' | 'error';
};

/** One bounded request at a time; closing/switching aborts obsolete requests. */
export function useContextUsage(conversationId: string, visible: boolean, active: boolean, inspect = false) {
  const accountScope = getRendererAccountScopeToken();
  const scopeKey = JSON.stringify([accountScope, conversationId]);
  const requestMode = inspect ? 'inspect' : 'summary';
  const currentScope = useRef(scopeKey);
  currentScope.current = scopeKey;
  const resetId = useId();
  const [ownedState, setOwnedState] = useState<{
    scope: string;
    requestMode: 'summary' | 'inspect';
    value: ContextUsageState;
  }>({
    scope: scopeKey,
    requestMode,
    value: { conversationId, status: 'loading' },
  });
  const [refresh, setRefresh] = useState(0);
  const retry = useCallback(() => setRefresh((value) => value + 1), []);
  useEffect(() => registerRendererAccountReset('context-usage:' + resetId, retry), [resetId, retry]);
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
    const setState = (update: ContextUsageState | ((previous: ContextUsageState) => ContextUsageState)) => {
      if (disposed || currentScope.current !== scopeKey || getRendererAccountScopeToken() !== accountScope) return;
      setOwnedState((previous) => {
        if (disposed || currentScope.current !== scopeKey || getRendererAccountScopeToken() !== accountScope)
          return previous;
        return {
          scope: scopeKey,
          requestMode,
          value:
            typeof update === 'function'
              ? update(previous.scope === scopeKey ? previous.value : { conversationId, status: 'loading' })
              : update,
        };
      });
    };
    setState((previous) =>
      previous.conversationId === conversationId &&
      (previous.status === 'available' || (inspect && previous.status === 'unavailable' && previous.inspection))
        ? { ...previous, refreshState: 'refreshing', inspectionRequest: inspect ? 'loading' : undefined }
        : { conversationId, status: conversationId ? 'loading' : 'unavailable' }
    );
    const load = async () => {
      if (!conversationId) return;
      if (inspect) setState((previous) => ({ ...previous, inspectionRequest: 'loading' }));
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
          fetchContextUsage(conversationId, requestController.signal, inspect),
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
        if (error instanceof ContextUsageRequestError && error.scopeChanged) {
          setState({
            conversationId,
            status: 'error',
            refreshState: 'stale',
            inspectionRequest: inspect ? 'error' : undefined,
          });
          return;
        }
        const willRetry = (visible || active) && isContextUsageRetryable(error) && failures < CONTEXT_USAGE_MAX_RETRIES;
        setState((previous) =>
          previous.conversationId === conversationId &&
          (previous.status === 'available' || (inspect && previous.status === 'unavailable' && previous.inspection))
            ? {
                ...previous,
                refreshState: willRetry ? 'retrying' : 'stale',
                inspectionRequest: inspect ? 'error' : undefined,
              }
            : {
                conversationId,
                status: 'error',
                refreshState: willRetry ? 'retrying' : 'stale',
                inspectionRequest: inspect ? 'error' : undefined,
              }
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
  }, [conversationId, visible, active, refresh, inspect, scopeKey, accountScope, requestMode]);
  const state: ContextUsageState =
    ownedState.scope === scopeKey ? ownedState.value : { conversationId, status: 'loading' };
  const inspection = state.status === 'available' || state.status === 'unavailable' ? state.inspection : undefined;
  return {
    // The inspector renders before the mode-change effect starts its request.
    // A warm summary is still valid, but is not a completed inspection read.
    state:
      inspect && ownedState.requestMode !== requestMode && !inspection
        ? { ...state, inspectionRequest: 'loading' as const }
        : state,
    scopeKey,
    retry,
  };
}
