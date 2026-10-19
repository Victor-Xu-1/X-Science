import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { loadXScienceMemoryEnabled } from '@/renderer/services/xScienceMemory';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';

export type XScienceMemoryAvailability =
  | { status: 'loading' }
  | { status: 'unavailable' }
  | { status: 'ready'; enabled: boolean };

export type XScienceMemoryControl = {
  availability: XScienceMemoryAvailability;
  refresh: () => void;
  current: () => boolean;
  canUpdate: () => boolean;
};

const loading: XScienceMemoryAvailability = { status: 'loading' };
const REQUEST_TIMEOUT_MS = 8_000;

/** An observed global/user capability, not another session preference or permission store. */
export function useXScienceMemoryAvailability(ownerKey: string): XScienceMemoryControl {
  const account = getRendererAccountScopeToken();
  const owner = useMemo(() => ({ account, ownerKey }), [account, ownerKey]);
  const ownerRef = useRef(owner);
  ownerRef.current = owner;
  const mounted = useRef(false);
  const pending = useRef<AbortController | null>(null);
  const resetId = useId();
  const latest = useRef<{ owner: typeof owner; availability: XScienceMemoryAvailability }>({
    owner,
    availability: loading,
  });
  const [snapshot, setSnapshot] = useState(latest.current);

  const current = useCallback(
    () => mounted.current && ownerRef.current === owner && getRendererAccountScopeToken() === owner.account,
    [owner]
  );
  const refresh = useCallback(() => {
    if (!current()) return;
    pending.current?.abort();
    const request = new AbortController();
    pending.current = request;
    latest.current = { owner, availability: loading };
    setSnapshot(latest.current);
    const publish = (availability: XScienceMemoryAvailability) => {
      if (!current() || pending.current !== request) return;
      latest.current = { owner, availability };
      setSnapshot(latest.current);
    };
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      request.abort();
      publish({ status: 'unavailable' });
    }, REQUEST_TIMEOUT_MS);
    request.signal.addEventListener('abort', () => clearTimeout(timer), { once: true });
    void loadXScienceMemoryEnabled({ signal: request.signal })
      .then((enabled) => {
        if (!request.signal.aborted) publish({ status: 'ready', enabled });
      })
      .catch(() => {
        if (!request.signal.aborted || timedOut) publish({ status: 'unavailable' });
      })
      .finally(() => {
        clearTimeout(timer);
        if (pending.current === request) pending.current = null;
      });
  }, [current, owner]);

  useEffect(() => {
    mounted.current = true;
    refresh();
    window.addEventListener('focus', refresh);
    const unregister = registerRendererAccountReset(`composer-memory:${resetId}`, () => {
      pending.current?.abort();
      setSnapshot({ owner, availability: loading });
    });
    return () => {
      mounted.current = false;
      pending.current?.abort();
      pending.current = null;
      window.removeEventListener('focus', refresh);
      unregister();
    };
  }, [owner, refresh, resetId]);

  return {
    availability: snapshot.owner === owner ? snapshot.availability : loading,
    current,
    refresh,
    canUpdate: () =>
      current() &&
      latest.current.owner === owner &&
      latest.current.availability.status === 'ready' &&
      latest.current.availability.enabled,
  };
}

/** Both presentations derive effective display without ever replacing the retained preference. */
export function sessionMemoryPresentation(control: XScienceMemoryControl | undefined, preference: boolean) {
  const availability = control?.availability ?? loading;
  return {
    checked: availability.status === 'ready' ? availability.enabled && preference : undefined,
    disabled: availability.status !== 'ready' || !availability.enabled,
    reason:
      availability.status === 'loading'
        ? 'conversation.xscienceRuntime.sessionOptions.memoryAvailabilityLoading'
        : availability.status === 'unavailable'
          ? 'conversation.xscienceRuntime.sessionOptions.memoryAvailabilityUnavailable'
          : !availability.enabled
            ? 'conversation.xscienceRuntime.sessionOptions.memoryDisabledInSettings'
            : undefined,
  } as const;
}
