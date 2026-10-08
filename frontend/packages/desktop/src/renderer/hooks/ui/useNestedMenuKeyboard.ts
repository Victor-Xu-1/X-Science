import { useCallback, useId, useLayoutEffect, useRef } from 'react';
import type React from 'react';
import { getMenuItems, handleMenuNavigation } from '@/renderer/utils/menuKeyboard';

/** Keyboard presentation for a controlled menu. The owner retains selection,
 * request authority and hover timing; this hook never changes an option. */
export function useNestedMenuKeyboard<T extends string>({
  open,
  setOpen,
  submenu,
  setSubmenu,
  cancelHoverClose,
}: {
  open: boolean;
  setOpen: (open: boolean) => void;
  submenu: T | null;
  setSubmenu: (submenu: T | null) => void;
  cancelHoverClose: () => void;
}) {
  const menuId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const submenuRef = useRef<HTMLDivElement>(null);
  const focusRequest = useRef<'first' | 'last' | null>(null);
  const submenuFocusRequest = useRef(false);
  const applyFocusRequest = useCallback((menu: HTMLElement | null) => {
    if (!focusRequest.current) return;
    const items = getMenuItems(menu);
    const item = focusRequest.current === 'last' ? items.at(-1) : items[0];
    if (item) {
      focusRequest.current = null;
      item.focus();
    }
  }, []);
  const bindMenu = useCallback(
    (menu: HTMLDivElement | null) => {
      menuRef.current = menu;
      if (open) applyFocusRequest(menu);
    },
    [applyFocusRequest, open]
  );
  useLayoutEffect(() => {
    if (open) applyFocusRequest(menuRef.current);
  }, [applyFocusRequest, open]);
  useLayoutEffect(() => {
    if (submenu && submenuFocusRequest.current) {
      const item = getMenuItems(submenuRef.current)[0];
      if (item) {
        submenuFocusRequest.current = false;
        item.focus();
      }
    }
  }, [submenu]);
  const close = () => {
    cancelHoverClose();
    focusRequest.current = null;
    submenuFocusRequest.current = false;
    setOpen(false);
    setSubmenu(null);
    triggerRef.current?.focus();
  };
  const onTriggerKeyDown = (event: React.KeyboardEvent<HTMLElement>, disabled: boolean) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    if (disabled) return;
    focusRequest.current = event.key === 'ArrowUp' ? 'last' : 'first';
    if (open) applyFocusRequest(menuRef.current);
    else setOpen(true);
  };
  const onMenuKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (handleMenuNavigation(event)) return;
    if (event.target instanceof Element && event.target.closest('[role="menu"]') !== event.currentTarget) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      close();
    } else if (event.key === 'Tab') close();
  };
  const onSubmenuKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (handleMenuNavigation(event)) return;
    if (event.key === 'Escape' || event.key === 'ArrowLeft') {
      event.preventDefault();
      event.stopPropagation();
      cancelHoverClose();
      const parent = getMenuItems(menuRef.current).find((item) => item.dataset.submenu === submenu);
      setSubmenu(null);
      parent?.focus();
    } else if (event.key === 'Tab') close();
  };
  const onSubmenuTriggerKeyDown = (event: React.KeyboardEvent<HTMLElement>, key: T) => {
    if (event.key !== 'ArrowRight') return;
    event.preventDefault();
    event.stopPropagation();
    cancelHoverClose();
    submenuFocusRequest.current = true;
    if (submenu === key) {
      submenuFocusRequest.current = false;
      getMenuItems(submenuRef.current)[0]?.focus();
    } else setSubmenu(key);
  };
  return {
    menuId,
    triggerRef,
    menuRef,
    submenuRef,
    bindMenu,
    onTriggerKeyDown,
    onMenuKeyDown,
    onSubmenuKeyDown,
    onSubmenuTriggerKeyDown,
  };
}
