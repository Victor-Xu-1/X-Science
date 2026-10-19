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

/** Root menus focus their surface for pointer opens, and an item for keyboard opens. */
export function focusMenuSurface(menu: HTMLElement | null, position: 'first' | 'last' | null): boolean {
  const items = position === null ? [] : getMenuItems(menu);
  const target = position === null ? menu : position === 'last' ? items.at(-1) : items[0];
  target?.focus({ preventScroll: true });
  return Boolean(target && target.ownerDocument.activeElement === target);
}

/** Arco keeps cached menus mounted while their popup ancestor is display:none.
 * Wait only for those already-hidden ancestors, and only for this opening. */
function isHiddenMenuAncestor(node: HTMLElement): boolean {
  const style = getComputedStyle(node);
  return node.hidden || style.display === 'none' || style.visibility === 'hidden';
}

export function focusMenuSurfaceWhenVisible(
  menu: HTMLElement,
  position: 'first' | 'last' | null,
  trigger: HTMLElement | null,
  onFocused: () => void
): () => void {
  const hiddenAncestors: HTMLElement[] = [];
  for (let node = menu.parentElement; node && node !== menu.ownerDocument.body; node = node.parentElement) {
    if (isHiddenMenuAncestor(node)) hiddenAncestors.push(node);
  }
  let active = true;
  let applyingFocus = false;
  let observer: MutationObserver | undefined;
  const cancel = () => {
    active = false;
    observer?.disconnect();
    trigger?.removeEventListener('blur', onTriggerBlur);
  };
  const onTriggerBlur = () => {
    if (!applyingFocus) cancel();
  };
  const apply = () => {
    if (!active) return;
    const focused = menu.ownerDocument.activeElement;
    if (!menu.isConnected || (focused !== trigger && focused !== menu.ownerDocument.body && focused !== menu)) {
      cancel();
      return;
    }
    if (hiddenAncestors.some(isHiddenMenuAncestor)) return;
    applyingFocus = true;
    const succeeded = focusMenuSurface(menu, position);
    applyingFocus = false;
    if (active && succeeded) {
      cancel();
      onFocused();
    }
  };
  if (hiddenAncestors.length) {
    observer = new MutationObserver(apply);
    for (const node of hiddenAncestors)
      observer.observe(node, { attributes: true, attributeFilter: ['style', 'class', 'hidden'] });
    trigger?.addEventListener('blur', onTriggerBlur);
  }
  apply();
  return cancel;
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
