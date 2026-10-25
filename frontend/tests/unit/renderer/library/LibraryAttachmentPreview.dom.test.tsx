import React from 'react';
import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LibraryAttachmentPreview } from '@/renderer/pages/library/LibraryAttachmentPreview';
import { renderWithI18n } from '../../i18nTestUtils';

const { loadModule } = vi.hoisted(() => ({ loadModule: vi.fn() }));
vi.mock('@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewLoaders', () => ({
  cachePreviewModule: () => loadModule,
}));
const Viewer = ({ filename, contentUrl }: { filename: string; contentUrl: string }) => (
  <div data-testid='loaded-pdf-core' data-url={contentUrl}>
    {filename}
  </div>
);
function deferredModule() {
  let resolve!: (value: { default: typeof Viewer }) => void;
  const promise = new Promise<{ default: typeof Viewer }>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('Library PDF module loading lifecycle', () => {
  beforeEach(() => loadModule.mockReset());
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('keeps an import failure local and retries the same document in the shared core', async () => {
    loadModule.mockRejectedValueOnce(new Error('module unavailable')).mockResolvedValueOnce({ default: Viewer });
    await renderWithI18n(
      <LibraryAttachmentPreview filename='original.pdf' contentUrl='/api/literature/attachment-versions/v1/content' />,
      'en-US'
    );
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByTestId('loaded-pdf-core')).toHaveAttribute(
      'data-url',
      '/api/literature/attachment-versions/v1/content'
    );
    expect(screen.getByTestId('loaded-pdf-core')).toHaveTextContent('original.pdf');
    expect(loadModule).toHaveBeenCalledTimes(2);
  });

  it('ends an unresolved loading view and rejects its late result after an explicit retry', async () => {
    vi.useFakeTimers();
    const first = deferredModule();
    const oldViewer = vi.fn(() => <div>obsolete module result</div>);
    loadModule.mockReturnValueOnce(first.promise).mockResolvedValueOnce({ default: Viewer });
    await renderWithI18n(
      <LibraryAttachmentPreview filename='retained.pdf' contentUrl='/api/literature/attachment-versions/v2/content' />,
      'en-US'
    );
    await act(() => vi.advanceTimersByTimeAsync(20_000));
    expect(screen.getByRole('alert')).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    });
    expect(screen.getByTestId('loaded-pdf-core')).toHaveTextContent('retained.pdf');
    await act(async () => {
      first.resolve({ default: oldViewer });
      await first.promise;
    });
    expect(oldViewer).not.toHaveBeenCalled();
    expect(screen.getByTestId('loaded-pdf-core')).toHaveAttribute(
      'data-url',
      '/api/literature/attachment-versions/v2/content'
    );
  });

  it('does not mount a late module after the selected preview is closed', async () => {
    const pending = deferredModule();
    const closedViewer = vi.fn(() => <div>closed preview</div>);
    loadModule.mockReturnValue(pending.promise);
    const rendered = await renderWithI18n(
      <LibraryAttachmentPreview filename='closed.pdf' contentUrl='/api/literature/attachment-versions/v3/content' />,
      'en-US'
    );
    rendered.unmount();
    await act(async () => {
      pending.resolve({ default: closedViewer });
      await pending.promise;
    });
    expect(closedViewer).not.toHaveBeenCalled();
    expect(screen.queryByText('closed preview')).toBeNull();
  });
});
