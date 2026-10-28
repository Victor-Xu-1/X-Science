import type React from 'react';
import { useCallback, useEffect, useLayoutEffect, useRef } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { focusableElements } from '@/renderer/utils/focusScope';

/** Local dismissal state only; URL, reads, pending writes and Arco focus lock retain their owners. */
export function useLibraryDetailDialog(account: string, phase: string, onClose: () => void) {
  const mounted = useRef(true);
  const nativeSelect = useRef<HTMLSelectElement | null>(null);
  const content = useRef<HTMLDivElement | null>(null);
  const initialized = useRef(false);
  const focus = useCallback(() => {
    const root = content.current?.closest<HTMLElement>('[role="dialog"]');
    if (!mounted.current || account !== getRendererAccountScopeToken() || !root?.isConnected) return;
    const active = document.activeElement;
    if (root.contains(active)) {
      initialized.current = true;
      return;
    }
    // Arco's default wrapper focus also depends on escToExit. This caller owns
    // only initial focus and a disappeared loading control; Arco keeps the trap.
    if (!initialized.current || active === document.body || !active?.isConnected) {
      (focusableElements(root)[0] ?? root).focus({ preventScroll: true });
      initialized.current = root.contains(document.activeElement);
    }
  }, [account]);
  useLayoutEffect(focus, [focus, phase]);
  const contentRef = useCallback(
    (node: HTMLDivElement | null) => {
      content.current = node;
      if (node) focus();
    },
    [focus]
  );
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      nativeSelect.current = null;
    };
  }, []);
  const close = useCallback(() => {
    if (mounted.current && account === getRendererAccountScopeToken()) onClose();
  }, [account, onClose]);
  const keyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.defaultPrevented || event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
    const target = event.target instanceof Element ? event.target : undefined;
    const select = target?.closest('select');
    if (
      select instanceof HTMLSelectElement &&
      (event.key === ' ' ||
        event.key === 'Enter' ||
        event.key === 'F4' ||
        (event.altKey && (event.key === 'ArrowDown' || event.key === 'ArrowUp')))
    )
      nativeSelect.current = select;
    if (event.key !== 'Escape') return;
    if (target?.closest('[role="dialog"], [role="alertdialog"]') !== event.currentTarget) return;
    if (select && nativeSelect.current === select) {
      nativeSelect.current = null;
      // The native popup still needs its default Escape. Consume only the parent dismissal.
      event.stopPropagation();
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    close();
  };
  const clearSelect = () => {
    nativeSelect.current = null;
  };
  return {
    close,
    focus,
    contentRef,
    events: {
      onKeyDown: keyDown,
      onPointerDownCapture: (event: React.PointerEvent<HTMLDivElement>) => {
        const select = event.target instanceof Element ? event.target.closest('select') : null;
        nativeSelect.current = select instanceof HTMLSelectElement ? select : null;
      },
      onChangeCapture: clearSelect,
      onBlurCapture: clearSelect,
    },
  };
}
