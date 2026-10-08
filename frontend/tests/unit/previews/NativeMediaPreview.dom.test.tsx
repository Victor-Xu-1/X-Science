import AudioPreview from '@/renderer/pages/artifact/AudioPreview';
import VideoPreview from '@/renderer/pages/artifact/VideoPreview';
import MediaPreview from '@/renderer/pages/conversation/Preview/components/viewers/MediaPreview';
import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const variants = [
  { name: 'board audio', kind: 'audio' as const, artifact: false },
  { name: 'board video', kind: 'video' as const, artifact: false },
  { name: 'artifact audio', kind: 'audio' as const, artifact: true },
  { name: 'artifact video', kind: 'video' as const, artifact: true },
];
async function view(variant: (typeof variants)[number], source = '/api/artifacts/versions/media-a') {
  const node = (url: string, filename = 'recording') =>
    variant.artifact ? (
      variant.kind === 'audio' ? (
        <AudioPreview url={url} filename={filename} />
      ) : (
        <VideoPreview url={url} filename={filename} />
      )
    ) : (
      <MediaPreview mediaType={variant.kind} filename={filename} content={url} />
    );
  const result = await renderWithI18n(node(source), 'en-US');
  const media = result.container.querySelector(variant.kind) as HTMLMediaElement;
  return { ...result, media, change: (url: string, name?: string) => result.rerender(node(url, name)) };
}
beforeEach(() => vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {}));
afterEach(async () => {
  cleanup();
  await Promise.resolve();
  vi.restoreAllMocks();
});

describe.each(variants)('$name lifecycle', (variant) => {
  it('shows already available cached metadata without another native load event', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'readyState', 'get').mockReturnValue(1);
    const { media } = await view(variant);
    expect(media.style.display).not.toBe('none');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('offers manual same-source retry after a media read failure', async () => {
    const { media, container } = await view(variant);
    fireEvent.error(media);
    expect(screen.getByRole('alert')).toHaveTextContent(
      variant.kind === 'audio' ? 'Failed to load audio' : 'Failed to load video'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry recording' }));
    const retried = container.querySelector(variant.kind)!;
    expect(retried).not.toBe(media);
    expect(retried).toHaveAttribute('src', '/api/artifacts/versions/media-a');
    fireEvent.loadedMetadata(retried);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('stops and releases only its detached native player when disposed', async () => {
    const paused = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {});
    const load = vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, 'paused', 'get').mockReturnValue(false);
    const { media, unmount } = await view(variant);
    unmount();
    await act(async () => {});
    expect(paused).toHaveBeenCalled();
    expect(media).not.toHaveAttribute('src');
    expect(load).toHaveBeenCalled();
  });

  it('does not restart a healthy source when only its display name changes', async () => {
    const { media, change, container } = await view(variant);
    fireEvent.loadedMetadata(media);
    change('/api/artifacts/versions/media-a', 'renamed');
    expect(container.querySelector(variant.kind)).toBe(media);
    expect(media.style.display).not.toBe('none');
    expect(media).toHaveAttribute('aria-label', 'renamed');
  });

  it('releases an old source without resetting the replacement or accepting its stale events', async () => {
    const { media, change, container } = await view(variant);
    change('/api/artifacts/versions/media-b');
    const current = container.querySelector(variant.kind)!;
    await act(async () => {});
    expect(media).not.toHaveAttribute('src');
    expect(current).toHaveAttribute('src', '/api/artifacts/versions/media-b');
    fireEvent.error(media);
    fireEvent.loadedMetadata(media);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toBeInTheDocument();
    fireEvent.loadedMetadata(current);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('returns retry focus to playback without stealing a later outside destination', async () => {
    const { media, container } = await view(variant);
    fireEvent.error(media);
    const retry = screen.getByRole('button', { name: 'Retry recording' });
    retry.focus();
    fireEvent.click(retry);
    const first = container.querySelector(variant.kind)!;
    fireEvent.loadedMetadata(first);
    expect(first).toHaveFocus();
    fireEvent.error(first);
    screen.getByRole('button', { name: 'Retry recording' }).focus();
    fireEvent.click(screen.getByRole('button', { name: 'Retry recording' }));
    const outside = document.createElement('button');
    document.body.append(outside);
    outside.focus();
    try {
      fireEvent.loadedMetadata(container.querySelector(variant.kind)!);
      expect(outside).toHaveFocus();
    } finally {
      outside.remove();
    }
  });

  it('does not create a native request or offer active retry for an unavailable disk identity', async () => {
    const { container } = await view(variant, '/workspace/private/recording');
    expect(container.querySelector(variant.kind)).toBeNull();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry recording' })).toBeDisabled();
  });
});

it.each(['audio', 'video'] as const)('keeps a connected %s source intact during Strict replay', async (kind) => {
  vi.spyOn(HTMLMediaElement.prototype, 'readyState', 'get').mockReturnValue(1);
  const loaded = vi.spyOn(HTMLMediaElement.prototype, 'load');
  const { container } = await renderWithI18n(
    <React.StrictMode>
      <MediaPreview mediaType={kind} filename='strict' content='/api/artifacts/versions/strict' />
    </React.StrictMode>,
    'en-US'
  );
  await act(async () => {});
  expect(container.querySelector(kind)).toHaveAttribute('src', '/api/artifacts/versions/strict');
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  expect(loaded).not.toHaveBeenCalled();
});
