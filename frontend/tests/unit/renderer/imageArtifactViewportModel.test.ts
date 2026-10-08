import { describe, expect, it } from 'vitest';
import {
  imageViewportGeometry,
  imageViewCenter,
  imageViewScroll,
} from '@/renderer/pages/artifact/imageArtifactViewportModel';

describe('image viewport geometry', () => {
  it('fits both wide and tall scientific images without changing aspect ratio', () => {
    for (const natural of [
      { width: 2000, height: 400 },
      { width: 400, height: 4000 },
    ]) {
      const g = imageViewportGeometry(natural, { width: 800, height: 400 }, { kind: 'fit' });
      expect(g.width).toBeLessThanOrEqual(800);
      expect(g.height).toBeLessThanOrEqual(400);
      expect(g.width / g.height).toBeCloseTo(natural.width / natural.height);
      expect(g.offsetX).toBeGreaterThanOrEqual(0);
      expect(g.offsetY).toBeGreaterThanOrEqual(0);
    }
  });

  it('does not prevent fit for unusually large or small input dimensions', () => {
    for (const natural of [
      { width: 1, height: 1 },
      { width: 100000, height: 5000 },
    ]) {
      const g = imageViewportGeometry(natural, { width: 800, height: 400 }, { kind: 'fit' });
      expect(g.scale).toBeGreaterThanOrEqual(g.minScale);
      expect(g.scale).toBeLessThanOrEqual(g.maxScale);
      expect(g.width).toBeLessThanOrEqual(800);
      expect(g.height).toBeLessThanOrEqual(400);
    }
  });

  it('retains the same normalized reading center across actual-size and zoom changes', () => {
    const size = { width: 2000, height: 1000 };
    const view = { width: 800, height: 400 };
    const old = imageViewportGeometry(size, view, { kind: 'scale', scale: 1 });
    const center = imageViewCenter(old, 800, 500);
    const next = imageViewportGeometry(size, view, { kind: 'scale', scale: 2 });
    const scroll = imageViewScroll(next, center);
    expect(imageViewCenter(next, scroll.left, scroll.top)).toEqual(center);
  });

  it('keeps all image edges reachable with positive scroll geometry', () => {
    const g = imageViewportGeometry(
      { width: 2000, height: 1000 },
      { width: 800, height: 400 },
      { kind: 'scale', scale: 1 }
    );
    expect(imageViewScroll(g, { x: 0, y: 0 })).toEqual({ left: 0, top: 0 });
    expect(imageViewScroll(g, { x: 1, y: 1 })).toEqual({ left: 1200, top: 600 });
  });

  it('rejects invalid decoded dimensions and bounds manual zoom', () => {
    for (const natural of [
      { width: 0, height: 10 },
      { width: NaN, height: 10 },
      { width: 10, height: Infinity },
    ]) {
      expect(() => imageViewportGeometry(natural, { width: 800, height: 400 }, { kind: 'fit' })).toThrow();
    }
    const g = imageViewportGeometry(
      { width: 2000, height: 400 },
      { width: 800, height: 400 },
      { kind: 'scale', scale: 1e8 }
    );
    expect(g.scale).toBe(g.maxScale);
  });
});
