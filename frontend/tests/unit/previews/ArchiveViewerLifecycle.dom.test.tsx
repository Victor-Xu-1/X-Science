import ArchiveViewer from '@/renderer/pages/conversation/Preview/components/viewers/ArchiveViewer';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createTestI18n } from '../i18nTestUtils';

const actions = vi.hoisted(() => ({ openPreview: vi.fn() }));
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => actions,
}));
const listing = {
  filename: 'a.zip',
  containers: [],
  entries: [
    { path: 'docs', name: 'docs', size: 0, directory: true, archive: false },
    { path: 'docs/notes.md', name: 'notes.md', size: 42, directory: false, archive: false },
  ],
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function mount(url: string | undefined = '/api/artifacts/versions/archive-a') {
  const i18n = await createTestI18n('en-US');
  const node = (source: string | undefined, filename = 'a.zip') => (
    <I18nextProvider i18n={i18n}>
      <ArchiveViewer filename={filename} contentUrl={source} />
    </I18nextProvider>
  );
  const result = render(node(url));
  return { ...result, change: (source: string | undefined, name?: string) => result.rerender(node(source, name)) };
}
async function openDocs() {
  await waitFor(() => expect(screen.getByRole('button', { name: /^docs$/ })).toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: /^docs$/ }));
  return screen.getByRole('button', { name: /notes\.md/ });
}
beforeEach(() => {
  actions.openPreview.mockReset();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => Response.json(listing))
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('archive viewer read ownership and recovery', () => {
  it('starts a different source at its root instead of filtering by the previous directory', async () => {
    const view = await mount();
    await openDocs();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        Response.json({
          filename: 'b.zip',
          containers: [],
          entries: [{ path: 'root.md', name: 'root.md', size: 10, directory: false, archive: false }],
        })
      )
    );
    view.change('/api/artifacts/versions/archive-b', 'b.zip');
    await waitFor(() => expect(screen.getByRole('button', { name: /root\.md/ })).toBeInTheDocument());
    expect(screen.queryByText('This folder is empty')).not.toBeInTheDocument();
  });

  it('does not keep a source-less replacement waiting on a disposed listing request', async () => {
    const read = deferred<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => read.promise)
    );
    const view = await mount();
    view.change(undefined, 'remote.zip');
    expect(screen.getByText('Safe archive browsing is not available for this source.')).toBeInTheDocument();
    await act(async () => read.resolve(Response.json(listing)));
    expect(screen.queryByRole('button', { name: /^docs$/ })).not.toBeInTheDocument();
  });

  it('does not launch duplicate listing reads while the current one is pending', async () => {
    const read = deferred<Response>();
    const request = vi.fn(() => read.promise);
    vi.stubGlobal('fetch', request);
    await mount();
    const refresh = screen.getByRole('button', { name: 'Reload archive' });
    expect(refresh).toBeDisabled();
    fireEvent.click(refresh);
    expect(request).toHaveBeenCalledTimes(1);
    await act(async () => read.resolve(Response.json(listing)));
    expect(refresh).toBeEnabled();
  });

  it('keeps the directory and retries only the entry after an HTTP read failure', async () => {
    const request = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes('/archive/content') ? new Response('failed', { status: 503 }) : Response.json(listing)
    );
    vi.stubGlobal('fetch', request);
    await mount();
    fireEvent.click(await openDocs());
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('notes.md'));
    expect(screen.getByRole('button', { name: /notes\.md.*42 B/ })).toBeEnabled();
    request.mockImplementation(async (input) =>
      String(input).includes('/archive/content') ? new Response('# Recovered', { status: 200 }) : Response.json(listing)
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry opening notes.md' }));
    await waitFor(() => expect(actions.openPreview).toHaveBeenCalledTimes(1));
    expect(actions.openPreview).toHaveBeenCalledWith('# Recovered', 'markdown', expect.any(Object), {
      presentation: 'board',
    });
    expect(request.mock.calls.filter(([url]) => !String(url).includes('/archive/content'))).toHaveLength(1);
  });

  it('reports a transport failure without an unhandled rejection or hiding healthy entries', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).includes('/archive/content')) throw new TypeError('offline');
        return Response.json(listing);
      })
    );
    await mount();
    fireEvent.click(await openDocs());
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('notes.md'));
    expect(screen.getByRole('button', { name: /notes\.md.*42 B/ })).toBeInTheDocument();
  });

  it('never opens a late entry after the viewer was unmounted', async () => {
    const content = deferred<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) =>
        String(input).includes('/archive/content') ? content.promise : Promise.resolve(Response.json(listing))
      )
    );
    const view = await mount();
    fireEvent.click(await openDocs());
    view.unmount();
    await act(async () => content.resolve(new Response('# Late', { status: 200 })));
    expect(actions.openPreview).not.toHaveBeenCalled();
  });

  it('never opens a late entry from a directory the user has left', async () => {
    const content = deferred<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) =>
        String(input).includes('/archive/content') ? content.promise : Promise.resolve(Response.json(listing))
      )
    );
    await mount();
    fireEvent.click(await openDocs());
    fireEvent.click(screen.getByRole('button', { name: 'Go up' }));
    await act(async () => content.resolve(new Response('# Late', { status: 200 })));
    expect(actions.openPreview).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: /^docs$/ })).toBeInTheDocument();
  });

  it('coalesces repeated entry activation and exposes its pending state', async () => {
    const content = deferred<Response>();
    const request = vi.fn((input: RequestInfo | URL) =>
      String(input).includes('/archive/content') ? content.promise : Promise.resolve(Response.json(listing))
    );
    vi.stubGlobal('fetch', request);
    await mount();
    const entry = await openDocs();
    fireEvent.click(entry);
    fireEvent.click(entry);
    expect(entry).toBeDisabled();
    expect(request.mock.calls.filter(([url]) => String(url).includes('/archive/content'))).toHaveLength(1);
    await act(async () => content.resolve(new Response('# Once', { status: 200 })));
    expect(actions.openPreview).toHaveBeenCalledTimes(1);
  });

  it('retains folder navigation on rename and returns reading focus when its opener disappears', async () => {
    const request = vi.fn(async () => Response.json(listing));
    vi.stubGlobal('fetch', request);
    const view = await mount();
    const folder = await screen.findByRole('button', { name: /^docs$/ });
    folder.focus();
    fireEvent.click(folder);
    expect(screen.getByRole('heading', { name: 'a.zip' })).toHaveFocus();
    view.change('/api/artifacts/versions/archive-a', 'renamed.zip');
    expect(screen.getByRole('heading', { name: 'renamed.zip' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /notes\.md/ })).toBeInTheDocument();
    expect(request).toHaveBeenCalledTimes(1);
  });

  it('allows two different entry reads to complete while coalescing each individual entry', async () => {
    const first = deferred<Response>();
    const second = deferred<Response>();
    const extraListing = {
      ...listing,
      entries: [
        ...listing.entries,
        { path: 'docs/other.md', name: 'other.md', size: 42, directory: false, archive: false },
      ],
    };
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        const url = new URL(String(input));
        if (!url.pathname.endsWith('/content')) return Promise.resolve(Response.json(extraListing));
        return url.searchParams.get('entry') === 'docs/notes.md' ? first.promise : second.promise;
      })
    );
    await mount();
    fireEvent.click(await openDocs());
    fireEvent.click(screen.getByRole('button', { name: /other\.md/ }));
    await act(async () => second.resolve(new Response('# Second', { status: 200 })));
    await act(async () => first.resolve(new Response('# First', { status: 200 })));
    expect(actions.openPreview).toHaveBeenCalledTimes(2);
    expect(actions.openPreview.mock.calls.map(([content]) => content)).toEqual(['# Second', '# First']);
  });

  it('keeps retry focus on a connected reading target rather than the disappearing retry control', async () => {
    const recovered = deferred<Response>();
    const request = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes('/archive/content') ? new Response('failed', { status: 503 }) : Response.json(listing)
    );
    vi.stubGlobal('fetch', request);
    await mount();
    fireEvent.click(await openDocs());
    const retry = await screen.findByRole('button', { name: 'Retry opening notes.md' });
    request.mockImplementation((input) =>
      String(input).includes('/archive/content') ? recovered.promise : Promise.resolve(Response.json(listing))
    );
    retry.focus();
    fireEvent.click(retry);
    expect(screen.getByRole('heading', { name: 'a.zip' })).toHaveFocus();
    expect(screen.queryByRole('button', { name: 'Retry opening notes.md' })).not.toBeInTheDocument();
    await act(async () => recovered.resolve(new Response('# Ready')));
    expect(actions.openPreview).toHaveBeenCalledTimes(1);
  });

  it('aborts a nested archive read on return and ignores its late listing', async () => {
    const child = deferred<Response>();
    const request = vi.fn((input: RequestInfo | URL) => {
      const url = new URL(String(input));
      return url.searchParams.has('container')
        ? child.promise
        : Promise.resolve(
            Response.json({
              ...listing,
              entries: [
                ...listing.entries,
                { path: 'inner.zip', name: 'inner.zip', size: 42, directory: false, archive: true },
              ],
            })
          );
    });
    vi.stubGlobal('fetch', request);
    await mount();
    fireEvent.click(await screen.findByRole('button', { name: /inner\.zip.*42 B/ }));
    expect(screen.getByRole('status')).toHaveTextContent('Reading archive contents');
    fireEvent.click(screen.getByRole('button', { name: 'Go up' }));
    await screen.findByRole('button', { name: /^docs$/ });
    await act(async () =>
      child.resolve(Response.json({ filename: 'inner.zip', containers: ['inner.zip'], entries: [] }))
    );
    expect(screen.getByRole('button', { name: /^docs$/ })).toBeInTheDocument();
    expect(screen.queryByText('This folder is empty')).not.toBeInTheDocument();
  });

  it('does not request an external content host or offer a meaningless reload', async () => {
    const request = vi.fn();
    vi.stubGlobal('fetch', request);
    await mount('https://external.example/archive.zip');
    expect(screen.getByRole('alert')).toHaveTextContent('Safe archive browsing is not available');
    expect(screen.getByRole('button', { name: 'Reload archive' })).toBeDisabled();
    expect(request).not.toHaveBeenCalled();
  });
});
