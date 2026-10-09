import React, { useLayoutEffect, useRef } from 'react';
import { handleTabListKeyDown } from '@/renderer/utils/tabListKeyboard';

/** Derives header semantics and inactive-panel focusability from Arco's DOM.
 * Arco retains active-tab state, panel IDs, mounted drafts and change callbacks. */
export default function ArcoTabListHeader({
  children,
  label,
  orientation = 'horizontal',
}: React.PropsWithChildren<{
  label: string;
  orientation?: 'horizontal' | 'vertical';
}>) {
  const headerRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    for (const tab of headerRef.current?.querySelectorAll<HTMLElement>('[role="tab"]') ?? []) {
      if (tab.closest('[role="tablist"]') !== headerRef.current) continue;
      tab.tabIndex =
        tab.getAttribute('aria-selected') === 'true' && tab.getAttribute('aria-disabled') !== 'true' ? 0 : -1;
      const panelId = tab.getAttribute('aria-controls');
      const panel = panelId ? document.getElementById(panelId) : null;
      if (panel?.getAttribute('role') === 'tabpanel' && panel.getAttribute('aria-labelledby') === tab.id) {
        panel.toggleAttribute('inert', panel.getAttribute('aria-hidden') === 'true');
      }
    }
  }, [children]);
  return (
    <div
      ref={headerRef}
      role='tablist'
      aria-label={label}
      aria-orientation={orientation}
      style={{ display: 'contents' }}
      onKeyDown={handleTabListKeyDown}
    >
      {children}
    </div>
  );
}
