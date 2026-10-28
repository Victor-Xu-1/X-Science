/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Defer a root cleanup until the current React commit stack has completed.
 * Vite can dispose an entry while React is rendering during HMR; synchronous
 * root.unmount() in that window can race React's DOM deletion pass.
 */
export function scheduleReactRootCleanup(cleanup: () => void): void {
  queueMicrotask(cleanup);
}

type RootLifecycleOptions = {
  unmount: () => void;
  disposeResources: () => void;
  target?: Pick<Window, 'addEventListener' | 'removeEventListener'>;
};

/** One owner for permanent renderer cleanup, including deferred HMR teardown. */
export function installReactRootLifecycle({ unmount, disposeResources, target = window }: RootLifecycleOptions) {
  let disposed = false;
  const detach = (): boolean => {
    if (disposed) return false;
    disposed = true;
    target.removeEventListener('pagehide', onPageHide);
    return true;
  };
  const dispose = () => {
    if (!detach()) return;
    try {
      disposeResources();
    } finally {
      unmount();
    }
  };
  const onPageHide: EventListener = (event) => {
    // Cached documents resume with this root and its state; pageshow does not
    // execute the entry again. Keep listening for their later permanent exit.
    if ((event as PageTransitionEvent).persisted === true) return;
    dispose();
  };
  target.addEventListener('pagehide', onPageHide);
  return {
    dispose,
    disposeForHotReload: () => {
      if (!detach()) return;
      try {
        disposeResources();
      } finally {
        scheduleReactRootCleanup(unmount);
      }
    },
  };
}
