import { ConfigProvider } from '@arco-design/web-react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React, { useEffect } from 'react';
import { VirtuosoMockContext } from 'react-virtuoso';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SynonBiomedPdfArtifactViewer } from '@/renderer/pages/artifact/SynonBiomedPdfArtifactViewer';
import { renderWithI18n as renderTranslated, type TestLanguage } from '../i18nTestUtils';

const textLayerCallbacks = new Map<number, () => void>();
const parser = vi.hoisted(() => ({ failure: false, pageFailure: false, pages: 2 }));

const VirtualViewport: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <VirtuosoMockContext.Provider value={{ viewportHeight: 900, itemHeight: 600 }}>
    {children}
  </VirtuosoMockContext.Provider>
);
const renderWithI18n = (ui: React.ReactElement, language: TestLanguage) =>
  renderTranslated(ui, language, { wrapper: VirtualViewport });

vi.mock('react-pdf', () => ({
  pdfjs: { GlobalWorkerOptions: { workerSrc: '' } },
  Document: ({
    children,
    onLoadSuccess,
    onLoadError,
    onItemClick,
  }: {
    children: React.ReactNode;
    onLoadSuccess: (value: unknown) => void;
    onLoadError: (error: Error) => void;
    onItemClick?: (destination: { pageIndex: number; pageNumber: number }) => void;
  }) => {
    useEffect(() => {
      if (parser.failure) onLoadError(new Error('Invalid PDF structure'));
      else onLoadSuccess({ numPages: parser.pages });
    }, [onLoadError, onLoadSuccess]);
    return (
      <div>
        <button onClick={() => onItemClick?.({ pageIndex: 399, pageNumber: 400 })}>Go to appendix</button>
        <button onClick={() => onItemClick?.({ pageIndex: -1, pageNumber: 0 })}>Invalid destination</button>
        {children}
      </div>
    );
  },
  Page: ({
    pageNumber,
    onRenderTextLayerSuccess,
    onRenderError,
  }: {
    pageNumber: number;
    onRenderTextLayerSuccess?: () => void;
    onRenderError: (reason: Error) => void;
  }) => {
    useEffect(() => {
      if (parser.pageFailure) onRenderError(new Error('PDF canvas failed'));
    }, [onRenderError]);
    useEffect(() => {
      if (!onRenderTextLayerSuccess) return;
      textLayerCallbacks.set(pageNumber, onRenderTextLayerSuccess);
      return () => {
        if (textLayerCallbacks.get(pageNumber) === onRenderTextLayerSuccess) textLayerCallbacks.delete(pageNumber);
      };
    }, [onRenderTextLayerSuccess, pageNumber]);
    return (
      <div className='react-pdf__Page'>
        <div className='react-pdf__Page__textContent'>
          <span>Page {pageNumber} assay result</span>
        </div>
      </div>
    );
  },
}));

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

describe('X-Science PDF artifact viewer', () => {
  beforeEach(() => {
    parser.failure = false;
    parser.pageFailure = false;
    parser.pages = 2;
    textLayerCallbacks.clear();
    vi.stubGlobal('ResizeObserver', TestResizeObserver);
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(async () => new Response(new Uint8Array([37, 80, 68, 70])))
    );
    Object.defineProperty(Range.prototype, 'getClientRects', {
      configurable: true,
      value: () => [{ left: 220, top: 130, right: 340, bottom: 148, width: 120, height: 18 }],
    });
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('supports read-only workbench use without an annotation callback', async () => {
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/pdf-1' />,
      'en-US'
    );
    const page = await screen.findByRole('region', { name: 'PDF page 1' });
    expect(page).not.toHaveClass('cursor-crosshair');
    fireEvent.click(page);
    expect(await screen.findByText('2 pages')).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith('/api/artifacts/pdf-1', {
      credentials: 'same-origin',
      signal: expect.any(AbortSignal),
    });
  });

  it('shows request failures and retries the same document explicitly', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null, { status: 503 }));
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/pdf-1' />,
      'en-US'
    );
    expect(await screen.findByText('Failed to load PDF preview')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('region', { name: 'PDF page 1' })).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('keeps parser errors visible and allows retry after recovery', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    parser.failure = true;
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/pdf-1' />,
      'en-US'
    );
    expect(await screen.findByText('Failed to load PDF preview')).toBeInTheDocument();
    parser.failure = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('2 pages')).toBeInTheDocument();
  });

  it('rejects empty bytes before invoking the PDF parser', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    vi.mocked(fetch).mockResolvedValueOnce(new Response(null));
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='empty.pdf' contentUrl='/api/artifacts/empty' />,
      'en-US'
    );
    expect(await screen.findByText('Failed to load PDF preview')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'PDF page 1' })).toBeNull();
  });

  it('reports page rendering failures after successful document parsing', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    parser.pageFailure = true;
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/pdf-1' />,
      'en-US'
    );
    expect(await screen.findByText('Failed to load PDF preview')).toBeInTheDocument();
    parser.pageFailure = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('region', { name: 'PDF page 1' })).toBeInTheDocument();
  });

  it('ignores a completed byte read from a cancelled source', async () => {
    let oldStreamController!: ReadableStreamDefaultController<Uint8Array>;
    const oldStream = new ReadableStream<Uint8Array>({
      start(controller) {
        oldStreamController = controller;
      },
    });
    vi.mocked(fetch).mockResolvedValueOnce(new Response(oldStream));
    const view = await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='old.pdf' contentUrl='/api/artifacts/old' />,
      'en-US'
    );
    await waitFor(() => expect(oldStream.locked).toBe(true));
    expect(screen.getByRole('status', { name: 'Loading PDF' })).toBeInTheDocument();
    view.rerender(<SynonBiomedPdfArtifactViewer filename='new.pdf' contentUrl='/api/artifacts/new' />);
    expect(await screen.findByText('2 pages')).toBeInTheDocument();
    await act(async () => {
      oldStreamController.close();
    });
    expect(screen.getByText('2 pages')).toBeInTheDocument();
    expect(screen.queryByText('Failed to load PDF preview')).toBeNull();
    const signal = vi.mocked(fetch).mock.calls[0][1]?.signal;
    expect(signal?.aborted).toBe(true);
  });

  it('keeps large document page canvases bounded by the visible viewport', async () => {
    parser.pages = 500;
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='long.pdf' contentUrl='/api/artifacts/long' />,
      'en-US'
    );
    expect(await screen.findByText('500 pages')).toBeInTheDocument();
    await screen.findByRole('region', { name: 'PDF page 1' });
    expect(document.querySelectorAll('[data-pdf-page]').length).toBeLessThan(5);
    expect(screen.queryByRole('region', { name: 'PDF page 500' })).toBeNull();
  });

  it('navigates internal document links to pages outside the mounted window', async () => {
    parser.pages = 500;
    await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='long.pdf' contentUrl='/api/artifacts/long' />,
      'en-US'
    );
    await screen.findByRole('region', { name: 'PDF page 1' });
    expect(screen.queryByRole('region', { name: 'PDF page 400' })).toBeNull();
    const scroller = document.querySelector<HTMLElement>('[data-virtuoso-scroller]')!;
    Object.defineProperties(scroller, {
      scrollHeight: { configurable: true, value: 300_000 },
      offsetHeight: { configurable: true, value: 900 },
      clientHeight: { configurable: true, value: 900 },
    });
    const scrollTo = vi.fn((options: ScrollToOptions) => {
      scroller.scrollTop = options.top ?? 0;
      fireEvent.scroll(scroller);
    });
    scroller.scrollTo = scrollTo;
    fireEvent.click(screen.getByRole('button', { name: 'Invalid destination' }));
    expect(scrollTo).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Go to appendix' }));
    expect(scrollTo).toHaveBeenCalled();
    expect(await screen.findByRole('region', { name: 'PDF page 400' })).toBeInTheDocument();
    expect(document.querySelectorAll('[data-pdf-page]').length).toBeLessThan(5);
  });

  it('does not reload or reset the document when the selection callback changes', async () => {
    const first = vi.fn();
    const next = vi.fn();
    const view = await renderWithI18n(
      <SynonBiomedPdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/pdf' onSelectionChange={first} />,
      'en-US'
    );
    await screen.findByRole('region', { name: 'PDF page 1' });
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in PDF' }));
    expect(screen.getByText('120%')).toBeInTheDocument();
    view.rerender(
      <SynonBiomedPdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/pdf' onSelectionChange={next} />
    );
    await act(async () => undefined);
    expect(fetch).toHaveBeenCalledOnce();
    expect(next).not.toHaveBeenCalled();
    expect(screen.getByText('120%')).toBeInTheDocument();
  });

  it('renders a controlled paged canvas and stores point coordinates relative to the selected page', async () => {
    const onSelectionChange = vi.fn();
    const onAnnotationClick = vi.fn();
    await renderWithI18n(
      <ConfigProvider>
        <SynonBiomedPdfArtifactViewer
          filename='report.pdf'
          contentUrl='/api/artifacts/pdf-1'
          annotations={[
            {
              id: 'pdf-point',
              artifactId: 'artifact-1',
              targetKey: 'av:version-1',
              label: '①',
              contentChecksum: 'checksum',
              type: 'point',
              text: 'Review figure',
              xPercent: 75,
              yPercent: 25,
              startLine: null,
              startColumn: null,
              endLine: null,
              endColumn: null,
              selectionText: null,
              pageNumber: 2,
              selectionPrefix: null,
              screenshotArtifactId: null,
              elementSelector: null,
              elementDescriptor: null,
              addressedAt: null,
              addressedInFrameId: null,
              createdAt: '2026-07-13T00:00:00.000Z',
            },
            {
              id: 'pdf-text',
              artifactId: 'artifact-1',
              targetKey: 'av:version-1',
              label: '②',
              contentChecksum: 'checksum',
              type: 'text_selection',
              text: 'Check assay wording',
              xPercent: null,
              yPercent: null,
              startLine: 1,
              startColumn: null,
              endLine: 1,
              endColumn: null,
              selectionText: 'assay result',
              pageNumber: 2,
              selectionPrefix: 'Page 2',
              screenshotArtifactId: null,
              elementSelector: null,
              elementDescriptor: null,
              addressedAt: null,
              addressedInFrameId: null,
              createdAt: '2026-07-13T00:00:00.000Z',
            },
          ]}
          onSelectionChange={onSelectionChange}
          onAnnotationClick={onAnnotationClick}
        />
      </ConfigProvider>,
      'en-US'
    );

    const page = await screen.findByRole('region', { name: 'PDF page 2' });
    Object.defineProperty(page, 'getBoundingClientRect', {
      configurable: true,
      value: () => ({ left: 200, top: 100, width: 600, height: 800, right: 800, bottom: 900 }),
    });
    await waitFor(() => expect(textLayerCallbacks.has(2)).toBe(true));
    await act(async () => {
      textLayerCallbacks.get(2)!();
    });
    fireEvent.click(page, { clientX: 350, clientY: 700 });
    await new Promise((resolve) => window.setTimeout(resolve, 40));

    expect(onSelectionChange).toHaveBeenLastCalledWith({
      type: 'point',
      text: 'PDF page 2 · 25.0%, 75.0%',
      x: 350,
      y: 700,
      xPercent: 25,
      yPercent: 75,
      pageNumber: 2,
    });
    expect(screen.getByRole('button', { name: 'View annotation ①' })).toHaveStyle({ left: '75%', top: '25%' });
    const textBadge = await screen.findByRole('button', { name: 'View annotation ②' });
    await waitFor(() =>
      expect(screen.getByTestId('pdf-text-highlight-pdf-text')).toHaveStyle({
        left: '20px',
        top: '30px',
        width: '120px',
        height: '18px',
      })
    );
    fireEvent.click(textBadge);
    expect(onAnnotationClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'pdf-text' }));
    await waitFor(() => expect(screen.getByText('2 pages')).toBeInTheDocument());
  });
});
