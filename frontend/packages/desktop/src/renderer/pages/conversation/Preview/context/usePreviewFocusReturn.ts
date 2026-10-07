import { useCallback, useEffect, useRef } from 'react';

/** Ephemeral DOM references only; never persisted with artifact content. A
 * newer open cancels a queued close restoration, so it cannot steal focus. */
export function usePreviewFocusReturn(tabIds?: readonly string[]) {
  const openers = useRef(new Map<string, HTMLElement>());
  const lastOpener = useRef<HTMLElement | null>(null);
  const pending = useRef<number | null>(null);
  const cancelPending = useCallback(() => {
    if (pending.current !== null) cancelAnimationFrame(pending.current);
    pending.current = null;
  }, []);
  useEffect(
    () => () => {
      cancelPending();
      openers.current.clear();
      lastOpener.current = null;
    },
    [cancelPending]
  );
  useEffect(() => {
    if (!tabIds) return;
    const liveIds = new Set(tabIds);
    for (const id of openers.current.keys()) if (!liveIds.has(id)) openers.current.delete(id);
  }, [tabIds]);
  const remember = useCallback(
    (id: string, opener: HTMLElement | null) => {
      cancelPending();
      // Opening a version or companion file from inside the preview must not
      // replace its return destination with a control that closes with it.
      if (opener && opener !== document.body && !opener.closest('.preview-panel')) lastOpener.current = opener;
      const destination = openers.current.get(id) ?? lastOpener.current;
      if (opener && opener !== document.body && !opener.closest('.preview-panel')) openers.current.set(id, opener);
      else if (destination) openers.current.set(id, destination);
    },
    [cancelPending]
  );
  const restore = useCallback(
    (id: string | null, all = false) => {
      const opener = id ? openers.current.get(id) : null;
      if (all) {
        openers.current.clear();
        lastOpener.current = null;
      } else if (id) openers.current.delete(id);
      cancelPending();
      if (!opener) return;
      const closingFocus = document.activeElement;
      pending.current = requestAnimationFrame(() => {
        pending.current = null;
        const current = document.activeElement;
        if (current !== closingFocus && current !== document.body && current?.isConnected) return;
        // Closing one of several files keeps focus in the surviving workspace;
        // only closing the final panel returns to its external opener.
        const panel = document.querySelector<HTMLElement>('[data-preview-focus-target]');
        const destination = panel ?? opener;
        if (destination.isConnected && !destination.matches(':disabled')) destination.focus({ preventScroll: true });
      });
    },
    [cancelPending]
  );
  return { remember, restore };
}
