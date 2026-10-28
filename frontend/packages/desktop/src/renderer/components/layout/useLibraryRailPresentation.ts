import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';

type Claim = { owner: object; account: string };

/** Layout's only Library rail owner; no navigation or catalogue requests. */
export function useLibraryRailPresentation(active: boolean, mobile: boolean) {
  const activeRef = useRef(active),
    mobileRef = useRef(mobile);
  activeRef.current = active;
  mobileRef.current = mobile;
  const [host, updateHost] = useState<HTMLElement | null>(null);
  const hostRef = useRef<HTMLElement | null>(null);
  const setHost = useCallback((element: HTMLElement | null) => {
    hostRef.current = element;
    updateHost(element);
  }, []);
  const [claim, setClaim] = useState<Claim>();
  const current = useRef<Claim | undefined>(undefined);
  const [collapsed, updateCollapsed] = useState(mobile);
  const collapsedRef = useRef(mobile);
  const key = useId();
  const clear = useCallback(() => {
    current.current = undefined;
    setClaim(undefined);
  }, []);
  useLayoutEffect(() => {
    if (!active) clear();
  }, [active, clear]);
  useEffect(() => registerRendererAccountReset(`library-rail:${key}`, clear), [key, clear]);
  const register = useCallback((owner: object, account: string) => {
    if (!activeRef.current || account !== getRendererAccountScopeToken()) return () => {};
    const next = { owner, account };
    current.current = next;
    setClaim(next);
    collapsedRef.current = mobileRef.current;
    updateCollapsed(mobileRef.current); // Reference desktop default on each Page mount; Web drawer starts closed.
    return () => {
      if (current.current !== next) return;
      current.current = undefined;
      setClaim((value) => (value === next ? undefined : value));
    };
  }, []);
  const setCollapsed = useCallback((value: boolean) => {
    if (!activeRef.current || current.current?.account !== getRendererAccountScopeToken()) return;
    if (value && !mobileRef.current && hostRef.current?.contains(document.activeElement))
      hostRef.current.querySelector<HTMLElement>('[data-library-rail-toggle]')?.focus();
    collapsedRef.current = value;
    updateCollapsed(value);
  }, []);
  const owned = active && claim === current.current && claim?.account === getRendererAccountScopeToken();
  useEffect(() => {
    if (!owned) return;
    const toggle = (event: KeyboardEvent) => {
      const mac = /Mac|iPhone|iPad/.test(navigator.platform);
      if (
        !activeRef.current ||
        current.current?.account !== getRendererAccountScopeToken() ||
        event.defaultPrevented ||
        event.isComposing ||
        event.repeat ||
        event.altKey ||
        event.shiftKey ||
        event.key.toLowerCase() !== 'b' ||
        !(mac ? event.metaKey : event.ctrlKey) ||
        document.querySelector(
          '[role="dialog"]:not([data-state="closed"]),[role="alertdialog"]:not([data-state="closed"])'
        )
      )
        return;
      event.preventDefault();
      setCollapsed(!collapsedRef.current);
    };
    window.addEventListener('keydown', toggle);
    return () => window.removeEventListener('keydown', toggle);
  }, [owned, setCollapsed]);
  return {
    host,
    setHost,
    owner: owned ? claim?.owner : undefined,
    register,
    collapsed: owned ? collapsed : mobile,
    setCollapsed,
  };
}
