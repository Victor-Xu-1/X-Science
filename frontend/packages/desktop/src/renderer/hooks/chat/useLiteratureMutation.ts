import { useEffect, useId, useMemo, useState, useSyncExternalStore } from 'react';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';

export function useLiteratureMutation() {
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const key = useId();
  useEffect(
    () => registerRendererAccountReset(`library-ui:${key}`, () => setAccount(getRendererAccountScopeToken())),
    [key]
  );
  const controller = useMemo(() => getLiteratureMutationController(getRendererAccountOwnerId()), [account]);
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  return { controller, view };
}
