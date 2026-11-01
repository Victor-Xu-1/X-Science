import { withRendererMutationLock } from './rendererMutationSlot';

/** Keep the original Library lock key, account fence and error vocabulary. */
export function withLiteratureMutationLock<T>(key: string, current: () => boolean, critical: () => T): Promise<T> {
  return withRendererMutationLock(key, current, critical, {
    label: 'Library',
    resetKeyPrefix: 'literature-mutation-lock',
  });
}
