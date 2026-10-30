import { useEffect, useRef, useState } from 'react';
import type React from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

/** Library-local dismissal only; Arco retains Arrow/Enter dispatch and popup ownership. */
export function useLibraryActionMenu(disabled: boolean) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const account = useRef(getRendererAccountScopeToken()).current;
  const current = () => account === getRendererAccountScopeToken();
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  const escape = (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key !== 'Escape' || !open) return;
    event.preventDefault();
    event.stopPropagation();
    setOpen(false);
    const target = trigger.current;
    if (target?.isConnected && !disabled && !target.disabled && current()) target.focus({ preventScroll: true });
  };
  const select = (
    event: { type: string; key?: string; keyCode?: number; preventDefault: () => void },
    action: () => void
  ) => {
    // Arco returns focus before Enter's native default. Consume only that default.
    if (event.type === 'keydown' && (event.key === 'Enter' || event.keyCode === 13)) event.preventDefault();
    setOpen(false);
    if (!disabled && current()) action();
  };
  return {
    trigger,
    visible: open && !disabled && current(),
    escape,
    select,
    visibility: (value: boolean) => {
      if (current()) setOpen(value && !disabled);
    },
  };
}
