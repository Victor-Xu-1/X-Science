import { describe, expect, it } from 'vitest';
import { retainPreviewGrid } from '@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewTransitionSnapshot';

describe('Fullscreen ancestor layout lease', () => {
  it('retains measured rows until the last scope exits and restores the original style only once', () => {
    const grid = document.createElement('div');
    const first = retainPreviewGrid({ node: grid, autoRows: '680px' });
    const second = retainPreviewGrid({ node: grid, autoRows: '568px' });
    expect(grid.style.gridAutoRows).toBe('680px');
    first();
    first();
    expect(grid.style.gridAutoRows).toBe('680px');
    second();
    expect(grid.style.gridAutoRows).toBe('');
  });
  it('does not overwrite an independent subsequent layout change', () => {
    const grid = document.createElement('div');
    const release = retainPreviewGrid({ node: grid, autoRows: '680px' });
    grid.style.gridAutoRows = '500px';
    release();
    expect(grid.style.gridAutoRows).toBe('500px');
  });
});
