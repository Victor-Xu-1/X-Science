import { Button, Spin } from '@arco-design/web-react';
import { Hand, MousePointer2, Shrink, ZoomIn, ZoomOut } from 'lucide-react';
import React, { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { PDFPageProxy } from 'pdfjs-dist';
import { useArtifactCanvasViewport } from './useArtifactCanvasViewport';
import { validImageDimensions, type ImageDimensions } from './imageArtifactViewportModel';

type PagePresentation = {
  pageWidth: number;
  devicePixelRatio: number;
  selectionEnabled: boolean;
  onLoad: (page: PDFPageProxy) => void;
  onRendered: () => void;
};

/** Source-aligned image presentation inside the one already loaded PDF Document. */
export function SinglePagePdfArtifactPreview({
  filename,
  owner,
  hideToolbar,
  interactive,
  renderPage,
  onError,
}: {
  filename: string;
  owner: string;
  hideToolbar: boolean;
  interactive: boolean;
  renderPage: (presentation: PagePresentation) => React.ReactNode;
  onError: (reason: unknown) => void;
}) {
  const { t } = useTranslation();
  const [natural, setNatural] = useState<ImageDimensions | null>(null);
  const [panning, setPanning] = useState(true);
  const [ready, setReady] = useState(false);
  const rendered = useCallback(() => setReady(true), []);
  const viewport = useArtifactCanvasViewport(owner, natural, { fitRelativeZoom: true });
  const loaded = useCallback(
    (page: PDFPageProxy) => {
      try {
        const { width, height } = page.getViewport({ scale: 1 });
        const next = { width, height };
        if (!validImageDimensions(next)) throw new Error('Invalid PDF page geometry');
        setNatural((previous) => (previous?.width === width && previous.height === height ? previous : next));
      } catch (reason) {
        onError(reason);
      }
    },
    [onError]
  );
  const geometry = viewport.geometry;
  // Quantized rerasterization follows zoom, not pan. Original PDF.js text and
  // annotation layers remain available in the stronger annotation extension.
  const rasterScale = geometry ? (Math.ceil((geometry.scale / geometry.minScale) * 2) / 2) * geometry.minScale : 1;
  const pageWidth = natural ? natural.width * rasterScale : 600;
  const pageHeight = natural ? natural.height * rasterScale : 800;
  const ratio = Math.min(
    Math.max(1 / rasterScale, window.devicePixelRatio || 1),
    5 / rasterScale,
    8192 / pageWidth,
    8192 / pageHeight,
    Math.sqrt((16 * 1024 * 1024) / pageWidth / pageHeight)
  );
  const render = renderPage({
    pageWidth,
    devicePixelRatio: ratio,
    selectionEnabled: interactive && !panning,
    onLoad: loaded,
    onRendered: rendered,
  });
  return (
    <div className='relative size-full overflow-hidden' data-single-page-pdf-preview>
      <div
        ref={viewport.gestureRef}
        className={`size-full overflow-auto p-16px ${panning ? 'cursor-grab select-none active:cursor-grabbing' : ''}`}
        role='region'
        aria-label={t('preview.scientific.pdf.singlePageViewport', { name: filename })}
        tabIndex={0}
        onKeyDown={viewport.keyDown}
        {...(panning ? viewport.panHandlers : {})}
        onDoubleClick={panning ? viewport.doubleClick : undefined}
        onClickCapture={viewport.clickCapture}
      >
        <div
          className='grid place-items-center'
          style={
            geometry ? { width: geometry.stageWidth, height: geometry.stageHeight } : { width: '100%', height: '100%' }
          }
        >
          <div
            className='relative shrink-0'
            style={
              geometry
                ? { width: geometry.width, height: geometry.height }
                : { width: pageWidth, height: pageHeight, visibility: 'hidden' }
            }
          >
            <div
              className='absolute left-0 top-0 origin-top-left'
              style={{
                width: pageWidth,
                height: pageHeight,
                transform: `scale(${geometry ? geometry.width / pageWidth : 1})`,
              }}
            >
              {render}
            </div>
          </div>
        </div>
      </div>
      {!ready && (
        <div
          className='pointer-events-none absolute inset-0 flex-center'
          role='status'
          aria-label={t('preview.scientific.pdf.loading')}
        >
          <Spin />
        </div>
      )}
      {!hideToolbar && geometry && (
        <div className='absolute bottom-12px right-12px z-20 flex gap-4px rd-8px border border-solid border-[var(--color-border-2)] bg-1 p-4px shadow-sm'>
          {interactive && (
            <>
              <Button
                size='mini'
                type='text'
                aria-label={t('preview.scientific.pdf.panMode')}
                aria-pressed={panning}
                onClick={() => setPanning(true)}
                icon={<Hand size={14} />}
              />
              <Button
                size='mini'
                type='text'
                aria-label={t('preview.scientific.pdf.selectMode')}
                aria-pressed={!panning}
                onClick={() => setPanning(false)}
                icon={<MousePointer2 size={14} />}
              />
            </>
          )}
          <Button
            size='mini'
            type='text'
            aria-label={t('preview.scientific.zoomIn')}
            title={t('preview.scientific.zoomIn')}
            disabled={geometry.scale >= geometry.maxScale}
            onClick={() => viewport.zoom(1)}
            icon={<ZoomIn size={14} />}
          />
          <Button
            size='mini'
            type='text'
            aria-label={t('preview.scientific.zoomOut')}
            title={t('preview.scientific.zoomOut')}
            disabled={geometry.scale <= geometry.minScale}
            onClick={() => viewport.zoom(-1)}
            icon={<ZoomOut size={14} />}
          />
          <Button
            size='mini'
            type='text'
            aria-label={t('preview.scientific.resetZoom')}
            title={t('preview.scientific.resetZoom')}
            onClick={() => viewport.changeMode({ kind: 'fit' })}
            icon={<Shrink size={14} />}
          />
        </div>
      )}
    </div>
  );
}
