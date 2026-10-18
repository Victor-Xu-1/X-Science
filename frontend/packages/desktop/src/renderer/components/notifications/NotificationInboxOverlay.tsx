import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { containTabFocus, restoreScopedFocus } from '@/renderer/utils/focusScope';
import styles from './notificationInbox.module.css';

type Props = React.PropsWithChildren<{
  id: string;
  anchor: React.RefObject<HTMLButtonElement | null>;
  mobile: boolean;
  side: 'top' | 'bottom';
  label: string;
  dismissLabel: string;
  onClose: () => void;
}>;
const clamp = (value: number, min: number, max: number) => Math.max(min, Math.min(value, Math.max(min, max)));

/** One mounted inbox surface; execution and approval ownership remain elsewhere. */
export function NotificationInboxOverlay({ id, anchor, mobile, side, label, dismissLabel, onClose, children }: Props) {
  const panel = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<React.CSSProperties>({ left: 8, top: 8, width: 440 });
  useLayoutEffect(() => {
    const root = panel.current,
      opener = anchor.current;
    root?.focus({ preventScroll: true });
    return () => {
      queueMicrotask(() => {
        if (!root?.isConnected) restoreScopedFocus(opener, root);
      });
    };
  }, [anchor]);
  useLayoutEffect(() => {
    if (mobile) return;
    const place = () => {
      const trigger = anchor.current,
        root = panel.current;
      if (!trigger || !root) return;
      const rect = trigger.getBoundingClientRect(),
        height = root.getBoundingClientRect().height;
      const width = Math.min(440, Math.max(0, window.innerWidth - 16));
      const above = rect.top - height - 8,
        below = rect.bottom + 8;
      let top = side === 'top' ? above : below;
      if (top < 8 && below + height <= window.innerHeight - 8) top = below;
      else if (top + height > window.innerHeight - 8 && above >= 8) top = above;
      setPosition({
        width,
        left: clamp(side === 'top' ? rect.left : rect.right - width, 8, window.innerWidth - width - 8),
        top: clamp(top, 8, window.innerHeight - height - 8),
      });
    };
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(place);
    if (panel.current) observer?.observe(panel.current);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [anchor, mobile, side]);
  useEffect(() => {
    const pointer = (event: PointerEvent) => {
      if (!panel.current?.contains(event.target as Node) && !anchor.current?.contains(event.target as Node)) onClose();
    };
    const keyboard = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
      } else if (mobile) containTabFocus(panel.current, event);
    };
    document.addEventListener('pointerdown', pointer);
    document.addEventListener('keydown', keyboard);
    return () => {
      document.removeEventListener('pointerdown', pointer);
      document.removeEventListener('keydown', keyboard);
    };
  }, [anchor, mobile, onClose]);
  useEffect(() => {
    if (!mobile) return;
    const root = document.getElementById('root'),
      overflow = document.body.style.overflow;
    const hidden = root?.getAttribute('aria-hidden') ?? null,
      inert = root?.inert ?? false;
    document.body.style.overflow = 'hidden';
    if (root) {
      root.inert = true;
      root.setAttribute('aria-hidden', 'true');
    }
    return () => {
      document.body.style.overflow = overflow;
      if (root) {
        root.inert = inert;
        if (hidden === null) root.removeAttribute('aria-hidden');
        else root.setAttribute('aria-hidden', hidden);
      }
    };
  }, [mobile]);
  return createPortal(
    <>
      {mobile && (
        <button type='button' tabIndex={-1} className={styles.dismiss} aria-label={dismissLabel} onClick={onClose} />
      )}
      <div
        ref={panel}
        id={id}
        role='dialog'
        tabIndex={-1}
        aria-label={label}
        aria-modal={mobile || undefined}
        className={`${styles.panel} ${mobile ? styles.sheet : ''}`}
        style={mobile ? undefined : position}
      >
        {children}
      </div>
    </>,
    document.body
  );
}
