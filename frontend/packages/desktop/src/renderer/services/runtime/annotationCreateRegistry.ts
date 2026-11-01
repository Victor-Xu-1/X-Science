import {
  getRendererAccountOwnerId,
  getRendererAccountScopeToken,
  registerRendererAccountReset,
} from '../rendererAccountScope';
import type { XScienceAnnotationTarget } from '../xScienceAnnotations';
import { AnnotationCreateIntentOwner } from './AnnotationCreateIntentOwner';
import { annotationTargetIdentity } from './annotationCreateIntent';

const owners = new Map<string, AnnotationCreateIntentOwner>();
registerRendererAccountReset('annotation-create-intent-registry', () => {
  for (const owner of owners.values()) owner.dispose();
  owners.clear();
});
export function getAnnotationCreateOwner(target: XScienceAnnotationTarget): AnnotationCreateIntentOwner {
  // Generation is an in-memory fence only; the owner derives its durable key from stable account+target.
  const scope = JSON.stringify([getRendererAccountScopeToken(), annotationTargetIdentity(target)]);
  let owner = owners.get(scope);
  if (!owner) {
    owner = new AnnotationCreateIntentOwner(getRendererAccountOwnerId(), target);
    owners.set(scope, owner);
  }
  return owner;
}
