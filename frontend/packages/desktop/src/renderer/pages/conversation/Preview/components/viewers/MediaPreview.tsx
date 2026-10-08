/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Attention } from '@icon-park/react';
import React, { useLayoutEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { buildPdfSrc } from '../../previewUrls';
import { useNativeMediaRead } from './useNativeMediaRead';

type MediaPreviewProps = {
  mediaType: 'audio' | 'video';
  filename: string;
  content?: string;
  filePath?: string;
};

const NativeMediaSession: React.FC<Pick<MediaPreviewProps, 'mediaType' | 'filename'> & { source: string }> = ({
  mediaType,
  filename,
  source,
}) => {
  const { t } = useTranslation();
  const media = useNativeMediaRead(source, mediaType);
  const retryFocus = useRef(false);
  useLayoutEffect(() => {
    if (media.status !== 'ready' || !retryFocus.current) return;
    retryFocus.current = false;
    if (document.activeElement === document.body) media.elementRef.current?.focus({ preventScroll: true });
  }, [media.status]);

  const failedMessage =
    mediaType === 'audio' ? t('preview.artifact.audioLoadFailed') : t('preview.artifact.videoLoadFailed');
  const mediaProps = {
    key: media.attempt,
    src: source,
    controls: true,
    preload: 'metadata' as const,
    tabIndex: 0,
    onLoadedMetadata: (event: React.SyntheticEvent<HTMLMediaElement>) => media.ready(event.currentTarget),
    onCanPlay: (event: React.SyntheticEvent<HTMLMediaElement>) => media.ready(event.currentTarget),
    onError: (event: React.SyntheticEvent<HTMLMediaElement>) => media.failed(event.currentTarget),
  };

  return (
    <section
      className='relative size-full flex-center overflow-hidden bg-fill-2'
      aria-label={t('preview.artifact.mediaPreviewNamed', { name: filename })}
    >
      {media.status === 'loading' ? (
        <div className='absolute inset-0 flex-center' role='status' aria-label={t('common.loading')}>
          <span className='size-32px animate-spin rounded-full border-2 border-solid border-fill-4 border-t-transparent' />
        </div>
      ) : null}
      {media.status === 'failed' ? (
        <div className='text-center text-t-secondary' role='alert'>
          <Attention size={52} className='mx-auto mb-12px text-danger-6' />
          <p>{failedMessage}</p>
          <button
            type='button'
            disabled={!source}
            className='min-h-32px rounded-7px border-0 bg-fill-1 px-12px text-12px text-t-primary disabled:opacity-40'
            aria-label={t('preview.artifact.mediaRetryNamed', { name: filename })}
            onClick={() => {
              retryFocus.current = true;
              media.retry();
            }}
          >
            {t('common.retry')}
          </button>
        </div>
      ) : null}
      {source && mediaType === 'audio' ? (
        <audio
          {...mediaProps}
          ref={(node) => {
            media.elementRef.current = node;
          }}
          aria-label={filename}
          className='w-full max-w-720px px-24px'
          style={{ display: media.status === 'ready' ? 'block' : 'none' }}
        />
      ) : source ? (
        <video
          {...mediaProps}
          ref={(node) => {
            media.elementRef.current = node;
          }}
          aria-label={filename}
          playsInline
          className='size-full object-contain'
          style={{ display: media.status === 'ready' ? 'block' : 'none' }}
        />
      ) : null}
    </section>
  );
};

const MediaPreview: React.FC<MediaPreviewProps> = ({ mediaType, filename, content, filePath }) => {
  const source = buildPdfSrc(filePath, content);
  return <NativeMediaSession key={source} source={source} mediaType={mediaType} filename={filename} />;
};

export default MediaPreview;
