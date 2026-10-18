import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import { DelegatedStopController } from './delegatedStopController';
import type { DelegatedStopIdentity } from './delegatedStopTypes';

const controllers = new Map<string, DelegatedStopController>();
registerRendererAccountReset('delegated-stop-controllers', () => {
  controllers.forEach((controller) => controller.dispose());
  controllers.clear();
});
export function acquireDelegatedStopController(identity: DelegatedStopIdentity): DelegatedStopController | null {
  if (
    !identity.ownerId ||
    !identity.parentId ||
    !identity.frameIncarnationId ||
    getRendererAccountOwnerId() !== identity.ownerId
  )
    return null;
  const key = JSON.stringify([getRendererAccountScopeToken(), identity.parentId, identity.frameIncarnationId]);
  const existing = controllers.get(key);
  if (existing) return existing;
  if (controllers.size >= 32) {
    for (const [candidate, controller] of controllers) {
      if (controller.canRelease()) {
        controller.dispose();
        controllers.delete(candidate);
      }
    }
  }
  if (controllers.size >= 32) return null;
  const controller = new DelegatedStopController(identity);
  controllers.set(key, controller);
  return controller;
}
