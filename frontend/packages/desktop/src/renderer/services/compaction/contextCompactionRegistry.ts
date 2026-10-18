import { getRendererAccountScopeToken, registerRendererAccountReset } from '../rendererAccountScope';
import { ContextCompactionController } from './ContextCompactionController';
import type { CompactionIdentity } from './contextCompactionTypes';

// Bounded renderer-local retry identity, not durable execution state. Canonical GET
// restores accepted maintenance after reload; no browser persistence grants authority.
const controllers = new Map<string, ContextCompactionController>();
export function getContextCompactionController(identity: CompactionIdentity) {
  const key = JSON.stringify([getRendererAccountScopeToken(), identity]);
  const existing = controllers.get(key);
  if (existing) return existing;
  for (const [oldKey, controller] of controllers) {
    if (!controller.attached && !controller.retained) controllers.delete(oldKey);
  }
  if (controllers.size >= 32) return null;
  const controller = new ContextCompactionController(identity);
  controllers.set(key, controller);
  return controller;
}
registerRendererAccountReset('context-compaction', () => {
  controllers.forEach((controller) => controller.suspend());
  controllers.clear();
});
