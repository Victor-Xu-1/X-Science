import { useEffect, useRef, useState } from 'react';
import type { PointerEvent } from 'react';

const clamp = (value: number) => Math.max(15, Math.min(55, value));

export function useNotebookSplit() {
  const [percent, setPercent] = useState(20);
  const cleanup = useRef<(() => void) | null>(null);
  useEffect(() => () => cleanup.current?.(), []);
  const onPointerDown = (event: PointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    cleanup.current?.();
    const parent = event.currentTarget.parentElement;
    if (!parent) return;
    const height = parent.getBoundingClientRect().height;
    if (!height) return;
    const top = event.clientY,
      start = percent;
    const move = (next: globalThis.PointerEvent) => setPercent(clamp(start + ((top - next.clientY) * 100) / height));
    const end = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', end);
      cleanup.current = null;
    };
    cleanup.current = end;
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', end);
  };
  return { percent, onPointerDown, adjust: (delta: number) => setPercent((current) => clamp(current + delta)) };
}
