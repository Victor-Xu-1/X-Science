/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type React from 'react';
import {
  IMAGE_VIEWPORT_PADDING,
  IMAGE_ZOOM_FACTOR,
  imageViewportGeometry,
  imageViewCenter,
  imageViewScroll,
  validImageDimensions,
  type ImageDimensions,
  type ImageViewportGeometry,
  type ImageViewportMode,
} from './imageArtifactViewportModel';

type OwnerToken = { owner: string };
type ViewState = { token: OwnerToken; size: ImageDimensions; mode: ImageViewportMode; measured: boolean };
type LiveViewport = { token: OwnerToken; element: HTMLDivElement; geometry: ImageViewportGeometry | null };
type Pan = {
  live: LiveViewport;
  pointerId: number;
  x: number;
  y: number;
  left: number;
  top: number;
  dragging: boolean;
  removeGlobal?: () => void;
};
const FIT: ImageViewportMode = { kind: 'fit' };

function releasePan(pan: Pan | null) {
  if (!pan) return;
  const remove = pan.removeGlobal;
  pan.removeGlobal = undefined;
  remove?.();
  if (!pan.dragging) return;
  try {
    if (pan.live.element.hasPointerCapture?.(pan.pointerId) !== false)
      pan.live.element.releasePointerCapture?.(pan.pointerId);
  } catch {
    // The browser may have already canceled/released this pointer.
  }
}

function protectedTarget(target: EventTarget | null, viewport: HTMLDivElement) {
  if (target instanceof Element) {
    if (
      target.closest(
        'button,input,textarea,select,a,[role="button"],[role="textbox"],[contenteditable]:not([contenteditable="false"])'
      )
    )
      return true;
    // A retained PDF text layer may be a non-selectable pan surface. Native
    // canvas fallback/structure text must not turn the canvas into a text input.
    const surface = target.closest('canvas,img,[data-canvas-pan-surface]');
    if (target !== viewport && !(surface && viewport.contains(surface)) && target.textContent?.trim()) return true;
  }
  return viewport.ownerDocument.getSelection()?.isCollapsed === false;
}

export function useArtifactCanvasViewport(
  owner: string,
  natural: ImageDimensions | null,
  options: { fitRelativeZoom?: boolean } = {}
) {
  const token = useMemo(() => ({ owner }), [owner]);
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const [view, setView] = useState<ViewState>({ token, size: { width: 0, height: 0 }, mode: FIT, measured: false });
  const [panToken, setPanToken] = useState<OwnerToken | null>(null);
  const liveRef = useRef<LiveViewport | null>(null);
  const panRef = useRef<Pan | null>(null);
  const suppressClick = useRef<OwnerToken | null>(null);
  const pendingCenter = useRef<{ token: OwnerToken; x: number; y: number } | null>(null);
  const removeWheel = useRef<(() => void) | null>(null);
  const mode = view.token === token ? view.mode : FIT;
  const geometry = useMemo(
    () =>
      natural && validImageDimensions(natural) && view.token === token && view.measured
        ? imageViewportGeometry(natural, view.size, mode, { fitRelativeZoom: options.fitRelativeZoom })
        : null,
    [natural, view, token, mode, options.fitRelativeZoom]
  );
  const current = () => {
    const live = liveRef.current;
    return live?.token === token && viewportRef.current === live.element ? live : null;
  };

  useLayoutEffect(() => {
    const element = viewportRef.current;
    if (!element) return;
    const live: LiveViewport = { token, element, geometry: null };
    liveRef.current = live;
    pendingCenter.current = { token, x: 0.5, y: 0.5 };
    suppressClick.current = null;
    element.scrollLeft = 0;
    element.scrollTop = 0;
    const measure = () => {
      if (liveRef.current !== live || viewportRef.current !== element) return;
      const size = {
        width: Math.max(0, element.clientWidth - IMAGE_VIEWPORT_PADDING * 2),
        height: Math.max(0, element.clientHeight - IMAGE_VIEWPORT_PADDING * 2),
      };
      if (live.geometry)
        pendingCenter.current = { token, ...imageViewCenter(live.geometry, element.scrollLeft, element.scrollTop) };
      setView((previous) =>
        previous.token === token
          ? previous.measured && previous.size.width === size.width && previous.size.height === size.height
            ? previous
            : { ...previous, size, measured: true }
          : { token, size, mode: FIT, measured: true }
      );
    };
    measure();
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
    observer?.observe(element);
    return () => {
      observer?.disconnect();
      if (liveRef.current !== live) return;
      liveRef.current = null;
      releasePan(panRef.current);
      panRef.current = null;
      pendingCenter.current = null;
      suppressClick.current = null;
    };
  }, [token]);

  useLayoutEffect(() => {
    const live = current();
    if (!live) return;
    live.geometry = geometry;
    if (!geometry) {
      releasePan(panRef.current);
      panRef.current = null;
      suppressClick.current = null;
      setPanToken(null);
      return;
    }
    const center = pendingCenter.current;
    if (center?.token !== token) return;
    const scroll = imageViewScroll(geometry, center);
    live.element.scrollLeft = scroll.left;
    live.element.scrollTop = scroll.top;
    pendingCenter.current = null;
  }, [geometry, token]);

  const changeMode = (next: ImageViewportMode) => {
    const live = current();
    if (!live?.geometry) return;
    pendingCenter.current =
      next.kind === 'fit'
        ? { token, x: 0.5, y: 0.5 }
        : { token, ...imageViewCenter(live.geometry, live.element.scrollLeft, live.element.scrollTop) };
    setView((previous) => (previous.token === token ? { ...previous, mode: next } : previous));
  };
  const zoom = (direction: 1 | -1) => {
    const live = current();
    if (live?.geometry) changeMode({ kind: 'scale', scale: live.geometry.scale * IMAGE_ZOOM_FACTOR ** direction });
  };
  const keyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const live = current();
    if (
      !live?.geometry ||
      event.currentTarget !== live.element ||
      event.target !== event.currentTarget ||
      event.ctrlKey ||
      event.metaKey ||
      event.altKey
    )
      return;
    if (event.key === '+' || event.key === '=') zoom(1);
    else if (event.key === '-') zoom(-1);
    else if (event.key === '0') changeMode({ kind: 'scale', scale: 1 });
    else if (event.key.toLowerCase() === 'f') changeMode(FIT);
    else if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
      const element = live.element;
      element.scrollLeft = Math.max(
        0,
        Math.min(
          live.geometry.stageWidth - live.geometry.viewport.width,
          element.scrollLeft + (event.key === 'ArrowLeft' ? -40 : event.key === 'ArrowRight' ? 40 : 0)
        )
      );
      element.scrollTop = Math.max(
        0,
        Math.min(
          live.geometry.stageHeight - live.geometry.viewport.height,
          element.scrollTop + (event.key === 'ArrowUp' ? -40 : event.key === 'ArrowDown' ? 40 : 0)
        )
      );
    } else return;
    event.preventDefault();
  };

  const finishPan = (pan: Pan, suppress: boolean) => {
    if (panRef.current !== pan) return;
    panRef.current = null;
    if (liveRef.current === pan.live) {
      if (suppress && pan.dragging) suppressClick.current = token;
      setPanToken(null);
    }
    releasePan(pan);
  };
  const pointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    const live = current();
    if (
      !live?.geometry ||
      event.currentTarget !== live.element ||
      event.button !== 0 ||
      event.isPrimary === false ||
      event.ctrlKey ||
      event.metaKey ||
      event.altKey ||
      event.shiftKey ||
      protectedTarget(event.target, live.element)
    )
      return;
    releasePan(panRef.current);
    setPanToken(null);
    suppressClick.current = null;
    const pan: Pan = {
      live,
      pointerId: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      left: live.element.scrollLeft,
      top: live.element.scrollTop,
      dragging: false,
    };
    panRef.current = pan;
    // Before the movement threshold there is deliberately no pointer capture.
    // A release outside this element or a window blur must still end that
    // pending gesture; listeners belong only to this one live pointer.
    const ended = (event: PointerEvent) => {
      if (event.pointerId === pan.pointerId) finishPan(pan, true);
    };
    const blurred = () => finishPan(pan, false);
    const document = live.element.ownerDocument;
    document.addEventListener('pointerup', ended, true);
    document.addEventListener('pointercancel', ended, true);
    document.defaultView?.addEventListener('blur', blurred);
    pan.removeGlobal = () => {
      document.removeEventListener('pointerup', ended, true);
      document.removeEventListener('pointercancel', ended, true);
      document.defaultView?.removeEventListener('blur', blurred);
    };
  };
  const pointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const live = current();
    const pan = panRef.current;
    if (
      !live?.geometry ||
      !pan ||
      pan.live !== live ||
      event.currentTarget !== live.element ||
      event.pointerId !== pan.pointerId
    )
      return;
    if ((event.buttons & 1) === 0) {
      finishPan(pan, false);
      return;
    }
    const dx = event.clientX - pan.x,
      dy = event.clientY - pan.y;
    if (!pan.dragging) {
      if (dx * dx + dy * dy < 16) return;
      try {
        live.element.setPointerCapture?.(pan.pointerId);
      } catch {
        finishPan(pan, false);
        return;
      }
      pan.dragging = true;
      setPanToken(token);
    }
    live.element.scrollLeft = Math.max(
      0,
      Math.min(live.geometry.stageWidth - live.geometry.viewport.width, pan.left - dx)
    );
    live.element.scrollTop = Math.max(
      0,
      Math.min(live.geometry.stageHeight - live.geometry.viewport.height, pan.top - dy)
    );
    event.preventDefault();
  };
  const pointerEnd = (event: React.PointerEvent<HTMLDivElement>) => {
    const live = current(),
      pan = panRef.current;
    if (!live || !pan || pan.live !== live || event.currentTarget !== live.element || event.pointerId !== pan.pointerId)
      return;
    finishPan(pan, true);
  };
  const clickCapture = (event: React.MouseEvent<HTMLDivElement>) => {
    const live = current(),
      suppress = suppressClick.current;
    if (!live || event.currentTarget !== live.element) return;
    suppressClick.current = null;
    if (suppress !== token || event.detail === 0 || protectedTarget(event.target, live.element)) return;
    event.preventDefault();
    event.stopPropagation();
  };
  const doubleClick = (event: React.MouseEvent<HTMLDivElement>) => {
    const live = current();
    if (!live?.geometry || event.currentTarget !== live.element || protectedTarget(event.target, live.element)) return;
    event.preventDefault();
    changeMode(FIT);
  };
  const wheel = (event: WheelEvent) => {
    const live = current();
    if (
      !live?.geometry ||
      event.currentTarget !== live.element ||
      (!event.ctrlKey && !event.metaKey) ||
      event.altKey ||
      !Number.isFinite(event.deltaY) ||
      event.deltaY === 0 ||
      protectedTarget(event.target, live.element)
    )
      return;
    event.preventDefault();
    zoom(event.deltaY < 0 ? 1 : -1);
  };
  // Opt-in only. React's delegated wheel listener may be passive, so PDF binds
  // this ref for a cancellable native listener; existing images keep viewportRef.
  const gestureRef = useCallback(
    (element: HTMLDivElement | null) => {
      removeWheel.current?.();
      removeWheel.current = null;
      viewportRef.current = element;
      if (element) {
        element.addEventListener('wheel', wheel, { passive: false });
        removeWheel.current = () => element.removeEventListener('wheel', wheel);
      } else {
        releasePan(panRef.current);
        panRef.current = null;
      }
    },
    [token]
  );

  return {
    viewportRef,
    geometry,
    mode,
    zoom,
    changeMode,
    keyDown,
    gestureRef,
    panHandlers: {
      onPointerDown: pointerDown,
      onPointerMove: pointerMove,
      onPointerUp: pointerEnd,
      onPointerCancel: pointerEnd,
      onLostPointerCapture: pointerEnd,
    },
    doubleClick,
    clickCapture,
    isPanning: panToken === token && geometry !== null,
  };
}
