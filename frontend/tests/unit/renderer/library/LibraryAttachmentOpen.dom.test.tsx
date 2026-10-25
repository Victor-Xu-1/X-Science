import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LibraryAttachmentBrowser } from '@/renderer/pages/library/LibraryAttachmentBrowser';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { attachmentItem, attachmentVersion } from './libraryAttachmentFixtures';

vi.mock('@/renderer/pages/artifact/XSciencePdfArtifactViewer', () => ({
  default: ({ filename, contentUrl }: { filename: string; contentUrl: string }) => (
    <div data-testid='shared-pdf-target' data-url={contentUrl}>
      {filename}
    </div>
  ),
}));
function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('Library attachment actions through original content owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('attachment-owner');
    const RealURL = URL;
    vi.stubGlobal(
      'URL',
      class extends RealURL {
        static createObjectURL = vi.fn(() => 'blob:synthetic-download');
        static revokeObjectURL = vi.fn();
      }
    );
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('opens only the explicitly chosen available version in the shared PDF renderer and returns focus on close', async () => {
    const item = attachmentItem([attachmentVersion('v1', 1), attachmentVersion('v2', 2, false)]);
    const rendered = await renderWithI18n(
      <LibraryAttachmentBrowser account={getRendererAccountScopeToken()} item={item} />,
      'en-US'
    );
    expect(screen.getByRole('button', { name: 'Preview PDF · paper-2.pdf' })).toBeDisabled();
    expect(screen.queryByTestId('shared-pdf-target')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Choose version' }));
    fireEvent.change(screen.getByRole('combobox', { name: 'Choose version' }), { target: { value: 'v1' } });
    const opener = screen.getByRole('button', { name: 'Preview PDF · paper-1.pdf' });
    fireEvent.click(opener);
    expect(await screen.findByTestId('shared-pdf-target')).toHaveAttribute(
      'data-url',
      '/api/literature/attachment-versions/v1/content'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('shared-pdf-target')).toBeNull();
    expect(opener).toHaveFocus();

    // The selected v1 no longer exists in the next owner-provided metadata.
    rendered.rerender(
      <LibraryAttachmentBrowser
        account={getRendererAccountScopeToken()}
        item={attachmentItem([attachmentVersion('v2', 2)])}
      />
    );
    expect(
      screen.getByText('The selected attachment version is unavailable. No other version was opened.')
    ).toBeInTheDocument();
    expect(screen.queryByTestId('shared-pdf-target')).toBeNull();
  });

  it('retries the same failed content URL and never invents a fallback version', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 404 }))
      .mockResolvedValueOnce(new Response(new Uint8Array([1, 2, 3])));
    vi.stubGlobal('fetch', fetch);
    await renderWithI18n(
      <LibraryAttachmentBrowser account={getRendererAccountScopeToken()} item={attachmentItem()} />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Download · paper-2.pdf' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not download paper-2.pdf.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1));
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      '/api/literature/attachment-versions/v2/content',
      '/api/literature/attachment-versions/v2/content',
    ]);
    expect(await screen.findByText('Download started · paper-2.pdf')).toBeInTheDocument();
  });

  it('retires a failed download when its exact version becomes unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 404 })));
    const rendered = await renderWithI18n(
      <LibraryAttachmentBrowser account={getRendererAccountScopeToken()} item={attachmentItem()} />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Download · paper-2.pdf' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    rendered.rerender(
      <LibraryAttachmentBrowser
        account={getRendererAccountScopeToken()}
        item={attachmentItem([attachmentVersion('v2', 2, false)])}
      />
    );
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Download · paper-2.pdf' })).toBeDisabled();
  });

  it('ends an unresolved download view and prevents its late response from superseding a retry', async () => {
    vi.useFakeTimers();
    const pending = deferredResponse();
    const fetch = vi
      .fn()
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce(new Response(new Uint8Array([1, 2, 3])));
    vi.stubGlobal('fetch', fetch);
    await renderWithI18n(
      <LibraryAttachmentBrowser account={getRendererAccountScopeToken()} item={attachmentItem()} />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Download · paper-2.pdf' }));
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    expect(screen.getByRole('alert')).toHaveTextContent('Could not download paper-2.pdf.');
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    });
    expect(screen.getByText('Download started · paper-2.pdf')).toBeInTheDocument();
    await act(async () => {
      pending.resolve(new Response(new Uint8Array([4, 5, 6])));
      await pending.promise;
    });
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Download started · paper-2.pdf')).toBeInTheDocument();
  });

  it.each(['item', 'account', 'close'])('does not trigger a late download after %s changes', async (boundary) => {
    const pending = deferredResponse();
    const fetch = vi.fn(() => pending.promise); // Deliberately ignores the caller's abort.
    vi.stubGlobal('fetch', fetch);
    const rendered = await renderWithI18n(
      <LibraryAttachmentBrowser account={getRendererAccountScopeToken()} item={attachmentItem()} />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Download · paper-2.pdf' }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    if (boundary === 'close') rendered.unmount();
    else {
      if (boundary === 'account')
        act(() => {
          setRendererAccountOwner('new-owner');
        });
      rendered.rerender(
        <LibraryAttachmentBrowser
          account={getRendererAccountScopeToken()}
          item={boundary === 'item' ? { ...attachmentItem(), id: 'paper-b' } : attachmentItem()}
        />
      );
    }
    await act(async () => {
      pending.resolve(new Response(new Uint8Array([1, 2, 3])));
      await pending.promise;
    });
    expect(HTMLAnchorElement.prototype.click).not.toHaveBeenCalled();
    expect(URL.createObjectURL).not.toHaveBeenCalled();
  });
});
