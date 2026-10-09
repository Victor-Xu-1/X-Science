import React from 'react';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { PreviewFullscreenLayer } from '@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewFullscreenLayer';
import { retainPreviewGrid } from '@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewTransitionSnapshot';

describe('Fullscreen ancestor layout lease', () => {
  let gridStyle: HTMLStyleElement;
  beforeEach(() => {
    gridStyle = document.createElement('style');
    gridStyle.textContent = '.transition-grid-fixture { display: grid; grid-auto-rows: 384px; }';
    document.head.append(gridStyle);
  });
  afterEach(() => gridStyle.remove());
  function ClampOnCommit({ active }: { active: boolean }) {
    const node = React.useRef<HTMLDivElement>(null);
    React.useLayoutEffect(() => {
      // Emulate layout clamping after fullscreen classes have changed, before
      // the ancestor snapshot callback. This preserves the real React order.
      node.current!.scrollTop = 0;
      node.current!.scrollLeft = 0;
    }, [active]);
    return (
      <div ref={node} data-testid='scrolling-content'>
        <button>Content action</button>
      </div>
    );
  }

  const fixture = (active: boolean) => (
    <div className='transition-grid-fixture' data-testid='grid-parent'>
      <PreviewFullscreenLayer active={active} label='File reading'>
        <ClampOnCommit active={active} />
      </PreviewFullscreenLayer>
    </div>
  );

  it('applies the entry snapshot after descendant layout effects on the very first transition', () => {
    const view = render(fixture(false));
    const content = screen.getByTestId('scrolling-content');
    content.scrollTop = 244;
    content.scrollLeft = 83;
    view.rerender(fixture(true));
    expect(content.scrollTop).toBe(244);
    expect(content.scrollLeft).toBe(83);
  });

  it('restores current fullscreen scrolling on exit, not the stale entry snapshot', () => {
    const view = render(fixture(false));
    const content = screen.getByTestId('scrolling-content');
    content.scrollTop = 244;
    view.rerender(fixture(true));
    content.scrollTop = 600;
    content.scrollLeft = 70;
    view.rerender(fixture(false));
    expect(content.scrollTop).toBe(600);
    expect(content.scrollLeft).toBe(70);
  });

  it('retains the original grid during this transition and releases it on exit or unmount', () => {
    const view = render(fixture(false));
    const grid = screen.getByTestId('grid-parent');
    expect(grid.style.gridAutoRows).toBe('');
    view.rerender(fixture(true));
    expect(grid.style.gridAutoRows).toBe('384px');
    gridStyle.textContent = '.transition-grid-fixture { display: grid; grid-auto-rows: 600px; }';
    expect(grid.style.gridAutoRows).toBe('384px');
    view.rerender(fixture(false));
    expect(grid.style.gridAutoRows).toBe('');
    view.rerender(fixture(true));
    expect(grid.style.gridAutoRows).toBe('600px');
    view.unmount();
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
  });
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
