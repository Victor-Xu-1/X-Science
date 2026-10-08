import type React from 'react';

/** Automatic-activation tabs: one Tab stop, arrows/Home/End move and select.
 * Ignore nested controls/tablists and modified browser shortcuts. */
export function handleTabListKeyDown(event: React.KeyboardEvent<HTMLElement>): void {
  if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
  const target = event.target instanceof Element ? event.target.closest<HTMLElement>('[role="tab"]') : null;
  if (!target || target.closest('[role="tablist"]') !== event.currentTarget) return;
  const tabs = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]')).filter(
    (tab) =>
      tab.closest('[role="tablist"]') === event.currentTarget && !tab.matches(':disabled, [aria-disabled="true"]')
  );
  const current = tabs.indexOf(target);
  if (current < 0 || tabs.length === 0) return;
  const vertical = event.currentTarget.getAttribute('aria-orientation') === 'vertical';
  const rtl = getComputedStyle(event.currentTarget).direction === 'rtl';
  const nextKey = vertical ? 'ArrowDown' : rtl ? 'ArrowLeft' : 'ArrowRight';
  const previousKey = vertical ? 'ArrowUp' : rtl ? 'ArrowRight' : 'ArrowLeft';
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? tabs.length - 1
        : event.key === nextKey
          ? (current + 1) % tabs.length
          : event.key === previousKey
            ? (current + tabs.length - 1) % tabs.length
            : null;
  if (next === null) return;
  event.preventDefault();
  tabs[next].focus();
  tabs[next].click();
}
