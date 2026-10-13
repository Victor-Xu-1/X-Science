import XScienceHdf5Viewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceHdf5Viewer';
import type {
  AnnDataOverview,
  Hdf5WorkerRequest,
  Hdf5WorkerResponse,
} from '@/renderer/pages/conversation/Preview/components/viewers/xScienceHdf5Model';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

class MockHdf5Worker {
  static withEmbedding = false;
  static overview: AnnDataOverview | undefined;
  messageListener: ((event: MessageEvent<Hdf5WorkerResponse>) => void) | null = null;
  messages: Hdf5WorkerRequest[] = [];

  postMessage(message: Hdf5WorkerRequest) {
    this.messages.push(message);
    if (message.type !== 'open') return;
    queueMicrotask(() =>
      this.messageListener?.({
        data: {
          type: 'ready',
          nodeCount: 2,
          truncated: false,
          overview: MockHdf5Worker.overview || {
            cells: 10,
            features: 3,
            embedding: MockHdf5Worker.withEmbedding
              ? {
                  key: 'derived_pca',
                  label: 'PCA',
                  labelKey: 'pcaPreview',
                  source: 'derived',
                  totalPoints: 10,
                  sampledPoints: 3,
                  points: [
                    [0, 0],
                    [1, 1],
                    [-1, 1],
                  ],
                }
              : undefined,
            distributions: [
              {
                key: 'cell_type',
                label: 'cell_type',
                labelKey: 'cellType',
                total: 10,
                items: [
                  { label: 'T cell', count: 6 },
                  { label: 'B cell', count: 4 },
                ],
              },
            ],
          },
          root: {
            name: message.filename,
            path: '/',
            kind: 'group',
            attributes: [{ name: 'encoding-type', value: '"anndata"' }],
            children: [{ name: 'X', path: '/X', kind: 'dataset', shape: [10, 3], dtype: '<f4', attributes: [] }],
          },
        },
      } as MessageEvent<Hdf5WorkerResponse>)
    );
  }

  addEventListener(type: string, listener: EventListener) {
    if (type === 'message') this.messageListener = listener as (event: MessageEvent<Hdf5WorkerResponse>) => void;
  }

  removeEventListener(type: string) {
    if (type === 'message') this.messageListener = null;
  }

  terminate() {}
}

describe('XScienceHdf5Viewer', () => {
  afterEach(() => {
    cleanup();
    MockHdf5Worker.withEmbedding = false;
    MockHdf5Worker.overview = undefined;
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('renders the AnnData overview before the structural hierarchy', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(new Uint8Array([137, 72, 68, 70, 13, 10, 26, 10]), {
        status: 200,
        headers: { 'content-length': '8' },
      })
    );
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('Worker', MockHdf5Worker);

    await renderWithI18n(
      <XScienceHdf5Viewer filename='simulated_raw.h5ad' contentUrl='/api/artifacts/artifact-1' />,
      'en-US'
    );

    expect(screen.getByRole('status')).toHaveTextContent('Reading simulated_raw.h5ad');
    await waitFor(() => expect(document.querySelector('[data-hdf5-viewer="ready"]')).toBeInTheDocument());
    expect(screen.getByRole('region', { name: 'AnnData visualization overview' })).toBeInTheDocument();
    expect(screen.getByText('10 × 3')).toBeInTheDocument();
    expect(screen.getByText('No UMAP, t-SNE, or PCA coordinates were found')).toBeInTheDocument();
    expect(screen.getByText('Cell type')).toBeInTheDocument();
    expect(screen.getByText('T cell')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'Data structure' }));
    expect(screen.getByRole('treeitem', { name: /simulated_raw.h5ad/ })).toBeInTheDocument();
    expect(screen.getByText('encoding-type')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/artifacts/artifact-1',
      expect.objectContaining({ credentials: 'same-origin', signal: expect.any(AbortSignal) })
    );
  });

  it.each([
    { missing: 1, knownPercent: '75%', missingPercent: '25%' },
    { missing: 3, knownPercent: '25%', missingPercent: '75%' },
    { missing: 4, knownPercent: undefined, missingPercent: '100%' },
  ])(
    'shows all four cells including $missing Unlabeled annotations',
    async ({ missing, knownPercent, missingPercent }) => {
      // Presenter contract: real-parser missing-code cases are covered separately
      // by XScienceHdf5.worker.test.ts, rather than by this transport stub.
      MockHdf5Worker.overview = {
        cells: 4,
        features: 2,
        distributions: [
          {
            key: 'cell_type',
            label: 'cell_type',
            labelKey: 'cellType',
            total: 4,
            items: [
              ...(missing < 4 ? [{ label: 'T cell', count: 4 - missing }] : []),
              { label: '', labelKey: 'unlabeled', count: missing },
            ],
          },
        ],
      };
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new Uint8Array([1, 2, 3]), { status: 200 })));
      vi.stubGlobal('Worker', MockHdf5Worker);

      await renderWithI18n(<XScienceHdf5Viewer filename='missing.h5ad' contentUrl='/api/artifacts/missing' />, 'en-US');

      const composition = await screen.findByRole('region', { name: 'Sample composition' });
      expect(screen.getByText('4 × 2')).toBeInTheDocument();
      expect(within(composition).getByText('4 cells')).toBeInTheDocument();
      const missingRow = within(composition).getByText('Unlabeled').parentElement!;
      expect(within(missingRow).getByText(missingPercent)).toBeInTheDocument();
      if (knownPercent) {
        const knownRow = within(composition).getByText('T cell').parentElement!;
        expect(within(knownRow).getByText(knownPercent)).toBeInTheDocument();
        expect(within(knownRow).queryByText('100%')).not.toBeInTheDocument();
      } else {
        expect(within(composition).queryByText('T cell')).not.toBeInTheDocument();
      }
    }
  );

  it('renders a derived PCA projection for raw AnnData without stored coordinates', async () => {
    MockHdf5Worker.withEmbedding = true;
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new Uint8Array([1, 2, 3]), { status: 200 })));
    vi.stubGlobal('Worker', MockHdf5Worker);
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      }
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      clearRect: vi.fn(),
      fillRect: vi.fn(),
      fillStyle: '',
      globalAlpha: 1,
    } as unknown as CanvasRenderingContext2D);

    await renderWithI18n(<XScienceHdf5Viewer filename='raw.h5ad' contentUrl='/api/artifacts/raw' />, 'en-US');

    await screen.findByText('PCA preview projection');
    expect(
      screen.getByText('Showing 3 / 10 cells · Computed from a sampled expression matrix for quick preview only')
    ).toBeInTheDocument();
    expect(screen.queryByText('No UMAP, t-SNE, or PCA coordinates were found')).not.toBeInTheDocument();
    expect(document.querySelector('canvas')).toBeInTheDocument();
  });

  it('shows a recoverable error and retries the real request', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('unavailable', { status: 502 }))
      .mockResolvedValueOnce(new Response(new Uint8Array([1, 2, 3]), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    vi.stubGlobal('Worker', MockHdf5Worker);

    await renderWithI18n(<XScienceHdf5Viewer filename='matrix.h5' contentUrl='/api/artifacts/artifact-2' />, 'en-US');
    await screen.findByText('Failed to load HDF5 file (HTTP 502).');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })));
    await waitFor(() => expect(document.querySelector('[data-hdf5-viewer="ready"]')).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('refuses files larger than the bounded browser-memory contract', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(new Uint8Array([1]), {
          status: 200,
          headers: { 'content-length': String(513 * 1024 * 1024) },
        })
      )
    );
    vi.stubGlobal('Worker', MockHdf5Worker);

    await renderWithI18n(
      <XScienceHdf5Viewer filename='oversized.h5ad' contentUrl='/api/artifacts/oversized' />,
      'en-US'
    );
    await screen.findByText('HDF5 file exceeds the 512 MB preview limit.');
    expect(screen.getByRole('link', { name: 'Download original file' })).toHaveAttribute(
      'href',
      '/api/artifacts/oversized'
    );
  });
});
