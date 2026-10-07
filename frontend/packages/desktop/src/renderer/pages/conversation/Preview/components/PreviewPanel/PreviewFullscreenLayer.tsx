import { containTabFocus, focusableElements, restoreScopedFocus } from '@/renderer/utils/focusScope';
import React, { useCallback, useLayoutEffect, useRef } from 'react';
import {
  PreviewTransitionSnapshot,
  restorePreviewScroll,
  retainPreviewGrid,
  type PreviewScrollSnapshot,
} from './PreviewTransitionSnapshot';

let activeScrollLocks = 0;
let savedBodyOverflow = '';
const activeScopes = new Set<HTMLElement>();
function isTopmostScope(root: HTMLElement | null): boolean {
  const leaves = [...activeScopes].filter(
    (scope) => ![...activeScopes].some((other) => other !== scope && scope.contains(other))
  );
  return !!root && leaves[leaves.length - 1] === root;
}
function lockBodyScroll(): () => void {
  if (activeScrollLocks++ === 0) {
    savedBodyOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  }
  let released = false;
  return () => {
    if (released) return;
    released = true;
    if (--activeScrollLocks === 0) document.body.style.overflow = savedBodyOverflow;
  };
}

/** Fullscreen changes presentation only. The original DOM ancestry remains
 * stable, including browser-owned iframe documents and media sessions. */
export const PreviewFullscreenLayer: React.FC<
  React.PropsWithChildren<{
    active: boolean;
    label?: string;
    onExit?: () => void;
  }>
> = ({ active, label, onExit, children }) => {
  const contentRef = useRef<HTMLDivElement>(null);
  const openerRef = useRef<HTMLElement | null>(null);
  const scrollSnapshotRef = useRef<PreviewScrollSnapshot>({ scroll: [], grid: null });
  const captureSnapshot = useCallback((snapshot: PreviewScrollSnapshot) => {
    scrollSnapshotRef.current = snapshot;
  }, []);
  const onExitRef = useRef(onExit);
  onExitRef.current = onExit;
  useLayoutEffect(() => {
    const root = contentRef.current;
    if (!root) return;
    const current = document.activeElement;
    if (active && current instanceof HTMLElement && current !== document.body) openerRef.current = current;
    const releaseGrid = active ? retainPreviewGrid(scrollSnapshotRef.current.grid) : () => {};
    restorePreviewScroll(root, scrollSnapshotRef.current);
    scrollSnapshotRef.current = { scroll: [], grid: null };
    if (!active) {
      restoreScopedFocus(openerRef.current, root);
      openerRef.current = null;
      return;
    }
    activeScopes.add(root);
    const unlock = lockBodyScroll();
    (focusableElements(contentRef.current)[0] ?? contentRef.current)?.focus({ preventScroll: true });
    return () => {
      unlock();
      releaseGrid();
      activeScopes.delete(root);
      const opener = openerRef.current;
      if (opener && !root.contains(opener)) restoreScopedFocus(opener, root);
    };
  }, [active]);
  useLayoutEffect(() => {
    if (!active) return;
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return;
      if (!isTopmostScope(contentRef.current)) return;
      const target = event.target instanceof Element ? event.target : document.activeElement;
      if (
        target instanceof Element &&
        !contentRef.current?.contains(target) &&
        target.closest('[role="dialog"], [role="alertdialog"], [role="menu"]')
      )
        return;
      if (onExitRef.current) {
        event.preventDefault();
        onExitRef.current();
      }
    };
    const handleFocus = (event: FocusEvent) => {
      const root = contentRef.current;
      if (!isTopmostScope(root) || !(event.target instanceof Element) || root?.contains(event.target)) return;
      // Arco dialogs/menus may own a legitimate child portal. Plain outside
      // focus (including Tab leaving an iframe) must remain inside this modal.
      if (event.target.closest('[role="dialog"], [role="alertdialog"], [role="menu"]')) return;
      (focusableElements(root)[0] ?? root)?.focus({ preventScroll: true });
    };
    window.addEventListener('keydown', handleEscape);
    document.addEventListener('focusin', handleFocus);
    return () => {
      window.removeEventListener('keydown', handleEscape);
      document.removeEventListener('focusin', handleFocus);
    };
  }, [active]);

  return (
    <PreviewTransitionSnapshot active={active} onSnapshot={captureSnapshot}>
      <div
        ref={contentRef}
        style={{ display: 'contents' }}
        tabIndex={-1}
        data-preview-fullscreen-scope={active ? '' : undefined}
        role={active ? 'dialog' : undefined}
        aria-modal={active || undefined}
        aria-label={active ? label : undefined}
        onKeyDown={(event) => {
          if (!active || event.defaultPrevented || !event.currentTarget.contains(event.target as Node)) return;
          if (!isTopmostScope(contentRef.current)) return;
          if (event.key === 'Escape' && onExit) {
            event.preventDefault();
            onExit();
            return;
          }
          containTabFocus(contentRef.current, event);
        }}
      >
        {children}
      </div>
    </PreviewTransitionSnapshot>
  );
};
