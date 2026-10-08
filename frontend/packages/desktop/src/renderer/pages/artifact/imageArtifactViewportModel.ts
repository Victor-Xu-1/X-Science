/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

export type ImageDimensions = { width: number; height: number };
export type ImageViewportMode = { kind: 'fit' } | { kind: 'scale'; scale: number };
export type ImageViewportGeometry = {
  scale: number;
  minScale: number;
  maxScale: number;
  width: number;
  height: number;
  stageWidth: number;
  stageHeight: number;
  offsetX: number;
  offsetY: number;
  viewport: ImageDimensions;
};
export const IMAGE_VIEWPORT_PADDING = 16;
export const IMAGE_ZOOM_FACTOR = 1.25;

export function validImageDimensions(size: ImageDimensions): boolean {
  return Number.isFinite(size.width) && Number.isFinite(size.height) && size.width > 0 && size.height > 0;
}

export function imageViewportGeometry(
  natural: ImageDimensions,
  viewport: ImageDimensions,
  mode: ImageViewportMode
): ImageViewportGeometry {
  if (!validImageDimensions(natural)) throw new Error('invalid_image_dimensions');
  const fit = validImageDimensions(viewport)
    ? Math.min(viewport.width / natural.width, viewport.height / natural.height)
    : 1;
  const minScale = Math.min(0.1, fit);
  const maxScale = Math.max(8, fit);
  const requested = mode.kind === 'fit' ? fit : Number.isFinite(mode.scale) ? mode.scale : 1;
  const scale = Math.max(minScale, Math.min(maxScale, requested));
  const width = natural.width * scale;
  const height = natural.height * scale;
  const stageWidth = Math.max(viewport.width, width);
  const stageHeight = Math.max(viewport.height, height);
  return {
    scale,
    minScale,
    maxScale,
    width,
    height,
    stageWidth,
    stageHeight,
    offsetX: (stageWidth - width) / 2,
    offsetY: (stageHeight - height) / 2,
    viewport,
  };
}

const clampUnit = (value: number) => Math.max(0, Math.min(1, value));

export function imageViewCenter(geometry: ImageViewportGeometry, scrollLeft: number, scrollTop: number) {
  return {
    x: clampUnit((scrollLeft + geometry.viewport.width / 2 - geometry.offsetX) / geometry.width),
    y: clampUnit((scrollTop + geometry.viewport.height / 2 - geometry.offsetY) / geometry.height),
  };
}

export function imageViewScroll(geometry: ImageViewportGeometry, center: { x: number; y: number }) {
  return {
    left: Math.max(
      0,
      Math.min(
        geometry.stageWidth - geometry.viewport.width,
        center.x * geometry.width + geometry.offsetX - geometry.viewport.width / 2
      )
    ),
    top: Math.max(
      0,
      Math.min(
        geometry.stageHeight - geometry.viewport.height,
        center.y * geometry.height + geometry.offsetY - geometry.viewport.height / 2
      )
    ),
  };
}
