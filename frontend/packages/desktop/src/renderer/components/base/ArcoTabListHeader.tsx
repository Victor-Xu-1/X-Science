import React, { useLayoutEffect, useRef } from 'react';
import { handleTabListKeyDown } from '@/renderer/utils/tabListKeyboard';

/** Supplies semantics/keyboard behaviour to Arco's header only. Arco retains
 * active-tab state, panel IDs, disabled tabs and change callbacks. */
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
