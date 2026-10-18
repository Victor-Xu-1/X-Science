import { useCallback, useLayoutEffect, useRef } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

type Opening = {
  identity: string;
  account: string;
  active: boolean;
  requests: Map<string, AbortController>;
};

export type PreviewArtifactRequest = {
  signal: AbortSignal;
  isCurrent: () => boolean;
  finish: () => void;
};

/** One mounted opening owns its callbacks, not the accepted server operation. */
export function usePreviewArtifactActionScope(identity: string) {
  const opening = useRef<Opening | null>(null);
  const currentIdentity = useRef(identity);
  currentIdentity.current = identity;

  useLayoutEffect(() => {
    // Allocate on every actual activation, including StrictMode effect replay.
    // An old token can never become current just because the same IDs reopen.
    const scope: Opening = {
      identity,
      account: getRendererAccountScopeToken(),
      active: true,
      requests: new Map(),
    };
    opening.current = scope;
    return () => {
      scope.active = false;
      for (const controller of scope.requests.values()) controller.abort();
      scope.requests.clear();
      if (opening.current === scope) opening.current = null;
    };
  }, [identity]);

  const begin = useCallback((channel: string, exclusive = false): PreviewArtifactRequest | null => {
    const scope = opening.current;
    if (
      !scope?.active ||
      scope.identity !== currentIdentity.current ||
      scope.account !== getRendererAccountScopeToken()
    )
      return null;
    const previous = scope.requests.get(channel);
    if (exclusive && previous) return null;
    previous?.abort();
    const controller = new AbortController();
    scope.requests.set(channel, controller);
    return {
      signal: controller.signal,
      isCurrent: () =>
        opening.current === scope &&
        scope.active &&
        scope.identity === currentIdentity.current &&
        scope.account === getRendererAccountScopeToken() &&
        scope.requests.get(channel) === controller &&
        !controller.signal.aborted,
      finish: () => {
        if (scope.requests.get(channel) === controller) scope.requests.delete(channel);
      },
    };
  }, []);

  const invalidate = useCallback((channel: string) => {
    const scope = opening.current;
    scope?.requests.get(channel)?.abort();
    scope?.requests.delete(channel);
  }, []);

  return { begin, invalidate };
}
