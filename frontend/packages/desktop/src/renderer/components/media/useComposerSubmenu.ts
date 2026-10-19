import { useCallback, useEffect, useId, useRef, type KeyboardEvent } from 'react';
import { getMenuItems, handleMenuNavigation } from '@/renderer/utils/menuKeyboard';

function focusPanel(panel: HTMLElement | null) {
  (panel?.querySelector<HTMLElement>('input:not(:disabled)') ?? getMenuItems(panel)[0])?.focus();
}

/** Focus plumbing only: the capability picker remains the submenu state owner. */
export function useComposerSubmenu(open: boolean, onOpenChange: (open: boolean) => void) {
  const panelId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const focusPending = useRef(false);
  useEffect(() => {
    if (!open) focusPending.current = false;
  }, [open]);
  const bindPanel = useCallback(
    (node: HTMLDivElement | null) => {
      panelRef.current = node;
      if (node && open && focusPending.current) {
        focusPending.current = false;
        focusPanel(node);
      }
    },
    [open]
  );
  const closeToTrigger = () => {
    focusPending.current = false;
    onOpenChange(false);
    queueMicrotask(() => triggerRef.current?.focus());
  };
  const openFromKeyboard = () => {
    focusPending.current = true;
    onOpenChange(true);
    if (open) {
      focusPending.current = false;
      focusPanel(panelRef.current);
    }
  };
  return {
    triggerRef,
    triggerProps: {
      'aria-haspopup': 'menu' as const,
      'aria-expanded': open,
      'aria-controls': open ? panelId : undefined,
      onClick: () => openFromKeyboard(),
      onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => {
        if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
        if (!['ArrowRight', 'ArrowDown', 'Enter', ' '].includes(event.key)) return;
        event.preventDefault();
        event.stopPropagation();
        openFromKeyboard();
      },
    },
    panelProps: {
      id: panelId,
      ref: bindPanel,
      onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
        if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
        const editing =
          event.target instanceof Element && event.target.closest('input, textarea, [contenteditable="true"]');
        if (event.key === 'Escape' || (event.key === 'ArrowLeft' && !editing)) {
          event.preventDefault();
          event.stopPropagation();
          closeToTrigger();
          return;
        }
        if (editing) {
          if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            const items = getMenuItems(event.currentTarget);
            const target = event.key === 'ArrowDown' ? items[0] : items.at(-1);
            if (target) {
              event.preventDefault();
              event.stopPropagation();
              target.focus();
            }
          }
          return;
        }
        handleMenuNavigation(event);
      },
    },
  };
}
