import { useEffect, useMemo, useSyncExternalStore } from 'react';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { EMPTY_RESOURCE_TAG_VIEW, getResourceTagOwner } from '@/renderer/services/runtime/resourceTagOwner';

const emptySubscribe = () => (): void => undefined;
const emptySnapshot = () => EMPTY_RESOURCE_TAG_VIEW;
const rejectStaleAccount = async <T = void>(): Promise<T> => {
  throw new Error('Resource tag account changed');
};

/** One account owner is shared by table and details; closing a menu does not cancel accepted work. */
export function useResourceTags(account = getRendererAccountScopeToken()) {
  const currentScope = getRendererAccountScopeToken();
  const owner = useMemo(
    () => (account === currentScope && getRendererAccountOwnerId() ? getResourceTagOwner(account) : null),
    [account, currentScope]
  );
  const view = useSyncExternalStore(
    owner?.subscribe ?? emptySubscribe,
    owner?.getSnapshot ?? emptySnapshot,
    owner?.getSnapshot ?? emptySnapshot
  );
  useEffect(() => {
    owner?.start();
  }, [owner]);
  return {
    ...(owner && account === getRendererAccountScopeToken() ? view : EMPTY_RESOURCE_TAG_VIEW),
    reload: owner?.reload ?? rejectStaleAccount,
    retry: owner?.retry ?? rejectStaleAccount,
    assign: owner?.assign ?? rejectStaleAccount,
    createAndAssign: owner?.createAndAssign ?? rejectStaleAccount,
    create: owner?.create ?? rejectStaleAccount<string>,
    catalog: owner?.catalog ?? rejectStaleAccount<string | undefined>,
    update: owner?.update ?? rejectStaleAccount,
    delete: owner?.delete ?? rejectStaleAccount,
    reorder: owner?.reorder ?? rejectStaleAccount,
  };
}
