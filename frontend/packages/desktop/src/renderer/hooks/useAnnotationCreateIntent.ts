import { useEffect, useId, useState, useSyncExternalStore } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import type { XScienceAnnotationTarget } from '@/renderer/services/xScienceAnnotations';
import { annotationTargetIdentity } from '@/renderer/services/runtime/annotationCreateIntent';
import { getAnnotationCreateOwner } from '@/renderer/services/runtime/annotationCreateRegistry';

export function useAnnotationCreateIntent(target: XScienceAnnotationTarget) {
  const [account, setAccount] = useState(getRendererAccountScopeToken);
  const id = useId();
  const targetId = annotationTargetIdentity(target);
  useEffect(
    () =>
      registerRendererAccountReset(`annotation-create-view:${id}`, () => setAccount(getRendererAccountScopeToken())),
    [id]
  );
  const owner = getAnnotationCreateOwner(target);
  const view = useSyncExternalStore(owner.subscribe, owner.getSnapshot, owner.getSnapshot);
  useEffect(() => {
    owner.restore();
    if (typeof window === 'undefined') return;
    const changed = (event: StorageEvent) => {
      if (event.key === owner.storageKey) owner.restore();
    };
    window.addEventListener('storage', changed);
    return () => window.removeEventListener('storage', changed);
  }, [owner]);
  return { owner, view, scopeKey: JSON.stringify([account, targetId]), retryFrozen: owner.retryFrozen };
}
