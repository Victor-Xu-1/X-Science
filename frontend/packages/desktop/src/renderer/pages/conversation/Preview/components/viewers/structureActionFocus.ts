import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { focusableElements, isVisibleFocusable } from '@/renderer/utils/focusScope';

type ActionFocus = {
  root: HTMLElement;
  opener: HTMLElement;
  parents: HTMLElement[];
};

/** One ephemeral owner snapshot, never task data or a document-wide fallback. */
export function captureStructureActionFocus(root: HTMLElement | null): ActionFocus | null {
  const opener = root?.ownerDocument.activeElement;
  if (!root || !(opener instanceof HTMLElement) || !root.contains(opener) || !isVisibleFocusable(opener)) return null;
  const parents: HTMLElement[] = [];
  for (let parent = opener.parentElement; parent && root.contains(parent); parent = parent.parentElement) {
    parents.push(parent);
    if (parent === root) break;
  }
  return { root, opener, parents };
}

export function restoreStructureActionFocus(snapshot: ActionFocus | null, isCurrent: () => boolean): void {
  if (!snapshot || !isCurrent() || !snapshot.root.isConnected) return;
  const { root, opener, parents } = snapshot;
  if (opener.isConnected && !root.contains(opener)) return;
  const current = root.ownerDocument.activeElement;
  // Another destination, including one in this same viewer, wins. A native
  // disabled button can instead leave focus on itself or on BODY.
  if (current !== root.ownerDocument.body && current !== opener && current?.isConnected) return;
  if (opener.isConnected && root.contains(opener) && isVisibleFocusable(opener)) {
    opener.focus({ preventScroll: true });
    return;
  }
  for (const parent of parents) {
    if (!parent.isConnected || !root.contains(parent)) continue;
    const candidates = focusableElements(parent);
    const next = opener.isConnected
      ? candidates.find((node) => opener.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING)
      : candidates[0];
    const destination = next ?? candidates.at(-1);
    if (destination) {
      destination.focus({ preventScroll: true });
      return;
    }
  }
}

/** Restoration is queued by a committed render, not by the engine promise.
 * New actions, file generations and unmount invalidate it instead of retrying. */
export function useStructureActionFocus(sourceKey: readonly unknown[]) {
  const pending = useRef<number | null>(null);
  const queued = useRef<{ snapshot: ActionFocus; isCurrent: () => boolean; sourceKey: readonly unknown[] } | null>(
    null
  );
  const [revision, requestCommit] = useState(0);
  const mounted = useRef(true);
  const currentSource = useRef(sourceKey);
  useLayoutEffect(() => {
    currentSource.current = sourceKey;
  }, [sourceKey]);
  const cancel = useCallback(() => {
    if (pending.current !== null) cancelAnimationFrame(pending.current);
    pending.current = null;
    queued.current = null;
  }, []);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      cancel();
    };
  }, [cancel]);
  useLayoutEffect(() => {
    const request = queued.current;
    queued.current = null;
    if (!request) return;
    pending.current = requestAnimationFrame(() => {
      pending.current = null;
      if (
        mounted.current &&
        request.sourceKey.length === currentSource.current.length &&
        request.sourceKey.every((value, index) => value === currentSource.current[index])
      )
        restoreStructureActionFocus(request.snapshot, request.isCurrent);
    });
  }, [revision]);
  const restore = useCallback(
    (snapshot: ActionFocus | null, isCurrent: () => boolean) => {
      cancel();
      if (!snapshot || !mounted.current) return;
      queued.current = { snapshot, isCurrent, sourceKey };
      requestCommit((value) => value + 1);
    },
    [cancel, sourceKey]
  );
  return { cancel, restore };
}
