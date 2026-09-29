import { useLayoutEffect, useMemo, useState } from 'react';
import type { TurnPreviewItem } from './minimapTypes';
import { turnRailProgress } from './turnRailModel';

export function useTurnRailGeometry(items: TurnPreviewItem[], viewport: HTMLDivElement | null) {
  const owners = useMemo(
    () => new Map(items.flatMap((item, index) => item.messageIds.map((id) => [id, index] as const))),
    [items]
  );
  const [geometry, setGeometry] = useState<{
    left: number;
    top: number;
    rtl: boolean;
    progress: number;
    visible: number[];
  }>();
  useLayoutEffect(() => {
    if (!viewport || items.length === 0) return;
    let frame = 0;
    const panel = viewport.closest<HTMLElement>('.chat-surface-container') ?? viewport;
    const update = () => {
      frame = 0;
      const rect = viewport.getBoundingClientRect();
      const panelRect = panel.getBoundingClientRect();
      const rtl = getComputedStyle(viewport).direction === 'rtl';
      const visible = new Set<number>();
      const tops = new Map<number, number>();
      for (const element of viewport.querySelectorAll<HTMLElement>('[data-source-message-id]')) {
        const id = element.dataset.sourceMessageId ?? '';
        const index = owners.get(id);
        if (index === undefined) continue;
        const bounds = element.getBoundingClientRect();
        if (items[index].messageId === id) tops.set(index, bounds.top);
        if (bounds.bottom > rect.top && bounds.top < rect.bottom) visible.add(index);
      }
      const indices = [...visible].toSorted((a, b) => a - b);
      const next = {
        left: rtl ? rect.right - 16 : Math.max(0, rect.left - 8),
        top: panelRect.top + panelRect.height / 2,
        rtl,
        progress: turnRailProgress(tops, rect.top + 32, indices[0]),
        visible: indices,
      };
      setGeometry((previous) =>
        previous &&
        previous.left === next.left &&
        previous.top === next.top &&
        previous.rtl === next.rtl &&
        previous.progress === next.progress &&
        previous.visible.join(',') === indices.join(',')
          ? previous
          : next
      );
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    viewport.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule);
    const observer = new ResizeObserver(schedule);
    observer.observe(viewport);
    if (panel !== viewport) observer.observe(panel);
    if (viewport.firstElementChild) observer.observe(viewport.firstElementChild);
    if (viewport.parentElement) observer.observe(viewport.parentElement);
    return () => {
      viewport.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [items, owners, viewport]);
  return geometry;
}
