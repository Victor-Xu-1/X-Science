import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, type RefObject } from 'react';
import type { SelectHandle } from '@arco-design/web-react/es/Select/interface';

/** Observe the primitive's close; never own its visibility or active option. */
export function useSelectCloseFocus(
  select: RefObject<SelectHandle | null>,
  eligible: boolean,
  disabled: boolean,
  controlledVisible: boolean | undefined
) {
  const intent = useRef<HTMLDivElement | undefined>(undefined);
  const notifiedClose = useRef<HTMLDivElement | undefined>(undefined);
  const previous = useRef<{ visible?: boolean; control?: HTMLDivElement }>({ visible: controlledVisible });
  const [closed, commitClose] = useReducer((value: number) => value + 1, 0);

  const capture = useCallback(() => {
    intent.current = undefined;
    if (!eligible || disabled) return;
    const control = select.current?.dom;
    if (!control?.isConnected) return;
    const document = control.ownerDocument;
    const id = control.getAttribute('aria-controls');
    const menu = id ? document.getElementById(id) : undefined;
    if (control.contains(document.activeElement) || menu?.contains(document.activeElement)) {
      intent.current = control;
      commitClose();
    }
  }, [disabled, eligible, select]);

  useLayoutEffect(() => {
    const before = previous.current;
    const control = select.current?.dom;
    previous.current = { visible: controlledVisible, control };
    if (controlledVisible === true || disabled || !eligible) {
      intent.current = undefined;
      notifiedClose.current = undefined;
    }
    // An onChange owner may commit its controlled close before Arco's deferred
    // visibility notification. Observe that commit before SelectView's blur.
    // A notified Tab/prevented close must not be captured a second time.
    if (before.visible === true && controlledVisible === false) {
      if (!notifiedClose.current && before.control === control) capture();
      notifiedClose.current = undefined;
    }
  });

  useEffect(() => {
    const control = intent.current;
    intent.current = undefined;
    notifiedClose.current = undefined;
    // SelectView blurs in its child passive effect. Restore only after that
    // real close, to the same connected owner, without overriding an outside
    // focus handoff or a controlled owner that kept the popup open.
    if (!eligible || disabled || !control?.isConnected || select.current?.dom !== control) return;
    const document = control.ownerDocument;
    if (
      control.getAttribute('aria-expanded') === 'false' &&
      (document.activeElement === document.body || control.contains(document.activeElement))
    ) {
      select.current.focus();
    }
  }, [closed, disabled, eligible, select]);

  useEffect(
    () => () => {
      intent.current = undefined;
      notifiedClose.current = undefined;
    },
    []
  );

  return {
    visibleChange(next: boolean) {
      intent.current = undefined;
      const control = select.current?.dom;
      notifiedClose.current = next ? undefined : control;
      if (!next && control?.getAttribute('aria-expanded') === 'true') capture();
    },
    keyDown(event: { key: string; keyCode: number; defaultPrevented: boolean }) {
      if (event.key === 'Tab' || event.keyCode === 9 || event.defaultPrevented) intent.current = undefined;
    },
  };
}
