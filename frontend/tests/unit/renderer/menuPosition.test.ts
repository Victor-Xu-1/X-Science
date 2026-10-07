import { describe, expect, it } from 'vitest';
import { clampMenuPosition } from '@/renderer/utils/menuPosition';

describe('Measured context menu placement', () => {
  it('fits the real expanded menu at the bottom-right without guessing its action count', () => {
    expect(clampMenuPosition(1330, 1220, 200, 336, 1332, 1228)).toEqual({ left: 1124, top: 884 });
  });
  it('keeps the original point where enough room exists', () => {
    expect(clampMenuPosition(100, 140, 220, 120, 1000, 800)).toEqual({ left: 100, top: 140 });
  });
  it('handles negative points and a menu taller than the view without negative positions', () => {
    expect(clampMenuPosition(-20, -10, 240, 900, 390, 700)).toEqual({ left: 8, top: 8 });
  });
});
