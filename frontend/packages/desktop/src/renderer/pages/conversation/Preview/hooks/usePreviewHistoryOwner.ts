import { useCallback, useEffect, useMemo, useRef } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

export type PreviewHistoryLease = { owns: () => boolean };

/** Receiver leases only: server writes and version admission stay with their
 * existing owners. A new identity is allocated even when A returns after B.
 * Cleanup also invalidates work across StrictMode's setup/cleanup replay. */
export function usePreviewHistoryOwner(ownerKey: string) {
  const owner = useMemo(() => ({}), [ownerKey]);
  const latest = useRef(owner);
  latest.current = owner;
  const account = getRendererAccountScopeToken();
  const lifetime = useRef({ active: false, generation: 0 });

  useEffect(() => {
    lifetime.current.active = true;
    lifetime.current.generation += 1;
    return () => {
      lifetime.current.active = false;
      lifetime.current.generation += 1;
    };
  }, []);

  return useCallback((): PreviewHistoryLease | null => {
    const generation = lifetime.current.generation;
    const owns = () =>
      lifetime.current.active &&
      generation === lifetime.current.generation &&
      owner === latest.current &&
      account === getRendererAccountScopeToken();
    return owns() ? { owns } : null;
  }, [owner, account]);
}
