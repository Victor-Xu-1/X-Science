import { describe, expect, it } from 'vitest';
import { isNearMessageBoundary } from '@/renderer/pages/conversation/Messages/virtualMessageNavigation';

describe('actual message viewport boundary', () => {
  const viewport = (scrollTop: number, clientHeight = 600, scrollHeight = 1600) =>
    ({ scrollTop, clientHeight, scrollHeight }) as HTMLElement;

  it('does not infer a visible edge from the render overscan range', () => {
    expect(isNearMessageBoundary(viewport(400), 'end')).toBe(false);
    expect(isNearMessageBoundary(viewport(400), 'start')).toBe(false);
    expect(isNearMessageBoundary(viewport(900), 'end')).toBe(true);
    expect(isNearMessageBoundary(viewport(100), 'start')).toBe(true);
  });
  it('ignores detached and unmeasured viewports', () => {
    expect(isNearMessageBoundary(null, 'end')).toBe(false);
    expect(isNearMessageBoundary(viewport(0, 0), 'start')).toBe(false);
    expect(isNearMessageBoundary(viewport(0, 600, 0), 'end')).toBe(false);
  });
});
