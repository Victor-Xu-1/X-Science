import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useArtifactCanvasViewport } from '@/renderer/pages/artifact/useArtifactCanvasViewport';
import {
  imageViewportGeometry,
  imageViewCenter,
  type ImageDimensions,
} from '@/renderer/pages/artifact/imageArtifactViewportModel';

type Viewport = ReturnType<typeof useArtifactCanvasViewport>;
const NATURAL = { width: 2000, height: 1000 };
let latest: Viewport;
let width = 832,
  height = 432;
const observers: { resize: () => void; disconnect: ReturnType<typeof vi.fn> }[] = [];

function Harness({
  owner = 'a',
  natural = NATURAL,
  relative = false,
  gestures = true,
  clicked = () => {},
  observed = () => {},
}: {
  owner?: string;
  natural?: ImageDimensions | null;
  relative?: boolean;
  gestures?: boolean;
  clicked?: () => void;
  observed?: (value: Viewport) => void;
}) {
  const viewport = useArtifactCanvasViewport(owner, natural, { fitRelativeZoom: relative });
  latest = viewport;
  observed(viewport);
  return (
    <>
      <button onClick={() => viewport.zoom(1)}>Zoom in</button>
      <button onClick={() => viewport.zoom(-1)}>Zoom out</button>
      <button onClick={() => viewport.changeMode({ kind: 'scale', scale: 1 })}>Actual</button>
      <button onClick={() => viewport.changeMode({ kind: 'fit' })}>Fit</button>
      <div
        data-testid='viewport'
        ref={gestures ? viewport.gestureRef : viewport.viewportRef}
        data-scale={viewport.geometry?.scale ?? 'none'}
        data-mode={viewport.mode.kind}
        data-panning={viewport.isPanning}
        tabIndex={0}
        onKeyDown={viewport.keyDown}
        {...(gestures ? viewport.panHandlers : {})}
        onDoubleClick={gestures ? viewport.doubleClick : undefined}
        onClickCapture={gestures ? viewport.clickCapture : undefined}
      >
        <canvas data-testid='canvas' onClick={clicked} />
        <button data-testid='nested-button'>Nested</button>
        <input aria-label='Nested input' />
        <span data-testid='selectable'>Selectable text</span>
      </div>
    </>
  );
}

function capture(element: HTMLElement) {
  const held = new Set<number>();
  const set = vi.fn((id: number) => held.add(id));
  const release = vi.fn((id: number) => held.delete(id));
  Object.defineProperties(element, {
    setPointerCapture: { configurable: true, value: set },
    releasePointerCapture: { configurable: true, value: release },
    hasPointerCapture: { configurable: true, value: (id: number) => held.has(id) },
  });
  return { set, release };
}

function wheel(element: HTMLElement, values: WheelEventInit) {
  const event = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -100, ...values });
  fireEvent(element, event);
  return event;
}

beforeEach(() => {
  width = 832;
  height = 432;
  observers.length = 0;
  vi.spyOn(Element.prototype, 'clientWidth', 'get').mockImplementation(() => width);
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockImplementation(() => height);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: () => void) {
        observers.push({ resize: callback, disconnect: this.disconnect });
      }
      observe() {}
      disconnect = vi.fn();
    }
  );
  vi.stubGlobal(
    'PointerEvent',
    class extends MouseEvent {
      pointerId: number;
      isPrimary: boolean;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, { ...init, buttons: init.buttons ?? (type === 'pointermove' || type === 'pointerdown' ? 1 : 0) });
        this.pointerId = init.pointerId ?? 1;
        this.isPrimary = init.isPrimary ?? true;
      }
    }
  );
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('shared artifact canvas viewport', () => {
  it('preserves default image geometry while optionally bounding PDF zoom relative to fit', () => {
    const view = { width: 800, height: 400 };
    const legacy = imageViewportGeometry(NATURAL, view, { kind: 'fit' });
    expect(legacy).toEqual(imageViewportGeometry(NATURAL, view, { kind: 'fit' }, {}));
    expect(legacy.minScale).toBe(0.1);
    expect(legacy.maxScale).toBe(8);
    const relative = imageViewportGeometry(NATURAL, view, { kind: 'scale', scale: 999 }, { fitRelativeZoom: true });
    expect(relative.minScale).toBe(0.4);
    expect(relative.maxScale).toBe(3.2);
    expect(relative.scale).toBe(3.2);
    expect(imageViewportGeometry(NATURAL, view, { kind: 'scale', scale: 0 }, { fitRelativeZoom: true }).scale).toBe(
      0.4
    );
    render(<Harness gestures={false} />);
    const element = screen.getByTestId('viewport');
    expect(element).toHaveAttribute('data-scale', '0.4');
    fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
    fireEvent.keyDown(element, { key: '+' });
    expect(element).toHaveAttribute('data-scale', '1.25');
    fireEvent.keyDown(element, { key: 'f' });
    expect(element).toHaveAttribute('data-scale', '0.4');
  });

  it('does not opt existing images into new pointer, wheel or double-click behavior', () => {
    render(<Harness gestures={false} />);
    const element = screen.getByTestId('viewport');
    expect(wheel(element, { ctrlKey: true }).defaultPrevented).toBe(false);
    fireEvent.doubleClick(element);
    fireEvent.pointerDown(element, { pointerId: 1, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(element, { pointerId: 1, clientX: 50, clientY: 50 });
    expect(element).toHaveAttribute('data-scale', '0.4');
    expect(element).toHaveAttribute('data-panning', 'false');
  });

  it('uses cancellable Ctrl/Meta wheel and immediate fit reset without hijacking normal scroll', () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({ matches: true }))
    );
    render(<Harness relative />);
    const element = screen.getByTestId('viewport');
    expect(wheel(element, {}).defaultPrevented).toBe(false);
    expect(wheel(element, { ctrlKey: true }).defaultPrevented).toBe(true);
    expect(element).toHaveAttribute('data-scale', '0.5');
    expect(wheel(element, { metaKey: true }).defaultPrevented).toBe(true);
    expect(element).toHaveAttribute('data-scale', '0.625');
    fireEvent.doubleClick(screen.getByTestId('canvas'));
    expect(element).toHaveAttribute('data-scale', '0.4');
    expect(element).toHaveAttribute('data-mode', 'fit');
  });

  it('captures only after a real drag and suppresses only its generated mouse click', () => {
    const clicked = vi.fn();
    render(<Harness clicked={clicked} />);
    const element = screen.getByTestId('viewport'),
      canvas = screen.getByTestId('canvas');
    const pointer = capture(element);
    fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
    const left = element.scrollLeft,
      top = element.scrollTop;
    fireEvent.pointerDown(canvas, { pointerId: 7, button: 0, clientX: 100, clientY: 100 });
    expect(pointer.set).not.toHaveBeenCalled();
    fireEvent.pointerMove(element, { pointerId: 7, clientX: 97, clientY: 100 });
    expect(pointer.set).not.toHaveBeenCalled();
    fireEvent.pointerMove(element, { pointerId: 7, clientX: 80, clientY: 70 });
    expect(pointer.set).toHaveBeenCalledWith(7);
    expect(element.scrollLeft).toBe(left + 20);
    expect(element.scrollTop).toBe(top + 30);
    expect(element).toHaveAttribute('data-panning', 'true');
    fireEvent.pointerUp(element, { pointerId: 7 });
    expect(pointer.release).toHaveBeenCalledWith(7);
    expect(element).toHaveAttribute('data-panning', 'false');
    fireEvent.click(canvas, { detail: 1 });
    expect(clicked).not.toHaveBeenCalled();
    fireEvent.pointerDown(canvas, { pointerId: 8, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerUp(canvas, { pointerId: 8 });
    fireEvent.click(canvas, { detail: 1 });
    expect(clicked).toHaveBeenCalledOnce();
  });

  it('pans a canvas with accessibility fallback text', () => {
    render(<Harness />);
    const element = screen.getByTestId('viewport');
    const pointer = capture(element);
    const canvas = screen.getByTestId('canvas');
    canvas.textContent = 'Accessible PDF structure fallback';
    fireEvent.pointerDown(canvas, { pointerId: 7, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(element, { pointerId: 7, clientX: 70, clientY: 70 });
    expect(pointer.set).toHaveBeenCalledWith(7);
  });

  it('ends pointer cancellation and lost capture without blocking the next interaction', () => {
    render(<Harness />);
    const element = screen.getByTestId('viewport');
    const pointer = capture(element);
    fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
    for (const end of [fireEvent.pointerCancel, fireEvent.lostPointerCapture]) {
      fireEvent.pointerDown(element, { pointerId: 1, button: 0, clientX: 100, clientY: 100 });
      fireEvent.pointerMove(element, { pointerId: 1, clientX: 90, clientY: 90 });
      end(element, { pointerId: 1 });
      expect(element).toHaveAttribute('data-panning', 'false');
    }
    expect(pointer.release).toHaveBeenCalledTimes(2);
    expect(wheel(element, { ctrlKey: true }).defaultPrevented).toBe(true);
  });

  it.each(['outside release', 'window blur'] as const)(
    'ends a pending gesture after %s outside the viewport',
    (end) => {
      render(<Harness />);
      const element = screen.getByTestId('viewport');
      const pointer = capture(element);
      fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
      fireEvent.pointerDown(screen.getByTestId('canvas'), { pointerId: 7, button: 0, clientX: 100, clientY: 100 });
      if (end === 'outside release') fireEvent.pointerUp(document.body, { pointerId: 7 });
      else fireEvent(window, new Event('blur'));
      const left = element.scrollLeft;
      fireEvent.pointerMove(element, { pointerId: 7, buttons: 0, clientX: 40, clientY: 40 });
      expect(element).toHaveAttribute('data-panning', 'false');
      expect(pointer.set).not.toHaveBeenCalled();
      expect(element.scrollLeft).toBe(left);
    }
  );

  it('leaves nested controls, selectable text and active text selection untouched', () => {
    render(<Harness />);
    const element = screen.getByTestId('viewport');
    for (const target of [
      screen.getByTestId('nested-button'),
      screen.getByRole('textbox'),
      screen.getByTestId('selectable'),
    ]) {
      expect(wheel(target, { ctrlKey: true }).defaultPrevented).toBe(false);
      fireEvent.pointerDown(target, { pointerId: 1, button: 0, clientX: 100, clientY: 100 });
      fireEvent.pointerMove(element, { pointerId: 1, clientX: 50, clientY: 50 });
      fireEvent.doubleClick(target);
      expect(element).toHaveAttribute('data-scale', '0.4');
      expect(element).toHaveAttribute('data-panning', 'false');
    }
    vi.spyOn(document, 'getSelection').mockReturnValue({ isCollapsed: false } as Selection);
    expect(wheel(element, { metaKey: true }).defaultPrevented).toBe(false);
    fireEvent.pointerDown(element, { pointerId: 2, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(element, { pointerId: 2, clientX: 50, clientY: 50 });
    expect(element).toHaveAttribute('data-panning', 'false');
  });

  it('never borrows owner geometry and rejects late gestures/resize even after an A-B-A switch', () => {
    const observed = vi.fn();
    const rendered = render(<Harness observed={observed} />);
    const element = screen.getByTestId('viewport');
    fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
    const previous = latest,
      oldResize = observers[0].resize;
    observed.mockClear();
    rendered.rerender(<Harness owner='b' natural={null} observed={observed} />);
    expect(observed.mock.calls[0][0].geometry).toBeNull();
    expect(observed.mock.calls[0][0].mode.kind).toBe('fit');
    act(() => {
      previous.zoom(1);
      oldResize();
    });
    expect(element).toHaveAttribute('data-scale', 'none');
    observed.mockClear();
    rendered.rerender(<Harness owner='a' observed={observed} />);
    expect(observed.mock.calls[0][0].geometry).toBeNull();
    expect(element).toHaveAttribute('data-scale', '0.4');
    act(() => {
      previous.zoom(1);
      previous.changeMode({ kind: 'scale', scale: 7 });
      oldResize();
    });
    expect(element).toHaveAttribute('data-scale', '0.4');
    expect(observers[0].disconnect).toHaveBeenCalledOnce();
  });

  it('retains the normalized reading center when the measured viewport changes', () => {
    render(<Harness />);
    const element = screen.getByTestId('viewport');
    fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
    element.scrollLeft = 800;
    element.scrollTop = 400;
    const center = imageViewCenter(latest.geometry!, element.scrollLeft, element.scrollTop);
    act(() => {
      width = 632;
      height = 332;
      observers.at(-1)!.resize();
    });
    expect(imageViewCenter(latest.geometry!, element.scrollLeft, element.scrollTop)).toEqual(center);
  });

  it('releases captured pointers and disconnects on unmount; old callbacks cannot update state', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    const rendered = render(
      <React.StrictMode>
        <Harness />
      </React.StrictMode>
    );
    const element = screen.getByTestId('viewport'),
      pointer = capture(element);
    fireEvent.click(screen.getByRole('button', { name: /^Actual$/ }));
    fireEvent.pointerDown(element, { pointerId: 4, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(element, { pointerId: 4, clientX: 80, clientY: 80 });
    const previous = latest,
      callbacks = observers.map((observer) => observer.resize);
    rendered.unmount();
    expect(pointer.release).toHaveBeenCalledWith(4);
    expect(observers.every((observer) => observer.disconnect.mock.calls.length === 1)).toBe(true);
    act(() => {
      callbacks.forEach((callback) => callback());
      previous.zoom(1);
    });
    expect(error).not.toHaveBeenCalled();
  });
});
