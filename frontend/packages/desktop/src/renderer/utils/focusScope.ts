/** Shared keyboard-focus rules for content dialogs and full-window previews. */
const FOCUSABLE_SELECTOR =
  'a[href], area[href], button, input:not([type="hidden"]), select, textarea, summary, iframe, audio[controls], video[controls], [contenteditable="true"], [tabindex]';

export function restoreScopedFocus(opener: HTMLElement | null, root: HTMLElement | null): void {
  if (
    opener?.isConnected &&
    !opener.matches(':disabled') &&
    !opener.closest('[hidden], [inert], [aria-hidden="true"]') &&
    (document.activeElement === document.body || root?.contains(document.activeElement))
  )
    opener.focus({ preventScroll: true });
}

export function focusableElements(root: HTMLElement | null): HTMLElement[] {
  return [...(root?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? [])].filter((element) => {
    if (
      element.tabIndex < 0 ||
      element.matches(':disabled') ||
      element.closest('[hidden], [inert], [aria-hidden="true"]')
    )
      return false;
    for (let ancestor: HTMLElement | null = element; ancestor && ancestor !== root; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor);
      if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') return false;
    }
    return true;
  });
}

export function containTabFocus(
  root: HTMLElement | null,
  event: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'preventDefault' | 'defaultPrevented'>
): void {
  if (event.key !== 'Tab' || event.defaultPrevented || !root) return;
  const focusable = focusableElements(root);
  if (focusable.length === 0) {
    event.preventDefault();
    root.focus();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  const current = document.activeElement;
  if (!focusable.includes(current as HTMLElement)) {
    event.preventDefault();
    (event.shiftKey ? last : first).focus();
  } else if (event.shiftKey && current === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && current === last) {
    event.preventDefault();
    first.focus();
  }
}
