import { useEffect, useRef } from 'react';
import type React from 'react';
import { useLibraryActionMenu } from './useLibraryActionMenu';
/** Display-only hover timing. Existing Library menu owns dismissal and Enter dispatch. */
export function useLibraryExportMenu(disabled: boolean) {
  const menu = useLibraryActionMenu(disabled);
  const content = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const pinned = useRef(false);
  const clear = () => {
    clearTimeout(timer.current);
    timer.current = undefined;
  };
  const pin = () => {
    pinned.current = true;
    clear();
  };
  useEffect(() => () => clearTimeout(timer.current), [disabled]);
  useEffect(() => {
    if (menu.visible && pinned.current) content.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }, [menu.visible]);
  const enter = (event: React.PointerEvent) => {
    if (disabled || event.pointerType === 'touch' || menu.visible) return;
    clear();
    pinned.current = false;
    timer.current = setTimeout(() => menu.visibility(true), 120);
  };
  const leave = () => {
    clear();
    if (!pinned.current) timer.current = setTimeout(() => menu.visibility(false), 180);
  };
  const keyboard = (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {
      clear();
      menu.escape(event);
      return;
    }
    pin();
    if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      menu.visibility(true);
    }
  };
  return {
    ...menu,
    content,
    enter,
    leave,
    clear,
    pin,
    keyboard,
    visibility: (open: boolean) => {
      clear();
      if (open) pin();
      menu.visibility(open);
    },
  };
}
