import type { SynonBiomedArtifactAnnotation } from '@/renderer/services/synonBiomedAnnotations';
import React, { useCallback, useEffect, useId, useLayoutEffect, useRef } from 'react';
import { Button, Spin } from '@arco-design/web-react';
import { Minus, Plus } from '@icon-park/react';
import { useTranslation } from 'react-i18next';
import { clampPercent, type SynonBiomedArtifactPointSelection } from './artifactCanvasSelection';
import { useImageArtifactViewport } from './useImageArtifactViewport';

export const SynonBiomedImageArtifactViewer: React.FC<{
  filename: string;
  contentUrl: string;
  annotations?: SynonBiomedArtifactAnnotation[];
  onSelectionChange: (selection: SynonBiomedArtifactPointSelection | null) => void;
  onAnnotationClick?: (annotation: SynonBiomedArtifactAnnotation) => void;
}> = ({ filename, contentUrl, annotations = [], onSelectionChange, onAnnotationClick }) => {
  const { t } = useTranslation();
  const viewport = useImageArtifactViewport(contentUrl);
  const helpId = useId();
  const selectionCallback = useRef(onSelectionChange);
  const retryFocus = useRef(false);
  useLayoutEffect(() => {
    selectionCallback.current = onSelectionChange;
  }, [onSelectionChange]);
  useLayoutEffect(() => {
    retryFocus.current = false;
  }, [contentUrl]);
  useLayoutEffect(() => {
    if (viewport.status !== 'ready' || !retryFocus.current) return;
    retryFocus.current = false;
    if (document.activeElement === document.body) viewport.viewportRef.current?.focus({ preventScroll: true });
  }, [viewport.status]);

  useEffect(() => {
    selectionCallback.current(null);
  }, [contentUrl]);

  const selectPoint = useCallback(
    (event: React.MouseEvent<HTMLImageElement>) => {
      if (viewport.status !== 'ready') return;
      const rect = event.currentTarget.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) return;
      const xPercent = clampPercent(((event.clientX - rect.left) / rect.width) * 100);
      const yPercent = clampPercent(((event.clientY - rect.top) / rect.height) * 100);
      onSelectionChange({
        type: 'point',
        text: t('preview.artifactAnnotations.imagePosition', {
          x: xPercent.toFixed(1),
          y: yPercent.toFixed(1),
        }),
        x: event.clientX,
        y: event.clientY,
        xPercent,
        yPercent,
        pageNumber: null,
      });
    },
    [onSelectionChange, t, viewport.status]
  );

  return (
    <div className='size-full min-h-360px min-w-0 flex flex-col bg-fill-1'>
      <div
        className='shrink-0 px-12px py-8px flex flex-wrap items-center gap-6px border-b border-solid border-[var(--color-border-2)]'
        role='group'
        aria-label={t('preview.image.controls')}
      >
        <Button
          size='small'
          aria-label={t('preview.image.zoomOut')}
          title={t('preview.image.zoomOut')}
          icon={<Minus size={14} />}
          disabled={!viewport.geometry || viewport.geometry.scale <= viewport.geometry.minScale}
          onClick={() => viewport.zoom(-1)}
        />
        <span className='min-w-48px text-center text-12px text-t-secondary' aria-live='off'>
          {viewport.geometry
            ? t('preview.image.zoomPercent', { percent: Math.round(viewport.geometry.scale * 100) })
            : '—'}
        </span>
        <Button
          size='small'
          aria-label={t('preview.image.zoomIn')}
          title={t('preview.image.zoomIn')}
          icon={<Plus size={14} />}
          disabled={!viewport.geometry || viewport.geometry.scale >= viewport.geometry.maxScale}
          onClick={() => viewport.zoom(1)}
        />
        <Button
          size='small'
          disabled={!viewport.geometry}
          aria-pressed={viewport.mode.kind === 'fit'}
          onClick={() => viewport.changeMode({ kind: 'fit' })}
        >
          {t('preview.image.fit')}
        </Button>
        <Button
          size='small'
          disabled={!viewport.geometry}
          aria-pressed={viewport.mode.kind === 'scale' && viewport.geometry?.scale === 1}
          onClick={() => viewport.changeMode({ kind: 'scale', scale: 1 })}
        >
          {t('preview.image.actualSize')}
        </Button>
        <span id={helpId} className='text-11px text-t-tertiary'>
          {t('preview.image.keyboardHint')}
        </span>
      </div>
      <div
        ref={viewport.viewportRef}
        className='relative flex-1 min-h-0 overflow-auto p-16px'
        role='region'
        aria-label={t('preview.image.viewport')}
        aria-describedby={helpId}
        aria-busy={viewport.status === 'loading'}
        tabIndex={0}
        onKeyDown={viewport.keyDown}
      >
        {viewport.status !== 'ready' && (
          <div className='absolute inset-0 flex flex-col items-center justify-center gap-10px text-12px text-t-secondary'>
            {viewport.status === 'loading' ? (
              <div role='status' className='flex items-center gap-8px'>
                <Spin size={14} />
                {t('preview.image.loading')}
              </div>
            ) : (
              <>
                <p role='alert' className='m-0'>
                  {t('preview.image.loadFailed')}
                </p>
                <Button
                  size='small'
                  aria-label={t('preview.image.retry')}
                  onClick={() => {
                    retryFocus.current = true;
                    viewport.retry();
                  }}
                >
                  {t('common.retry')}
                </Button>
              </>
            )}
          </div>
        )}
        <div
          className='grid place-items-center'
          style={
            viewport.geometry
              ? { width: viewport.geometry.stageWidth, height: viewport.geometry.stageHeight }
              : { width: '100%', height: '100%' }
          }
        >
          <div
            className='relative leading-0'
            data-testid='artifact-image-canvas'
            style={viewport.geometry ? { width: viewport.geometry.width, height: viewport.geometry.height } : undefined}
          >
            <img
              key={viewport.owner}
              ref={viewport.imageRef}
              src={contentUrl}
              alt={filename}
              draggable={false}
              className='block max-w-none max-h-none cursor-crosshair select-none'
              style={{
                width: viewport.geometry?.width,
                height: viewport.geometry?.height,
                opacity: viewport.status === 'ready' ? 1 : 0,
              }}
              onLoad={(event) => viewport.loaded(event.currentTarget)}
              onError={(event) => viewport.failed(event.currentTarget)}
              onClick={selectPoint}
            />
            {viewport.status === 'ready' &&
              annotations
                .filter(
                  (annotation) =>
                    annotation.type === 'point' && annotation.xPercent != null && annotation.yPercent != null
                )
                .map((annotation) => (
                  <button
                    type='button'
                    key={annotation.id}
                    className='absolute size-28px -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-solid border-white bg-[rgb(var(--primary-6))] text-11px leading-24px font-[700] text-white shadow-md'
                    style={{ left: `${annotation.xPercent}%`, top: `${annotation.yPercent}%` }}
                    title={annotation.text}
                    aria-label={t('preview.artifactAnnotations.viewNamed', { label: annotation.label })}
                    onClick={(event) => {
                      event.stopPropagation();
                      onAnnotationClick?.(annotation);
                    }}
                  >
                    {annotation.label || '•'}
                  </button>
                ))}
          </div>
        </div>
      </div>
    </div>
  );
};
