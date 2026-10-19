import { useCallback, useLayoutEffect, useRef } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

/** Fences asynchronous UI updates; it grants no filesystem or send authority. */
export function useComposerLifetime(ownerId: string, draftGeneration: string | number = 0) {
  const scope = useRef<object | null>(null);
  useLayoutEffect(() => {
    const token = {};
    scope.current = token;
    return () => {
      if (scope.current === token) scope.current = null;
    };
  }, [ownerId, draftGeneration]);
  return useCallback(() => {
    const token = scope.current;
    const account = getRendererAccountScopeToken();
    return () => token !== null && scope.current === token && getRendererAccountScopeToken() === account;
  }, [ownerId, draftGeneration]);
}
