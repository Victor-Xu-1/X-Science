import { useEffect, useId, useMemo, useState, useSyncExternalStore } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { getNotebookSessionController } from '@/renderer/services/notebook/NotebookSessionController';
import type { NotebookSessionNavigation } from '@/renderer/services/notebook/manualNotebookTypes';

export function useLiveNotebook(navigation: NotebookSessionNavigation) {
  const id = useId();
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  useEffect(
    () => registerRendererAccountReset(`notebook-view:${id}`, () => setAccount(getRendererAccountScopeToken())),
    [id]
  );
  const controller = useMemo(
    () => getNotebookSessionController(navigation.frameId, navigation.rootFrameId),
    [account, navigation.frameId, navigation.rootFrameId]
  );
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  useEffect(() => {
    controller.navigate(navigation.preferredKernelId, navigation.focusRequest);
  }, [controller, navigation.preferredKernelId, navigation.focusRequest]);
  return { controller, snapshot };
}
