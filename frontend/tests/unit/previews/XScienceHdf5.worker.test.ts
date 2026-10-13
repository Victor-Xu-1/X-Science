import type {
  AnnDataDistribution,
  AnnDataOverview,
  Hdf5WorkerRequest,
  Hdf5WorkerResponse,
} from '@/renderer/pages/conversation/Preview/components/viewers/xScienceHdf5Model';
import h5wasm, { type Dataset, type Group } from 'h5wasm';
import { readFileSync } from 'node:fs';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';

type Codes = Int8Array | Int16Array | Int32Array | BigInt64Array | Float64Array;
const template = readFileSync(new URL('../../fixtures/anndata-categorical/categorical-template.h5ad', import.meta.url));
let messageListener: (event: MessageEvent<Hdf5WorkerRequest>) => void;
let receive: ((message: Hdf5WorkerResponse) => void) | undefined;
let nextRequestId = 0;

beforeAll(async () => {
  // Only the worker message transport is adapted to Node. The production
  // worker and its locked browser h5wasm import read every real HDF5 fixture.
  vi.stubGlobal('self', {
    addEventListener(type: string, listener: typeof messageListener) {
      expect(type).toBe('message');
      messageListener = listener;
    },
    postMessage(message: Hdf5WorkerResponse) {
      receive?.(message);
    },
  });
  await h5wasm.ready;
  await import('@/renderer/pages/conversation/Preview/components/viewers/XScienceHdf5.worker');
});

afterAll(() => vi.unstubAllGlobals());

function encodeArray(dataset: Dataset, type = 'array') {
  dataset.create_attribute('encoding-type', type);
  dataset.create_attribute('encoding-version', '0.2.0');
}

function strings(group: Group, name: string, data: string[]) {
  const dataset = group.create_dataset({ name, data, dtype: 'S', shape: [data.length] });
  encodeArray(dataset, 'string-array');
}

async function fixtureBuffer(codes: Codes, categories = ['T cell', 'B cell'], labels?: string[]) {
  const { FS } = await h5wasm.ready;
  const filename = `/categorical-fixture-${crypto.randomUUID()}.h5ad`;
  FS.writeFile(filename, template);
  const file = new h5wasm.File(filename, 'a');
  try {
    const cells = codes.length;
    const matrix = file.create_dataset({ name: 'X', data: new Float64Array(cells * 2), shape: [cells, 2] });
    encodeArray(matrix);
    const obs = file.get('/obs') as Group;
    strings(
      obs,
      '_index',
      Array.from({ length: cells }, (_, index) => `cell${index + 1}`)
    );
    strings(file.get('/var') as Group, '_index', ['gene1', 'gene2']);
    const categorical = file.get('/obs/cell_type') as Group;
    expect(categorical.attrs.ordered.json_value).toBe(false);
    encodeArray(categorical.create_dataset({ name: 'codes', data: codes }));
    strings(categorical, 'categories', categories);
    if (labels) {
      expect(labels).toHaveLength(cells);
      expect(obs.delete_attribute('column-order')).toBe(0);
      obs.create_attribute('column-order', ['cell_type', 'responder'], null, 'S32');
      strings(obs, 'responder', labels);
    }
  } finally {
    file.close();
  }
  const bytes = FS.readFile(filename).slice();
  FS.unlink(filename);
  return bytes.buffer as ArrayBuffer;
}

async function request(data: Hdf5WorkerRequest): Promise<Hdf5WorkerResponse> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      receive = undefined;
      reject(new Error('HDF5 worker did not respond'));
    }, 3000);
    receive = (message) => {
      clearTimeout(timeout);
      receive = undefined;
      if (message.type === 'error') reject(new Error(`HDF5 worker error: ${message.code}`));
      else resolve(message);
    };
    messageListener({ data } as MessageEvent<Hdf5WorkerRequest>);
  });
}

async function openFixture(codes: Codes, categories?: string[], labels?: string[]): Promise<AnnDataOverview> {
  const response = await request({
    type: 'open',
    filename: 'synthetic.h5ad',
    buffer: await fixtureBuffer(codes, categories, labels),
  });
  expect(response.type).toBe('ready');
  if (response.type !== 'ready' || !response.overview) throw new Error('Missing AnnData overview');
  expect(response.overview.cells).toBe(codes.length);
  expect(response.overview.features).toBe(2);
  return response.overview;
}

async function rawCodes(expected: unknown[]) {
  const response = await request({ type: 'read', path: '/obs/cell_type/codes', requestId: ++nextRequestId });
  expect(response.type).toBe('detail');
  if (response.type !== 'detail') throw new Error('Missing raw categorical detail');
  expect(response.requestId).toBe(nextRequestId);
  expect(response.detail.preview).toEqual(expected);
  expect(response.detail.truncated).toBe(false);
}

function counts(distribution: AnnDataDistribution) {
  return Object.fromEntries(distribution.items.map((item) => [item.labelKey || item.label, item.count]));
}

describe('real HDF5 AnnData categorical composition', () => {
  it.each([
    { name: 'mixed missing', codes: [0, 0, 1, -1], expected: { 'T cell': 2, 'B cell': 1, unlabeled: 1 } },
    { name: 'mostly missing', codes: [0, -1, -1, -1], expected: { 'T cell': 1, unlabeled: 3 } },
    { name: 'all missing', codes: [-1, -1, -1, -1], expected: { unlabeled: 4 } },
    { name: 'no missing', codes: [0, 0, 1, 1], expected: { 'T cell': 2, 'B cell': 2 } },
  ])('keeps the full cell denominator for $name', async ({ codes, expected }) => {
    const overview = await openFixture(new Int8Array(codes));
    expect(overview.distributions).toHaveLength(1);
    expect(overview.distributions[0].total).toBe(4);
    expect(counts(overview.distributions[0])).toEqual(expected);
    await rawCodes(codes);
  });

  it('keeps missing cells when the category table is empty', async () => {
    const overview = await openFixture(new Int8Array([-1, -1, -1]), []);
    expect(overview.distributions[0].total).toBe(3);
    expect(counts(overview.distributions[0])).toEqual({ unlabeled: 3 });
    await rawCodes([-1, -1, -1]);
  });

  it('keeps a genuinely empty annotation empty', async () => {
    const overview = await openFixture(new Int8Array([]), []);
    expect(overview.distributions).toEqual([]);
    await rawCodes([]);
  });

  it.each([
    ['int8', new Int8Array([0, -1, 1])],
    ['int16', new Int16Array([0, -1, 1])],
    ['int32', new Int32Array([0, -1, 1])],
    ['int64', new BigInt64Array([BigInt(0), BigInt(-1), BigInt(1)])],
  ])('preserves missing codes in signed %s', async (_width, codes) => {
    const overview = await openFixture(codes as Codes);
    expect(overview.distributions[0].total).toBe(3);
    expect(counts(overview.distributions[0])).toEqual({ 'T cell': 1, unlabeled: 1, 'B cell': 1 });
    // The existing raw preview converts bigints to strings; numeric widths stay numeric.
    await rawCodes(codes instanceof BigInt64Array ? ['0', '-1', '1'] : [0, -1, 1]);
  });

  it('matches blank-string Unlabeled semantics without changing category labels', async () => {
    const overview = await openFixture(
      new Int8Array([0, -1, 1, -1]),
      ['T cell', 'B cell'],
      ['T cell', '', 'B cell', '']
    );
    const [categorical, strings] = overview.distributions;
    expect(categorical.total).toBe(4);
    expect(strings.total).toBe(4);
    expect(categorical.items).toEqual(strings.items);
    expect(counts(categorical)).toEqual({ 'T cell': 1, 'B cell': 1, unlabeled: 2 });
    await rawCodes([0, -1, 1, -1]);
  });

  it('combines an empty category label and a missing code in the existing bucket', async () => {
    const overview = await openFixture(new Int8Array([0, 1, -1]), ['T cell', '']);
    expect(counts(overview.distributions[0])).toEqual({ 'T cell': 1, unlabeled: 2 });
    expect(overview.distributions[0].total).toBe(3);
    await rawCodes([0, 1, -1]);
  });

  it('preserves existing filtering of invalid codes instead of treating them as missing', async () => {
    // Deliberately corrupt float encoding, including another negative code,
    // fractions, non-finite values and out-of-range indices.
    const codes = [-1, 0, 1, -2, -1.5, 0.5, NaN, Infinity, -Infinity, 2, 999];
    const overview = await openFixture(new Float64Array(codes));
    expect(overview.distributions[0].total).toBe(3);
    expect(counts(overview.distributions[0])).toEqual({ unlabeled: 1, 'T cell': 1, 'B cell': 1 });
    await rawCodes(codes);
  });

  it('does not create a missing bucket from invalid codes alone', async () => {
    const codes = [-2, -1.5, 0.5, NaN, Infinity, -Infinity, 2, 999];
    const overview = await openFixture(new Float64Array(codes));
    expect(overview.distributions).toEqual([]);
    await rawCodes(codes);
  });

  it('preserves top-ten and Other aggregation when missing is a visible category', async () => {
    const categories = Array.from({ length: 12 }, (_, index) => `type${index}`);
    const codes = [...Array(20).fill(-1), ...categories.flatMap((_label, index) => Array(12 - index).fill(index))];
    const overview = await openFixture(new Int16Array(codes), categories);
    const distribution = overview.distributions[0];
    expect(distribution.total).toBe(codes.length);
    expect(distribution.items).toHaveLength(11);
    expect(distribution.items[0]).toEqual({ label: '', labelKey: 'unlabeled', count: 20 });
    expect(distribution.items.slice(1, 10).map((item) => [item.label, item.count])).toEqual(
      categories.slice(0, 9).map((label, index) => [label, 12 - index])
    );
    expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 6 });
    await rawCodes(codes);
  });

  it('retains missing cells in Other when their bucket falls below the top ten', async () => {
    const categories = Array.from({ length: 11 }, (_, index) => `type${index}`);
    const codes = [...categories.flatMap((_label, index) => Array(12 - index).fill(index)), -1];
    const overview = await openFixture(new Int16Array(codes), categories);
    const distribution = overview.distributions[0];
    expect(distribution.total).toBe(codes.length);
    expect(distribution.items).toHaveLength(11);
    expect(distribution.items.some((item) => item.labelKey === 'unlabeled')).toBe(false);
    expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 3 });
    expect(distribution.items.reduce((total, item) => total + item.count, 0)).toBe(codes.length);
    await rawCodes(codes);
  });

  it.each(
    [199, 200, 201].flatMap((categoryCount) => [0, 1, 2].map((missingCount) => ({ categoryCount, missingCount })))
  )(
    'keeps the guard at $categoryCount observed categories with $missingCount missing cells',
    async ({ categoryCount, missingCount }) => {
      const categories = Array.from({ length: categoryCount }, (_, index) => `type${index}`);
      const codes = [...categories.map((_label, index) => index), ...Array(missingCount).fill(-1)];
      const overview = await openFixture(new Int16Array(codes), categories);
      if (categoryCount > 200) {
        expect(overview.distributions).toEqual([]);
      } else {
        expect(overview.distributions).toHaveLength(1);
        const distribution = overview.distributions[0];
        expect(distribution.total).toBe(codes.length);
        expect(distribution.items).toHaveLength(11);
        const visibleCategoryCount = missingCount > 1 ? 9 : 10;
        const visibleCategories = distribution.items.filter((item) => !item.labelKey);
        expect(visibleCategories.map((item) => [item.label, item.count])).toEqual(
          categories.slice(0, visibleCategoryCount).map((label) => [label, 1])
        );
        expect(distribution.items[10]).toEqual({
          label: '',
          labelKey: 'other',
          count: categoryCount - visibleCategoryCount + (missingCount === 1 ? 1 : 0),
        });
        if (missingCount > 1) {
          expect(distribution.items[0]).toEqual({ label: '', labelKey: 'unlabeled', count: missingCount });
        } else {
          expect(distribution.items.some((item) => item.labelKey === 'unlabeled')).toBe(false);
        }
        expect(distribution.items.reduce((total, item) => total + item.count, 0)).toBe(codes.length);
      }
      await rawCodes(codes);
    }
  );

  it('counts a literal Unlabeled category separately from missing cells at the boundary', async () => {
    const categories = ['Unlabeled', ...Array.from({ length: 199 }, (_, index) => `type${index}`)];
    const codes = [...categories.map((_label, index) => index), -1, -1];
    const overview = await openFixture(new Int16Array(codes), categories);
    const distribution = overview.distributions[0];
    expect(distribution.total).toBe(202);
    expect(distribution.items).toHaveLength(11);
    expect(distribution.items[0]).toEqual({ label: '', labelKey: 'unlabeled', count: 2 });
    expect(distribution.items[1]).toEqual({ label: 'Unlabeled', labelKey: undefined, count: 1 });
    expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 191 });
    await rawCodes(codes);
  });

  it.each([200, 201])('counts an observed empty category label toward the %s-category guard', async (categoryCount) => {
    const categories = ['', ...Array.from({ length: categoryCount - 1 }, (_, index) => `type${index}`)];
    // Put missing first: a later valid empty label must still count as a real category.
    const codes = [-1, -1, ...categories.map((_label, index) => index)];
    const overview = await openFixture(new Int16Array(codes), categories);
    if (categoryCount > 200) {
      expect(overview.distributions).toEqual([]);
    } else {
      const distribution = overview.distributions[0];
      expect(distribution.total).toBe(202);
      expect(distribution.items[0]).toEqual({ label: '', labelKey: 'unlabeled', count: 3 });
      expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 190 });
    }
    await rawCodes(codes);
  });

  it('retains the observed-label guard when category tables have unused and duplicate entries', async () => {
    const observed = Array.from({ length: 200 }, (_, index) => `type${index}`);
    const categories = [...observed, 'type0', '', ...Array.from({ length: 299 }, (_, index) => `unused${index}`)];
    const codes = [...observed.map((_label, index) => index), 200, -1];
    const overview = await openFixture(new Int16Array(codes), categories);
    const distribution = overview.distributions[0];
    expect(distribution.total).toBe(202);
    expect(distribution.items[0]).toEqual({ label: 'type0', labelKey: undefined, count: 2 });
    expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 191 });
    await rawCodes(codes);
  });

  it('does not let a dominant missing bucket admit 201 genuine categories', async () => {
    const categories = Array.from({ length: 201 }, (_, index) => `type${index}`);
    const codes = [...Array(100).fill(-1), ...categories.map((_label, index) => index)];
    const overview = await openFixture(new Int16Array(codes), categories);
    expect(overview.distributions).toEqual([]);
    await rawCodes(codes);
  });

  it('keeps the full boundary composition while limiting raw previews to 400 values', async () => {
    const categories = Array.from({ length: 200 }, (_, index) => `type${index}`);
    const codes = [...Array(201).fill(-1), ...categories.map((_label, index) => index)];
    const overview = await openFixture(new Int16Array(codes), categories);
    const distribution = overview.distributions[0];
    expect(distribution.total).toBe(401);
    expect(distribution.items[0]).toEqual({ label: '', labelKey: 'unlabeled', count: 201 });
    expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 191 });
    expect(distribution.items.reduce((total, item) => total + item.count, 0)).toBe(401);
    const response = await request({ type: 'read', path: '/obs/cell_type/codes', requestId: ++nextRequestId });
    expect(response.type).toBe('detail');
    if (response.type !== 'detail') throw new Error('Missing raw categorical detail');
    expect(response.requestId).toBe(nextRequestId);
    expect(response.detail.shape).toEqual([401]);
    expect(response.detail.preview).toEqual(codes.slice(0, 400));
    expect(response.detail.truncated).toBe(true);
  });

  it('keeps invalid codes out of the category limit and denominator at the boundary', async () => {
    const categories = Array.from({ length: 200 }, (_, index) => `type${index}`);
    const codes = [...categories.map((_label, index) => index), -1, -2, -1.5, 0.5, NaN, Infinity, -Infinity, 200, 999];
    const overview = await openFixture(new Float64Array(codes), categories);
    const distribution = overview.distributions[0];
    expect(distribution.total).toBe(201);
    expect(distribution.items[10]).toEqual({ label: '', labelKey: 'other', count: 191 });
    await rawCodes(codes);
  });

  it.each([199, 200, 201])(
    'keeps the string-dataset guard unchanged for %s labels plus a blank',
    async (categoryCount) => {
      const labels = [...Array.from({ length: categoryCount }, (_, index) => `type${index}`), ''];
      const codes = new Int16Array(labels.length);
      const overview = await openFixture(codes, ['T cell'], labels);
      const distribution = overview.distributions.find((item) => item.key === 'responder');
      if (categoryCount >= 200) {
        expect(distribution).toBeUndefined();
      } else {
        expect(distribution?.total).toBe(200);
        expect(distribution?.items[10]).toEqual({ label: '', labelKey: 'other', count: 190 });
      }
      await rawCodes(Array.from(codes));
    }
  );
});
