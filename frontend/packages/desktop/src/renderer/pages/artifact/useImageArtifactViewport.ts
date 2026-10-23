/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useLayoutEffect, useRef, useState } from 'react';
import { validImageDimensions, type ImageDimensions } from './imageArtifactViewportModel';
import { useArtifactCanvasViewport } from './useArtifactCanvasViewport';

export function useImageArtifactViewport(contentUrl: string) {
  const imageRef = useRef<HTMLImageElement>(null);
  const [revision, setRevision] = useState(0);
  const owner = JSON.stringify([contentUrl, revision]);
  const currentOwner = useRef(owner);
  const [read, setRead] = useState<{ owner: string; natural: ImageDimensions | null; failed: boolean }>({
    owner,
    natural: null,
    failed: false,
  });
  const natural = read.owner === owner ? read.natural : null;
  const viewport = useArtifactCanvasViewport(owner, natural);

  useLayoutEffect(() => {
    currentOwner.current = owner;
    // Cached loads (and retained DOM after fast refresh) may not dispatch a new
    // load event. The current node's decoded geometry is the readiness signal.
    const image = imageRef.current;
    const completed = image?.complete && image.getAttribute('src') === contentUrl;
    const size = completed ? { width: image.naturalWidth, height: image.naturalHeight } : null;
    const decoded = size !== null && validImageDimensions(size);
    setRead({ owner, natural: decoded ? size : null, failed: !!completed && !decoded });
  }, [owner, contentUrl]);

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

  return {
    owner,
    imageRef,
    viewportRef: viewport.viewportRef,
    geometry: viewport.geometry,
    mode: viewport.mode,
    loaded,
    failed,
    zoom: viewport.zoom,
    changeMode: viewport.changeMode,
    keyDown: viewport.keyDown,
    retry: () => setRevision((value) => value + 1),
    status:
      read.owner !== owner || (!read.natural && !read.failed)
        ? ('loading' as const)
        : read.failed
          ? ('failed' as const)
          : ('ready' as const),
  };
}
