import { useRef, useState, type PointerEvent } from 'react';

/** All custom IDs remain in the intent. System tags never enter the ordering payload. */
export function moveResourceTag(ids: readonly string[], id: string, target: string, edge: 'before' | 'after') {
  const from = ids.indexOf(id),
    to = ids.indexOf(target);
  if (from < 0 || to < 0 || from === to) return [...ids];
  const next = ids.filter((value) => value !== id);
  next.splice(next.indexOf(target) + (edge === 'after' ? 1 : 0), 0, id);
  return next;
}
type Drag = {
  pointerId: number;
  id: string;
  y: number;
  ids: readonly string[];
  active: boolean;
  target?: string;
  edge?: 'before' | 'after';
};
export function useResourceTagReorder(ids: readonly string[], busy: boolean, save: (ids: readonly string[]) => void) {
  const drag = useRef<Drag | null>(null);
  const [indicator, setIndicator] = useState<{ id: string; edge: 'before' | 'after' } | null>(null);
  const finish = () => {
    drag.current = null;
    setIndicator(null);
  };
  const down = (event: PointerEvent<HTMLButtonElement>, id: string) => {
    if (busy || event.button !== 0 || event.isPrimary === false) return;
    event.currentTarget.setPointerCapture?.(event.pointerId);
    drag.current = { pointerId: event.pointerId, id, y: event.clientY, ids: [...ids], active: false };
  };
  const move = (event: PointerEvent<HTMLButtonElement>) => {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    if (!current.active && Math.abs(event.clientY - current.y) < 6) return;
    current.active = true;
    event.preventDefault();
    const rows = event.currentTarget.closest('ol')?.querySelectorAll<HTMLElement>('[data-reorderable-tag-id]');
    let found: { id: string; edge: 'before' | 'after' } | null = null;
    for (const row of rows ?? []) {
      const id = row.dataset.reorderableTagId;
      if (!id || id === current.id) continue;
      const bounds = row.getBoundingClientRect();
      if (event.clientY >= bounds.top && event.clientY <= bounds.bottom) {
        found = { id, edge: event.clientY < (bounds.top + bounds.bottom) / 2 ? 'before' : 'after' };
        break;
      }
    }
    current.target = found?.id;
    current.edge = found?.edge;
    setIndicator(found);
  };
  const up = (event: PointerEvent<HTMLButtonElement>) => {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    if (current.active && current.target && current.edge && JSON.stringify(current.ids) === JSON.stringify(ids))
      save(moveResourceTag(current.ids, current.id, current.target, current.edge));
    finish();
  };
  return { indicator, down, move, up, finish };
}
