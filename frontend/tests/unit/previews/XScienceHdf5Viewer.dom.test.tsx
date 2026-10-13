import XScienceHdf5Viewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceHdf5Viewer';
import type {
  AnnDataOverview,
  Hdf5WorkerRequest,
  Hdf5WorkerResponse,
} from '@/renderer/pages/conversation/Preview/components/viewers/xScienceHdf5Model';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { readFileSync } from 'node:fs';
import path from 'node:path';
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

  it('renders exact integer decimals and boolean metadata from the real HDF5 worker', async () => {
    vi.resetModules();
    const { default: h5wasm } = await import('h5wasm');
    const { FS } = await h5wasm.ready;
    const filename = `/viewer-integers-${crypto.randomUUID()}.h5`;
    const template = readFileSync(
      path.resolve(import.meta.dirname, '../../fixtures/anndata-categorical/categorical-template.h5ad')
    );
    FS.writeFile(filename, template);
    const file = new h5wasm.File(filename, 'a');
    const integers = [
      { name: 'signed_unsafe', dtype: '<q', value: '9007199254740993' },
      { name: 'signed_min', dtype: '<q', value: '-9223372036854775808' },
      { name: 'signed_max', dtype: '<q', value: '9223372036854775807' },
      { name: 'unsigned_max', dtype: '<Q', value: '18446744073709551615' },
      { name: 'timestamp_a', dtype: '<q', value: '1760000000000000000' },
      { name: 'timestamp_b', dtype: '<q', value: '1760000000000000001' },
    ];
    try {
      for (const { name, dtype, value } of integers) {
        const data = dtype === '<q' ? new BigInt64Array([BigInt(value)]) : new BigUint64Array([BigInt(value)]);
        file.create_dataset({ name, data, dtype, shape: [] });
        file.create_dataset({ name: `${name}_vector`, data, dtype, shape: [1] });
        file.create_attribute(name, data, [], dtype);
      }
      file.create_attribute(
        'integer_vector',
        new BigInt64Array(integers.slice(0, 3).map(({ value }) => BigInt(value)))
      );
    } finally {
      file.close();
    }
    const bytes = FS.readFile(filename).slice();
    FS.unlink(filename);

    let dispatch: (event: MessageEvent<Hdf5WorkerRequest>) => void;
    let receive: (message: Hdf5WorkerResponse) => void;
    // Adapt only message transport. The actual worker and locked browser
    // h5wasm parse real bytes and feed the unchanged production presenter.
    vi.stubGlobal('self', {
      location: window.location,
      addEventListener(_type: string, listener: typeof dispatch) {
        dispatch = listener;
      },
      postMessage(message: Hdf5WorkerResponse) {
        receive(structuredClone(message));
      },
    });
    await import('@/renderer/pages/conversation/Preview/components/viewers/XScienceHdf5.worker');
    class RealHdf5Worker {
      listener: ((event: MessageEvent<Hdf5WorkerResponse>) => void) | undefined;
      addEventListener(type: string, listener: EventListener) {
        if (type === 'message') this.listener = listener as typeof this.listener;
      }
      removeEventListener(type: string) {
        if (type === 'message') this.listener = undefined;
      }
      postMessage(message: Hdf5WorkerRequest) {
        receive = (data) => this.listener?.({ data } as MessageEvent<Hdf5WorkerResponse>);
        dispatch({ data: message } as MessageEvent<Hdf5WorkerRequest>);
      }
      terminate() {
        this.listener = undefined;
      }
    }
    vi.stubGlobal('Worker', RealHdf5Worker);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(bytes, { status: 200 })));
    await renderWithI18n(<XScienceHdf5Viewer filename='integers.h5' contentUrl='/synthetic/integers' />, 'en-US');
    await waitFor(() => expect(document.querySelector('[data-hdf5-viewer="ready"]')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('tab', { name: 'Data structure' }));
    const details = screen.getByRole('region', { name: 'HDF5 node details' });
    for (const { name, value } of integers) {
      const row = within(details).getByText(name).parentElement!;
      expect(row).toHaveTextContent(JSON.stringify(value));
    }
    expect(within(details).getByText('integer_vector').parentElement).toHaveTextContent(
      JSON.stringify(integers.slice(0, 3).map(({ value }) => value))
    );

    const select = (name: string) => {
      const item = [...document.querySelectorAll('[role="treeitem"]')].find(
        (node) => node.querySelector('span.truncate')?.textContent === name
      );
      if (!item) throw new Error(`Missing HDF5 tree item: ${name}`);
      fireEvent.click(item.querySelector('button')!);
    };
    for (const { name, value } of integers) {
      select(name);
      await waitFor(() => expect(details.querySelector('pre')?.textContent).toBe(JSON.stringify(value, null, 2)));
      expect(details).not.toHaveTextContent('Showing a bounded preview');
      select(`${name}_vector`);
      await waitFor(() => expect(details.querySelector('pre')?.textContent).toBe(JSON.stringify([value], null, 2)));
    }
    select('obs');
    select('cell_type');
    expect(within(details).getByText('ordered').parentElement).toHaveTextContent('false');
    expect(within(details).getByText('ordered').parentElement).not.toHaveTextContent('0');
  });

  it('renders nonfinite numbers, literal strings and actual null distinctly from the real HDF5 worker', async () => {
    vi.resetModules();
    const { default: h5wasm } = await import('h5wasm');
    const { FS } = await h5wasm.ready;
    const filename = `/viewer-nonfinite-${crypto.randomUUID()}.h5`;
    FS.writeFile(
      filename,
      readFileSync(path.resolve(import.meta.dirname, '../../../node_modules/h5wasm/test/empty.h5'))
    );
    const file = new h5wasm.File(filename, 'a');
    const cases: Array<{ name: string; data: Float32Array | Float64Array; text: string; shape: number[] }> = [
      { name: 'nan64', data: new Float64Array([NaN]), text: 'NaN', shape: [] },
      { name: 'positive_infinity64', data: new Float64Array([Infinity]), text: 'Infinity', shape: [] },
      { name: 'negative_infinity64', data: new Float64Array([-Infinity]), text: '-Infinity', shape: [] },
      { name: 'nan32', data: new Float32Array([NaN]), text: 'NaN', shape: [] },
      { name: 'positive_infinity32', data: new Float32Array([Infinity]), text: 'Infinity', shape: [] },
      { name: 'negative_infinity32', data: new Float32Array([-Infinity]), text: '-Infinity', shape: [] },
      {
        name: 'mixed',
        data: new Float64Array([NaN, Infinity, -Infinity, 0, 1.25]),
        text: '[\n  NaN,\n  Infinity,\n  -Infinity,\n  0,\n  1.25\n]',
        shape: [5],
      },
      { name: 'finite', data: new Float64Array([0, 1.25, -2.5]), text: '[\n  0,\n  1.25,\n  -2.5\n]', shape: [3] },
      { name: 'empty', data: new Float64Array([]), text: '[]', shape: [0] },
    ];
    try {
      for (const { name, data, shape } of cases) {
        file.create_dataset({ name, data, shape });
        file.create_attribute(name, data, shape);
      }
      for (const value of ['NaN', 'Infinity', '-Infinity', 'null']) {
        file.create_dataset({ name: `literal_${value}`, data: [value], dtype: 'S', shape: [] });
        file.create_attribute(`literal_${value}`, value);
      }
      file.create_dataset({ name: 'bounded', data: new Float64Array(401).fill(Infinity) });
    } finally {
      file.close();
    }
    const bytes = FS.readFile(filename).slice();
    FS.unlink(filename);

    let dispatch: (event: MessageEvent<Hdf5WorkerRequest>) => void;
    let receive: (message: Hdf5WorkerResponse) => void;
    vi.stubGlobal('self', {
      location: window.location,
      addEventListener(_type: string, listener: typeof dispatch) {
        dispatch = listener;
      },
      postMessage(message: Hdf5WorkerResponse) {
        receive(structuredClone(message));
      },
    });
    await import('@/renderer/pages/conversation/Preview/components/viewers/XScienceHdf5.worker');
    class RealHdf5Worker {
      listener: ((event: MessageEvent<Hdf5WorkerResponse>) => void) | undefined;
      addEventListener(type: string, listener: EventListener) {
        if (type === 'message') this.listener = listener as typeof this.listener;
      }
      removeEventListener(type: string) {
        if (type === 'message') this.listener = undefined;
      }
      postMessage(message: Hdf5WorkerRequest) {
        receive = (data) => this.listener?.({ data } as MessageEvent<Hdf5WorkerResponse>);
        dispatch({ data: message } as MessageEvent<Hdf5WorkerRequest>);
      }
      terminate() {
        this.listener = undefined;
      }
    }
    vi.stubGlobal('Worker', RealHdf5Worker);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(bytes, { status: 200 })));
    await renderWithI18n(<XScienceHdf5Viewer filename='nonfinite.h5' contentUrl='/synthetic/nonfinite' />, 'en-US');
    await waitFor(() => expect(document.querySelector('[data-hdf5-viewer="ready"]')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('tab', { name: 'Data structure' }));
    const details = screen.getByRole('region', { name: 'HDF5 node details' });
    const attribute = (name: string) =>
      within(details).getByText(name).parentElement!.querySelector('code')!.textContent;
    for (const { name, text } of cases) expect.soft(attribute(name)).toBe(text.replace(/\n\s*/g, ''));
    expect(attribute('empty_attr')).toBe('null');
    for (const value of ['NaN', 'Infinity', '-Infinity', 'null'])
      expect(attribute(`literal_${value}`)).toBe(JSON.stringify(value));

    const select = (name: string) => {
      const item = [...document.querySelectorAll('[role="treeitem"]')].find(
        (node) => node.querySelector('span.truncate')?.textContent === name
      );
      if (!item) throw new Error(`Missing HDF5 tree item: ${name}`);
      fireEvent.click(item.querySelector('button')!);
    };
    for (const { name, text } of cases) {
      select(name);
      await waitFor(() => expect(details.querySelector('pre')?.textContent).toBe(text));
      expect(details).not.toHaveTextContent('Only the first 400 values are shown in this bounded slice.');
    }
    for (const value of ['NaN', 'Infinity', '-Infinity', 'null']) {
      select(`literal_${value}`);
      await waitFor(() => expect(details.querySelector('pre')?.textContent).toBe(JSON.stringify(value)));
    }
    select('empty_dataset');
    await waitFor(() => expect(details.querySelector('pre')?.textContent).toBe('null'));
    select('bounded');
    await waitFor(() =>
      expect(details).toHaveTextContent('Only the first 400 values are shown in this bounded slice.')
    );
    expect(details.querySelector('pre')?.textContent).toBe(`[\n${Array(400).fill('  Infinity').join(',\n')}\n]`);
    // Repeat navigation to catch a stale bounded detail or truncation label.
    select('nan64');
    await waitFor(() => expect(details.querySelector('pre')?.textContent).toBe('NaN'));
    expect(details).not.toHaveTextContent('Only the first 400 values are shown in this bounded slice.');
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
