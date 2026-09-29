export const TURN_RAIL_ROW_HEIGHT = 8;
export const TURN_RAIL_MAX_HEIGHT = 480;

export function turnRailScale(pointer: number | null, index: number): number {
  return pointer === null ? 0.4 : 0.4 + 0.6 * Math.cos((Math.min(4, Math.abs(pointer - index)) * Math.PI) / 8) ** 2;
}

export function turnRailWindow(count: number, scrollTop: number, height: number, focused = -1): number[] {
  if (count === 0) return [];
  const indices = new Set([0, count - 1]);
  const first = Math.max(0, Math.floor(scrollTop / TURN_RAIL_ROW_HEIGHT) - 4);
  const last = Math.min(count, Math.ceil((scrollTop + height) / TURN_RAIL_ROW_HEIGHT) + 4);
  for (let index = first; index < last; index++) indices.add(index);
  if (focused >= 0 && focused < count) indices.add(focused);
  return [...indices].toSorted((a, b) => a - b);
}

export function turnRailProgress(promptTops: Map<number, number>, boundary: number, firstVisible = 0): number {
  let current = 0;
  for (const [index, top] of promptTops) if (top <= boundary) current = Math.max(current, index);
  const top = promptTops.get(current);
  const next = promptTops.get(current + 1);
  const fraction =
    top !== undefined && next !== undefined && next > top
      ? Math.max(0, Math.min(1, (boundary - top) / (next - top)))
      : 0;
  return Math.max(current + fraction, firstVisible);
}
