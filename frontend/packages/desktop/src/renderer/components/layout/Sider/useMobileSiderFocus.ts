import { useLayoutEffect, useRef, type RefObject } from 'react';
import { focusableElements, isVisibleFocusable, restoreScopedFocus } from '@/renderer/utils/focusScope';

type Options = {
  mobile: boolean;
  open: boolean;
  routeKey: string;
  root: RefObject<HTMLElement | null>;
  shell: RefObject<HTMLElement | null>;
  backdrop: RefObject<HTMLElement | null>;
  toggleId: string;
  onClose: () => void;
};

function available(element: HTMLElement): boolean {
  for (let node: HTMLElement | null = element; node; node = node.parentElement) {
    if (node.inert || node.hidden || node.hasAttribute('inert') || node.getAttribute('aria-hidden') === 'true')
      return false;
    const style = getComputedStyle(node);
    if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') return false;
  }
  return element.isConnected;
}

/** Follow actual open controls, including body portals, without admitting unrelated global menus. */
function controlledScopes(root: HTMLElement, toggle: HTMLElement | null): HTMLElement[] {
  const scopes = [root, ...(toggle && !root.contains(toggle) && available(toggle) ? [toggle] : [])];
  const seen = new Set<HTMLElement>(scopes);
  for (let index = 0; index < scopes.length; index++) {
    const current = scopes[index];
    const controls = [...current.querySelectorAll<HTMLElement>('[aria-controls], [aria-owns]')];
    if (current !== root) controls.unshift(current);
    for (const control of controls) {
      if (control.getAttribute('aria-expanded') === 'false') continue;
      const ids = `${control.getAttribute('aria-expanded') === 'true' ? (control.getAttribute('aria-controls') ?? '') : ''} ${control.getAttribute('aria-owns') ?? ''}`;
      for (const id of ids.trim().split(/\s+/)) {
        const popup = document.getElementById(id);
        if (
          popup instanceof HTMLElement &&
          !seen.has(popup) &&
          !root.contains(popup) &&
          !popup.contains(root) &&
          available(popup)
        ) {
          seen.add(popup);
          scopes.push(popup);
        }
      }
    }
  }
  return scopes;
}

function isolateBackground(
  root: HTMLElement,
  shell: HTMLElement,
  backdrop: HTMLElement | null,
  foreground: HTMLElement[],
  syncOwnership: () => void
): () => void {
  const saved = new Map<HTMLElement, { inert: string | null; hidden: string | null }>();
  const changed = new Map<HTMLElement, Set<string>>();
  const branches = new Set<HTMLElement>();
  const parents = new Set<HTMLElement>();
  for (const element of foreground) {
    if (!shell.contains(element)) continue;
    let branch = element;
    branches.add(branch);
    while (branch !== shell && branch.parentElement && shell.contains(branch.parentElement)) {
      branch = branch.parentElement;
      branches.add(branch);
      parents.add(branch);
    }
  }
  let observer: MutationObserver | undefined;
  const claim = (element: HTMLElement) => {
    if (saved.has(element) || branches.has(element) || element === backdrop) return;
    const modals = [
      element,
      ...element.querySelectorAll<HTMLElement>('[role="dialog"][aria-modal="true"], [role="alertdialog"]'),
    ];
    if (
      modals.some((node) => node.matches('[role="dialog"][aria-modal="true"], [role="alertdialog"]') && available(node))
    )
      return;
    saved.set(element, { inert: element.getAttribute('inert'), hidden: element.getAttribute('aria-hidden') });
    element.setAttribute('inert', '');
    element.setAttribute('aria-hidden', 'true');
    observer?.observe(element, { attributes: true, attributeFilter: ['inert', 'aria-hidden'] });
  };
  for (const parent of parents) for (const child of parent.children) if (child instanceof HTMLElement) claim(child);
  const trackChanges = (records: MutationRecord[]) => {
    let relationshipsChanged = false;
    for (const record of records) {
      if (record.type === 'attributes' && record.target instanceof HTMLElement && record.attributeName) {
        if (saved.has(record.target)) {
          const names = changed.get(record.target) ?? new Set<string>();
          names.add(record.attributeName);
          changed.set(record.target, names);
        }
        if (root.contains(record.target) && !(record.target === root && record.attributeName === 'aria-owns'))
          relationshipsChanged = true;
      } else if (record.type === 'childList') {
        if (parents.has(record.target as HTMLElement)) {
          for (const child of record.addedNodes) if (child instanceof HTMLElement) claim(child);
        }
        if (root.contains(record.target)) relationshipsChanged = true;
      }
    }
    if (relationshipsChanged) syncOwnership();
  };
  // Observe only after our writes. A later writer, even one setting the same
  // value, takes ownership; never roll its state back to our entry snapshot.
  observer = new MutationObserver(trackChanges);
  for (const element of saved.keys()) {
    observer.observe(element, { attributes: true, attributeFilter: ['inert', 'aria-hidden'] });
  }
  for (const parent of parents) observer.observe(parent, { childList: true });
  observer.observe(root, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ['aria-controls', 'aria-expanded', 'aria-owns'],
  });
  const activeObserver = observer;
  return () => {
    trackChanges(activeObserver.takeRecords());
    activeObserver.disconnect();
    for (const [element, prior] of saved) {
      for (const [name, value] of [
        ['inert', prior.inert],
        ['aria-hidden', prior.hidden],
      ] as const) {
        if (changed.get(element)?.has(name) || element.getAttribute(name) !== (name === 'inert' ? '' : 'true'))
          continue;
        if (value === null) element.removeAttribute(name);
        else element.setAttribute(name, value);
      }
    }
  };
}

/** One transient keyboard boundary; the existing layout remains the open/close authority. */
export function useMobileSiderFocus({
  mobile,
  open,
  routeKey,
  root,
  shell,
  backdrop,
  toggleId,
  onClose,
}: Options): void {
  const latest = useRef({ mobile, open, onClose });
  const previousRoute = useRef(routeKey);
  const epoch = useRef(0);
  useLayoutEffect(() => {
    latest.current = { mobile, open, onClose };
  }, [mobile, open, onClose]);

  useLayoutEffect(() => {
    const routeChanged = previousRoute.current !== routeKey;
    previousRoute.current = routeKey;
    const scope = root.current;
    const app = shell.current;
    if (!mobile || !open || !scope || !app) return;
    // The previous effect cleans up before the new route's layout focus runs.
    if (routeChanged) {
      latest.current.onClose();
      return;
    }

    const revision = ++epoch.current;
    const current = document.activeElement;
    const opener = current instanceof HTMLElement && !scope.contains(current) ? current : null;
    let lastInside: HTMLElement | null = scope.contains(current) ? (current as HTMLElement) : null;
    const roots = () => controlledScopes(scope, document.getElementById(toggleId));
    const previousOwns = scope.getAttribute('aria-owns');
    let writtenOwns: string | null = previousOwns;
    const syncOwnership = () => {
      const ids = roots()
        .filter((node) => node !== scope && node.id)
        .map((node) => node.id);
      const value = ids.join(' ') || null;
      if (scope.getAttribute('aria-owns') !== value) {
        if (value === null) scope.removeAttribute('aria-owns');
        else scope.setAttribute('aria-owns', value);
      }
      writtenOwns = value;
    };
    syncOwnership();
    const restoreBackground = isolateBackground(scope, app, backdrop.current, roots(), syncOwnership);
    const includes = (element: Node | null) => roots().some((node) => node.contains(element));
    const otherModal = (target: HTMLElement | null) => {
      const dialog = target?.closest('[role="dialog"][aria-modal="true"], [role="alertdialog"]');
      return dialog instanceof HTMLElement && dialog !== scope && available(dialog);
    };
    const yields = (target: HTMLElement | null) =>
      epoch.current !== revision || !available(scope) || otherModal(target);
    const tabStops = () =>
      [
        ...new Set(
          roots().flatMap((node) => [...(isVisibleFocusable(node) ? [node] : []), ...focusableElements(node)])
        ),
      ].sort((a, b) => {
        const left = a.tabIndex > 0 ? a.tabIndex : Infinity;
        const right = b.tabIndex > 0 ? b.tabIndex : Infinity;
        return left === right ? 0 : left - right;
      });
    const focusInside = () => {
      const target =
        lastInside && includes(lastInside) && isVisibleFocusable(lastInside) ? lastInside : (tabStops()[0] ?? scope);
      target.focus({ preventScroll: true });
    };
    const focusIn = (event: FocusEvent) => {
      const target = event.target instanceof HTMLElement ? event.target : null;
      syncOwnership();
      if (yields(target)) return;
      if (target && includes(target)) lastInside = target;
      else focusInside();
    };
    const keyboard = (event: KeyboardEvent) => {
      const target = event.target instanceof HTMLElement ? event.target : null;
      syncOwnership();
      if (event.defaultPrevented || event.isComposing || yields(target)) return;
      if (event.key === 'Escape') {
        const menuOpen =
          roots().some((node) => node !== scope && node.id !== toggleId) ||
          [...scope.querySelectorAll<HTMLElement>('[role="menu"], [role="listbox"]')].some(available);
        if (menuOpen) return;
        event.preventDefault();
        latest.current.onClose();
      } else if (event.key === 'Tab') {
        const stops = tabStops();
        const index = stops.indexOf(document.activeElement as HTMLElement);
        const next =
          index < 0
            ? event.shiftKey
              ? stops.length - 1
              : 0
            : (index + (event.shiftKey ? -1 : 1) + stops.length) % stops.length;
        event.preventDefault();
        (stops[next] ?? scope).focus({ preventScroll: true });
      }
    };
    document.addEventListener('focusin', focusIn);
    document.addEventListener('keydown', keyboard);
    if (
      !otherModal(document.activeElement instanceof HTMLElement ? document.activeElement : null) &&
      !scope.contains(document.activeElement)
    )
      focusInside();

    return () => {
      const cleanupRevision = ++epoch.current;
      document.removeEventListener('focusin', focusIn);
      document.removeEventListener('keydown', keyboard);
      restoreBackground();
      if (scope.getAttribute('aria-owns') === writtenOwns) {
        if (previousOwns === null) scope.removeAttribute('aria-owns');
        else scope.setAttribute('aria-owns', previousOwns);
      }
      queueMicrotask(() => {
        if (epoch.current !== cleanupRevision || (scope.isConnected && (!latest.current.mobile || latest.current.open)))
          return;
        restoreScopedFocus(opener, scope);
      });
    };
  }, [mobile, open, routeKey, root, shell, backdrop, toggleId]);
}
