import { useCallback, useRef } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

type Visit = { account: string; route: string };
type Origin = { visit: Visit; operationId: string };

/** Only grants local navigation/focus authority; the durable mutation owner settles every ACK. */
export function useLibraryLifecycleVisit(account: string, route: string) {
  const current = useRef<Visit>({ account, route });
  if (current.current.account !== account || current.current.route !== route) current.current = { account, route };
  const visit = current.current;
  const origin = useRef<Origin | undefined>(undefined);
  const capture = useCallback(
    (operationId: string) => {
      // Retrying an immutable operation never transfers its original visit authority.
      if (
        origin.current?.operationId === operationId ||
        current.current !== visit ||
        visit.account !== getRendererAccountScopeToken()
      )
        return;
      origin.current = { visit, operationId };
    },
    [visit]
  );
  const owns = useCallback((operationId: string) => {
    const expected = origin.current;
    return (
      expected?.operationId === operationId &&
      expected.visit === current.current &&
      current.current.account === getRendererAccountScopeToken()
    );
  }, []);
  return { capture, owns };
}
