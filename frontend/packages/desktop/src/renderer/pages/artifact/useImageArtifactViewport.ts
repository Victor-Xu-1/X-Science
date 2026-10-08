/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useMemo, useRef, useState } from 'react';
import type React from 'react';
import {
  IMAGE_VIEWPORT_PADDING,
  IMAGE_ZOOM_FACTOR,
  imageViewportGeometry,
  imageViewCenter,
  imageViewScroll,
  validImageDimensions,
  type ImageDimensions,
  type ImageViewportMode,
} from './imageArtifactViewportModel';

export function useImageArtifactViewport(contentUrl: string) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const imageRef = useRef<HTMLImageElement>(null);
  const [revision, setRevision] = useState(0);
  const owner = JSON.stringify([contentUrl, revision]);
  const currentOwner = useRef(owner);
  const [read, setRead] = useState<{ owner: string; natural: ImageDimensions | null; failed: boolean }>({
    owner,
    natural: null,
    failed: false,
  });
  const [viewport, setViewport] = useState<ImageDimensions>({ width: 0, height: 0 });
  const [mode, setMode] = useState<ImageViewportMode>({ kind: 'fit' });
  const pendingCenter = useRef<{ x: number; y: number } | null>({ x: 0.5, y: 0.5 });
  const natural = read.owner === owner ? read.natural : null;
  const geometry = useMemo(
    () => (natural ? imageViewportGeometry(natural, viewport, mode) : null),
    [natural, viewport, mode]
  );
  const latestGeometry = useRef(geometry);

  useLayoutEffect(() => {
    currentOwner.current = owner;
    // Cached loads (and retained DOM after fast refresh) may not dispatch a new
    // load event. The current node's decoded geometry is the readiness signal.
    const image = imageRef.current;
    const completed = image?.complete && image.getAttribute('src') === contentUrl;
    const size = completed ? { width: image.naturalWidth, height: image.naturalHeight } : null;
    const decoded = size !== null && validImageDimensions(size);
    setRead({ owner, natural: decoded ? size : null, failed: !!completed && !decoded });
    setMode({ kind: 'fit' });
    pendingCenter.current = { x: 0.5, y: 0.5 };
    const element = viewportRef.current;
    if (element) {
      element.scrollLeft = 0;
      element.scrollTop = 0;
    }
  }, [owner, contentUrl]);

  useLayoutEffect(() => {
    const element = viewportRef.current;
    if (!element) return;
    const measure = () => {
      const width = Math.max(0, element.clientWidth - IMAGE_VIEWPORT_PADDING * 2);
      const height = Math.max(0, element.clientHeight - IMAGE_VIEWPORT_PADDING * 2);
      const previous = latestGeometry.current;
      if (previous) pendingCenter.current = imageViewCenter(previous, element.scrollLeft, element.scrollTop);
      setViewport((previousSize) =>
        previousSize.width === width && previousSize.height === height ? previousSize : { width, height }
      );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useLayoutEffect(() => {
    latestGeometry.current = geometry;
    const element = viewportRef.current;
    const center = pendingCenter.current;
    if (!element || !geometry || !center) return;
    const scroll = imageViewScroll(geometry, center);
    element.scrollLeft = scroll.left;
    element.scrollTop = scroll.top;
    pendingCenter.current = null;
  }, [geometry]);

  const isCurrentImage = (image: HTMLImageElement) =>
    currentOwner.current === owner && imageRef.current === image && image.getAttribute('src') === contentUrl;
  const loaded = (image: HTMLImageElement) => {
    if (!isCurrentImage(image)) return;
    const size = { width: image.naturalWidth, height: image.naturalHeight };
    setRead({ owner, natural: validImageDimensions(size) ? size : null, failed: !validImageDimensions(size) });
  };
  const failed = (image: HTMLImageElement) => {
    if (isCurrentImage(image)) setRead({ owner, natural: null, failed: true });
  };
  const changeMode = (next: ImageViewportMode) => {
    const element = viewportRef.current;
    if (!geometry || !element) return;
    pendingCenter.current =
      next.kind === 'fit' ? { x: 0.5, y: 0.5 } : imageViewCenter(geometry, element.scrollLeft, element.scrollTop);
    setMode(next);
  };
  const zoom = (direction: 1 | -1) => {
    if (geometry) changeMode({ kind: 'scale', scale: geometry.scale * IMAGE_ZOOM_FACTOR ** direction });
  };
  const keyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget || event.ctrlKey || event.metaKey || event.altKey || !geometry) return;
    if (event.key === '+' || event.key === '=') zoom(1);
    else if (event.key === '-') zoom(-1);
    else if (event.key === '0') changeMode({ kind: 'scale', scale: 1 });
    else if (event.key.toLowerCase() === 'f') changeMode({ kind: 'fit' });
    else if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
      const element = event.currentTarget;
      element.scrollLeft = Math.max(
        0,
        Math.min(
          geometry.stageWidth - geometry.viewport.width,
          element.scrollLeft + (event.key === 'ArrowLeft' ? -40 : event.key === 'ArrowRight' ? 40 : 0)
        )
      );
      element.scrollTop = Math.max(
        0,
        Math.min(
          geometry.stageHeight - geometry.viewport.height,
          element.scrollTop + (event.key === 'ArrowUp' ? -40 : event.key === 'ArrowDown' ? 40 : 0)
        )
      );
    } else return;
    event.preventDefault();
  };

  return {
    owner,
    imageRef,
    viewportRef,
    geometry,
    mode,
    loaded,
    failed,
    zoom,
    changeMode,
    keyDown,
    retry: () => setRevision((value) => value + 1),
    status:
      read.owner !== owner || (!read.natural && !read.failed)
        ? ('loading' as const)
        : read.failed
          ? ('failed' as const)
          : ('ready' as const),
  };
}
