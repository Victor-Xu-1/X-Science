import { useEffect, useId, useMemo, useState, useSyncExternalStore } from 'react';
import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '@/renderer/services/rendererAccountScope';
import {
  getLiteratureMutationController,
  type LiteratureMutationView,
} from '@/renderer/services/runtime/LiteratureMutationController';

type ViewProjection = (next: LiteratureMutationView, previous: LiteratureMutationView) => LiteratureMutationView;
const identityView: ViewProjection = (next) => next;

export function useLiteratureMutation(project: ViewProjection = identityView) {
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const key = useId();
  useEffect(
    () => registerRendererAccountReset(`library-ui:${key}`, () => setAccount(getRendererAccountScopeToken())),
    [key]
  );
  const controller = useMemo(() => getLiteratureMutationController(getRendererAccountOwnerId()), [account]);
  // A projection cache belongs to this exact account controller and pure selector.
  // The default returns the controller's original snapshot without alteration.
  const snapshot = useMemo(() => {
    let previous = controller.getSnapshot();
    return () => {
      previous = project(controller.getSnapshot(), previous);
      return previous;
    };
  }, [controller, project]);
  const view = useSyncExternalStore(controller.subscribe, snapshot, snapshot);
  return { controller, view };
}
