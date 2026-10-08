import { act, fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SynonBiomedImageArtifactViewer } from '@/renderer/pages/artifact/SynonBiomedImageArtifactViewer';
import { renderWithI18n } from '../i18nTestUtils';

let resize: (() => void) | undefined;
beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: () => void) {
        resize = callback;
      }
      observe() {}
      disconnect() {}
    }
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function view() {
  const changed = vi.fn();
  const rendered = await renderWithI18n(
    <SynonBiomedImageArtifactViewer
      filename='wide.png'
      contentUrl='/api/artifacts/versions/image-v1'
      onSelectionChange={changed}
    />,
    'en-US'
  );
  const image = screen.getByRole('img', { name: 'wide.png' });
  Object.defineProperties(image, { naturalWidth: { value: 2000 }, naturalHeight: { value: 400 } });
  return { image, changed, rendered };
}

describe('artifact image viewport', () => {
  it('reads an already decoded cached image without waiting for another load event', async () => {
    vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(true);
    vi.spyOn(HTMLImageElement.prototype, 'naturalWidth', 'get').mockReturnValue(1600);
    vi.spyOn(HTMLImageElement.prototype, 'naturalHeight', 'get').mockReturnValue(320);
    await renderWithI18n(
      <React.StrictMode>
        <SynonBiomedImageArtifactViewer
          filename='cached.png'
          contentUrl='/api/artifacts/versions/cached-v1'
          onSelectionChange={vi.fn()}
        />
      </React.StrictMode>,
      'en-US'
    );
    expect(screen.queryByText('Loading image')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Zoom in image' })).toBeEnabled();
    expect(screen.getByRole('img', { name: 'cached.png' })).toHaveStyle({ opacity: '1' });
  });

  it('does not leave a completed but undecodable cached image waiting indefinitely', async () => {
    vi.spyOn(HTMLImageElement.prototype, 'complete', 'get').mockReturnValue(true);
    await renderWithI18n(
      <SynonBiomedImageArtifactViewer
        filename='broken.png'
        contentUrl='/api/artifacts/versions/broken-v1'
        onSelectionChange={vi.fn()}
      />,
      'en-US'
    );
    expect(screen.getByRole('alert')).toHaveTextContent('Unable to load image');
    expect(screen.getByRole('button', { name: 'Retry image' })).toBeEnabled();
  });

  it('keeps zoom disabled until decoded image geometry is known', async () => {
    const { image } = await view();
    expect(screen.getByRole('button', { name: 'Zoom in image' })).toBeDisabled();
    expect(screen.getByRole('status')).toHaveTextContent('Loading image');
    fireEvent.load(image);
    expect(screen.getByRole('button', { name: 'Zoom in image' })).toBeEnabled();
  });

  it('fits a wide image, restores actual pixels and supports local keyboard zoom', async () => {
    const { image } = await view();
    const viewport = screen.getByRole('region', { name: 'Image viewport', exact: true });
    Object.defineProperties(viewport, { clientWidth: { value: 832 }, clientHeight: { value: 432 } });
    act(() => resize?.());
    fireEvent.load(image);
    expect(image).toHaveStyle({ width: '800px', height: '160px' });
    fireEvent.click(screen.getByRole('button', { name: 'Actual image size' }));
    expect(image).toHaveStyle({ width: '2000px', height: '400px' });
    fireEvent.keyDown(viewport, { key: '+' });
    expect(image).toHaveStyle({ width: '2500px', height: '500px' });
    fireEvent.keyDown(viewport, { key: 'f' });
    expect(image).toHaveStyle({ width: '800px', height: '160px' });
    expect(image).toHaveAttribute('src', '/api/artifacts/versions/image-v1');
  });

  it('reports an image read failure and retries without changing the immutable source URI', async () => {
    const { image } = await view();
    fireEvent.error(image);
    expect(screen.getByRole('alert')).toHaveTextContent('Unable to load image');
    expect(screen.getByRole('button', { name: 'Zoom in image' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry image' }));
    expect(screen.getByRole('img', { name: 'wide.png' })).not.toBe(image);
    expect(screen.getByRole('img', { name: 'wide.png' })).toHaveAttribute('src', '/api/artifacts/versions/image-v1');
  });

  it('restores reading focus after retry but preserves an outside focus destination', async () => {
    const { image } = await view();
    fireEvent.error(image);
    screen.getByRole('button', { name: 'Retry image' }).focus();
    fireEvent.click(screen.getByRole('button', { name: 'Retry image' }));
    const firstRetry = screen.getByRole('img', { name: 'wide.png' });
    Object.defineProperties(firstRetry, { naturalWidth: { value: 2000 }, naturalHeight: { value: 400 } });
    fireEvent.load(firstRetry);
    expect(screen.getByRole('region', { name: 'Image viewport', exact: true })).toHaveFocus();
    fireEvent.error(firstRetry);
    fireEvent.click(screen.getByRole('button', { name: 'Retry image' }));
    const outside = screen.getByRole('button', { name: 'Actual image size' });
    // The toolbar is enabled only after the decoded image is available, so use
    // a separate focus destination just as navigating elsewhere during a read.
    const destination = document.createElement('button');
    document.body.append(destination);
    destination.focus();
    try {
      const secondRetry = screen.getByRole('img', { name: 'wide.png' });
      Object.defineProperties(secondRetry, { naturalWidth: { value: 2000 }, naturalHeight: { value: 400 } });
      fireEvent.load(secondRetry);
      expect(destination).toHaveFocus();
      expect(outside).toBeEnabled();
    } finally {
      destination.remove();
    }
  });

  it('keeps a source percentage point stable while the image is zoomed and panned', async () => {
    const { image, changed } = await view();
    const viewport = screen.getByRole('region', { name: 'Image viewport', exact: true });
    Object.defineProperties(viewport, { clientWidth: { value: 832 }, clientHeight: { value: 432 } });
    act(() => resize?.());
    fireEvent.load(image);
    Object.defineProperty(image, 'getBoundingClientRect', {
      value: () => ({
        left: 100 - viewport.scrollLeft,
        top: 50 - viewport.scrollTop,
        width: parseFloat(image.style.width),
        height: parseFloat(image.style.height),
      }),
    });
    let rect = image.getBoundingClientRect();
    fireEvent.click(image, { clientX: rect.left + rect.width * 0.25, clientY: rect.top + rect.height * 0.75 });
    expect(changed).toHaveBeenLastCalledWith(expect.objectContaining({ xPercent: 25, yPercent: 75 }));
    fireEvent.click(screen.getByRole('button', { name: 'Actual image size' }));
    fireEvent.keyDown(viewport, { key: 'ArrowRight' });
    rect = image.getBoundingClientRect();
    fireEvent.click(image, { clientX: rect.left + rect.width * 0.25, clientY: rect.top + rect.height * 0.75 });
    expect(changed).toHaveBeenLastCalledWith(expect.objectContaining({ xPercent: 25, yPercent: 75 }));
    expect(viewport.scrollLeft).toBeGreaterThan(0);
  });

  it('does not intercept browser shortcuts or keys originating from a child control', async () => {
    const { image } = await view();
    fireEvent.load(image);
    const viewport = screen.getByRole('region', { name: 'Image viewport', exact: true });
    const width = image.style.width;
    fireEvent.keyDown(viewport, { key: '+', ctrlKey: true });
    expect(image.style.width).toBe(width);
    fireEvent.keyDown(image, { key: '+' });
    expect(image.style.width).toBe(width);
  });

  it('disposes the old image owner and resets geometry only when its immutable source changes', async () => {
    const { image, rendered } = await view();
    fireEvent.load(image);
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in image' }));
    const width = image.style.width;
    rendered.rerender(
      <SynonBiomedImageArtifactViewer
        filename='renamed.png'
        contentUrl='/api/artifacts/versions/image-v1'
        onSelectionChange={vi.fn()}
      />
    );
    expect(screen.getByRole('img', { name: 'renamed.png' })).toHaveStyle({ width });
    expect(screen.getByRole('button', { name: 'Zoom in image' })).toBeEnabled();
    rendered.rerender(
      <SynonBiomedImageArtifactViewer
        filename='other.png'
        contentUrl='/api/artifacts/versions/image-v2'
        onSelectionChange={vi.fn()}
      />
    );
    expect(screen.getByRole('img', { name: 'other.png' })).not.toBe(image);
    fireEvent.load(image);
    expect(screen.getByRole('button', { name: 'Zoom in image' })).toBeDisabled();
    expect(screen.getByRole('status')).toHaveTextContent('Loading image');
  });
});
