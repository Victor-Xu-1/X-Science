import { useEffect, useMemo, useSyncExternalStore } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { sessionGrantIdentifier, type SessionGrantIdentity } from '@/renderer/services/runtime/sessionPermissionGrants';
import {
  EMPTY_SESSION_GRANTS_VIEW,
  SessionPermissionGrantsController,
} from '@/renderer/services/runtime/sessionPermissionGrantsController';

export function useSessionPermissionGrantsController(identity?: SessionGrantIdentity | null) {
  const account = getRendererAccountScopeToken();
  const ownerId = identity?.ownerId;
  const frameId = identity?.frameId;
  const frameIncarnationId = identity?.frameIncarnationId;
  const rootFrameId = identity?.rootFrameId;
  const controller = useMemo(() => {
    if (![ownerId, frameId, frameIncarnationId, rootFrameId].every(sessionGrantIdentifier)) return null;
    return new SessionPermissionGrantsController({
      ownerId,
      frameId,
      frameIncarnationId,
      rootFrameId,
    } as SessionGrantIdentity);
  }, [account, ownerId, frameId, frameIncarnationId, rootFrameId]);
  useEffect(() => controller?.attach(), [controller]);
  return controller;
}
const noSubscribe = () => () => {};
const emptySnapshot = () => EMPTY_SESSION_GRANTS_VIEW;
export function useSessionPermissionGrantsView(controller?: SessionPermissionGrantsController | null) {
  return useSyncExternalStore(
    controller?.subscribe ?? noSubscribe,
    controller?.getSnapshot ?? emptySnapshot,
    emptySnapshot
  );
}
