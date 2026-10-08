import type React from 'react';

/** Only items belonging to this menu participate; nested menus own their focus. */
export function getMenuItems(menu: HTMLElement | null): HTMLElement[] {
  if (!menu) return [];
  return [...menu.querySelectorAll<HTMLElement>('[role^="menuitem"]')].filter(
    (item) =>
      item.closest('[role="menu"]') === menu &&
      !item.matches(':disabled, [aria-disabled="true"]') &&
      !item.closest('[hidden], [inert], [aria-hidden="true"]')
  );
}

export function handleMenuNavigation(event: React.KeyboardEvent<HTMLElement>): boolean {
  if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return false;
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return false;
  const menu = event.currentTarget;
  if (event.target instanceof Element && event.target.closest('[role="menu"]') !== menu) return false;
  const items = getMenuItems(menu);
  if (!items.length) return false;
  const index = items.indexOf(document.activeElement as HTMLElement);
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? items.length - 1
        : event.key === 'ArrowDown'
          ? (index + 1) % items.length
          : index < 0
            ? items.length - 1
            : (index + items.length - 1) % items.length;
  event.preventDefault();
  event.stopPropagation();
  items[next].focus();
  return true;
}
