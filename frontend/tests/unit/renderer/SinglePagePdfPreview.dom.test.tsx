import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React, { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { XSciencePdfArtifactViewer } from '@/renderer/pages/artifact/XSciencePdfArtifactViewer';
import type { XScienceArtifactAnnotation } from '@/renderer/services/xScienceAnnotations';
import { renderWithI18n } from '../i18nTestUtils';

const parser = vi.hoisted(() => ({
  pages: 1,
  defer: false,
  loads: [] as { success: (document: { numPages: number }) => void; failure: (error: Error) => void }[],
}));

vi.mock('react-pdf', () => ({
  pdfjs: { version: '5.4.296', GlobalWorkerOptions: { workerSrc: '' } },
  Document: ({
    children,
    file,
    onLoadSuccess,
    onLoadError,
  }: {
    children: React.ReactNode;
    file: unknown;
    onLoadSuccess: (document: { numPages: number }) => void;
    onLoadError: (error: Error) => void;
  }) => {
    useEffect(() => {
      parser.loads.push({ success: onLoadSuccess, failure: onLoadError });
      if (!parser.defer) onLoadSuccess({ numPages: parser.pages });
      // Deliberately retain callbacks to verify late outcomes after disposal.
    }, [file]);
    return <div data-testid='pdf-document-owner'>{children}</div>;
  },
  Page: ({
    pageNumber,
    onLoadSuccess,
    onRenderSuccess,
    width,
    devicePixelRatio,
    renderTextLayer,
  }: {
    pageNumber: number;
    width?: number;
    devicePixelRatio?: number;
    renderTextLayer?: boolean;
    onLoadSuccess?: (page: { getViewport: (options: { scale: number }) => { width: number; height: number } }) => void;
    onRenderSuccess?: () => void;
  }) => {
    useEffect(() => {
      onLoadSuccess?.({ getViewport: ({ scale }) => ({ width: 600 * scale, height: 800 * scale }) });
      onRenderSuccess?.();
    }, [onLoadSuccess, onRenderSuccess]);
    return (
      <div
        data-testid='parsed-pdf-page'
        data-number={pageNumber}
        data-render-width={width}
        data-render-density={devicePixelRatio}
      >
        Original PDF content
        {renderTextLayer && (
          <div className='react-pdf__Page__textContent'>
            <span data-testid='pdf-text'>Original PDF text</span>
          </div>
        )}
      </div>
    );
  },
}));

class ControlledResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

beforeEach(() => {
  parser.pages = 1;
  parser.defer = false;
  parser.loads = [];
  vi.stubGlobal('ResizeObserver', ControlledResizeObserver);
  vi.spyOn(window, 'devicePixelRatio', 'get').mockReturnValue(1);
  vi.stubGlobal(
    'PointerEvent',
    class extends MouseEvent {
      pointerId: number;
      constructor(type: string, init: PointerEventInit = {}) {
        super(type, { ...init, buttons: init.buttons ?? (type === 'pointermove' || type === 'pointerdown' ? 1 : 0) });
        this.pointerId = init.pointerId ?? 1;
      }
    }
  );
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(new Uint8Array([37, 80, 68, 70])))
  );
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(640);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(480);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('single-page PDF presentation', () => {
  it('bounds the actual native raster scale rather than only device pixel ratio', async () => {
    await renderWithI18n(<XSciencePdfArtifactViewer filename='plot.pdf' contentUrl='/api/artifacts/raster' />, 'en-US');
    const page = await screen.findByTestId('parsed-pdf-page');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Zoom in' })).toBeEnabled());
    const nativeWidth = () => Number(page.dataset.renderWidth) * Number(page.dataset.renderDensity);
    expect(nativeWidth() / 600).toBeGreaterThanOrEqual(1);
    vi.spyOn(window, 'devicePixelRatio', 'get').mockReturnValue(4);
    for (let index = 0; index < 12; index += 1) fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(nativeWidth() / 600).toBeLessThanOrEqual(5);
    expect(nativeWidth()).toBeLessThanOrEqual(8192);
    expect((nativeWidth() * 800) / 600).toBeLessThanOrEqual(8192);
    expect((nativeWidth() ** 2 * 800) / 600).toBeLessThanOrEqual(16 * 1024 * 1024);
  });

  it('pans the retained annotation text layer only in Pan mode and preserves annotation controls', async () => {
    const annotation: XScienceArtifactAnnotation = {
      id: 'text-1',
      artifactId: 'a',
      targetKey: 'av:1',
      label: '1',
      contentChecksum: 'checksum',
      type: 'text_selection' as const,
      text: 'Existing note',
      xPercent: null,
      yPercent: null,
      startLine: 1,
      startColumn: null,
      endLine: 1,
      endColumn: null,
      selectionText: null,
      pageNumber: 1,
      selectionPrefix: null,
      screenshotArtifactId: null,
      elementSelector: null,
      elementDescriptor: null,
      addressedAt: null,
      addressedInFrameId: null,
      createdAt: '2026-10-23T00:00:00.000Z',
    };
    const point = { ...annotation, id: 'point-1', label: '2', type: 'point' as const, xPercent: 50, yPercent: 50 };
    const openAnnotation = vi.fn();
    await renderWithI18n(
      <XSciencePdfArtifactViewer
        filename='plot.pdf'
        contentUrl='/api/artifacts/pan-text'
        annotations={[annotation, point]}
        onAnnotationClick={openAnnotation}
        onSelectionChange={vi.fn()}
      />,
      'en-US'
    );
    const text = await screen.findByTestId('pdf-text');
    const viewport = screen.getByRole('region', { name: 'Single-page PDF preview: plot.pdf' });
    const capture = vi.fn();
    Object.defineProperty(viewport, 'setPointerCapture', { configurable: true, value: capture });
    fireEvent.pointerDown(text, { pointerId: 7, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(viewport, { pointerId: 7, clientX: 70, clientY: 70 });
    expect(capture).toHaveBeenCalledWith(7);
    fireEvent.pointerUp(viewport, { pointerId: 7 });
    const annotationButton = screen.getByRole('button', { name: 'View annotation 2' });
    capture.mockClear();
    fireEvent.pointerDown(annotationButton, { pointerId: 9, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(viewport, { pointerId: 9, clientX: 70, clientY: 70 });
    expect(capture).not.toHaveBeenCalled();
    fireEvent.click(annotationButton);
    expect(openAnnotation).toHaveBeenCalledWith(point);
    expect(screen.getByRole('region', { name: 'PDF page 1' })).toHaveAttribute('data-canvas-pan-surface');
    fireEvent.click(screen.getByRole('button', { name: 'Select PDF text or position' }));
    capture.mockClear();
    expect(screen.getByTestId('pdf-text')).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'PDF page 1' })).not.toHaveAttribute('data-canvas-pan-surface');
    fireEvent.pointerDown(text, { pointerId: 8, button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(viewport, { pointerId: 8, clientX: 70, clientY: 70 });
    expect(capture).not.toHaveBeenCalled();
  });

  it.each([
    '/api/artifacts/a/versions/v/content',
    '/api/literature/items/i/attachments/a/versions/v/content',
    '/api/files/content?path=plot.pdf',
    'data:application/pdf;base64,JVBERi0xLjQ=',
    'blob:http://localhost/synthetic-pdf',
  ])('classifies the loaded single page from %s on the same PDF owner', async (contentUrl) => {
    const report = vi.fn();
    await renderWithI18n(
      <XSciencePdfArtifactViewer filename='plot.pdf' contentUrl={contentUrl} onReadingPositionChange={report} />,
      'en-US'
    );
    await waitFor(() => expect(document.querySelector('[data-single-page-pdf-preview]')).not.toBeNull());
    expect(screen.getAllByTestId('pdf-document-owner')).toHaveLength(1);
    expect(screen.getByTestId('parsed-pdf-page')).toHaveAttribute('data-number', '1');
    expect(screen.getByRole('button', { name: 'Zoom in' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Zoom out' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset zoom' })).toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: 'Current PDF page' })).toBeNull();
    expect(document.querySelector('[data-virtuoso-scroller]')).toBeNull();
    expect(report).toHaveBeenCalledWith({ pageNumber: 1, pageCount: 1 });
    expect(fetch).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    fireEvent.click(screen.getByRole('button', { name: 'Reset zoom' }));
    expect(fetch).toHaveBeenCalledOnce();
  });

  it('does not mount either reading controls or image controls before the actual page count arrives', async () => {
    parser.defer = true;
    await renderWithI18n(
      <XSciencePdfArtifactViewer filename='unknown.pdf' contentUrl='/api/artifacts/unknown' />,
      'en-US'
    );
    await waitFor(() => expect(parser.loads).toHaveLength(1));
    expect(screen.queryByRole('button', { name: 'Zoom in' })).toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Current PDF page' })).toBeNull();
    expect(document.querySelector('[data-single-page-pdf-preview]')).toBeNull();
    await act(async () => parser.loads[0].success({ numPages: 1 }));
    expect(document.querySelector('[data-single-page-pdf-preview]')).not.toBeNull();
  });

  it('keeps the current source when a disposed parser publishes a late error or page count', async () => {
    const view = await renderWithI18n(
      <XSciencePdfArtifactViewer filename='A.pdf' contentUrl='/api/artifacts/A' />,
      'en-US'
    );
    await waitFor(() => expect(parser.loads).toHaveLength(1));
    const old = parser.loads[0];
    view.rerender(<XSciencePdfArtifactViewer filename='B.pdf' contentUrl='/api/artifacts/B' />);
    await waitFor(() => expect(parser.loads).toHaveLength(2));
    await act(async () => {
      old.failure(new Error('late disposed source failure'));
      old.success({ numPages: 500 });
    });
    expect(screen.queryByText('Failed to load PDF preview')).toBeNull();
    expect(document.querySelector('[data-single-page-pdf-preview]')).not.toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Current PDF page' })).toBeNull();
    expect(screen.getByTestId('parsed-pdf-page')).toHaveAttribute('data-number', '1');
  });

  it('preserves the annotation extension and switches its controls with the active language', async () => {
    const selection = vi.fn();
    const view = await renderWithI18n(
      <XSciencePdfArtifactViewer
        filename='plot.pdf'
        contentUrl='/api/artifacts/annotated'
        onSelectionChange={selection}
      />,
      'en-US'
    );
    const select = await screen.findByRole('button', { name: 'Select PDF text or position' });
    fireEvent.click(select);
    expect(select).toHaveAttribute('aria-pressed', 'true');
    const page = screen.getByRole('region', { name: 'PDF page 1' });
    vi.spyOn(page, 'getBoundingClientRect').mockReturnValue({
      left: 100,
      top: 200,
      width: 300,
      height: 400,
    } as DOMRect);
    fireEvent.click(page, { clientX: 250, clientY: 400 });
    expect(selection).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'point', pageNumber: 1, xPercent: 50, yPercent: 50 })
    );
    await act(async () => view.i18n.changeLanguage('zh-CN'));
    expect(screen.getByRole('button', { name: '选择 PDF 文字或位置' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '平移 PDF' })).toBeInTheDocument();
  });

  it('changes to the existing multi-page reader for a different loaded source', async () => {
    const view = await renderWithI18n(
      <XSciencePdfArtifactViewer filename='plot.pdf' contentUrl='/api/artifacts/one' />,
      'en-US'
    );
    await waitFor(() => expect(document.querySelector('[data-single-page-pdf-preview]')).not.toBeNull());
    parser.pages = 9;
    view.rerender(<XSciencePdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/nine' />);
    const page = await screen.findByRole('textbox', { name: 'Current PDF page' });
    expect(page).toHaveValue('1');
    expect(document.querySelector('[data-single-page-pdf-preview]')).toBeNull();
    fireEvent.change(page, { target: { value: '4' } });
    fireEvent.keyDown(page, { key: 'Enter' });
    expect(page).toHaveValue('4');
  });

  it('retains multi-page zoom and established page through a same-source parser retry', async () => {
    parser.pages = 9;
    await renderWithI18n(
      <XSciencePdfArtifactViewer filename='report.pdf' contentUrl='/api/artifacts/retry' />,
      'en-US'
    );
    const page = await screen.findByRole('textbox', { name: 'Current PDF page' });
    fireEvent.change(page, { target: { value: '4' } });
    fireEvent.keyDown(page, { key: 'Enter' });
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in PDF' }));
    expect(screen.getByText('120%')).toBeInTheDocument();
    const old = parser.loads[0];
    await act(async () => old.failure(new Error('controlled parser error')));
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Current PDF page' })).toHaveValue('4'));
    expect(screen.getByText('120%')).toBeInTheDocument();
    await act(async () => old.failure(new Error('late previous retry failure')));
    expect(screen.queryByText('Failed to load PDF preview')).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
