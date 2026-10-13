import type {
  AnnDataDistribution,
  AnnDataOverview,
  Hdf5TreeNode,
  Hdf5WorkerRequest,
  Hdf5WorkerResponse,
} from '@/renderer/pages/conversation/Preview/components/viewers/xScienceHdf5Model';
import { formatHdf5DisplayValue } from '@/renderer/pages/conversation/Preview/components/viewers/xScienceHdf5Model';
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

const integerCases = [
  { name: 'signed_safe', dtype: '<q', value: BigInt('9007199254740991') },
  { name: 'signed_safe_negative', dtype: '<q', value: BigInt('-9007199254740991') },
  { name: 'signed_unsafe', dtype: '<q', value: BigInt('9007199254740993') },
  { name: 'signed_unsafe_negative', dtype: '<q', value: BigInt('-9007199254740993') },
  { name: 'signed_max', dtype: '<q', value: BigInt('9223372036854775807') },
  { name: 'signed_min', dtype: '<q', value: BigInt('-9223372036854775808') },
  { name: 'unsigned_safe', dtype: '<Q', value: BigInt('9007199254740991') },
  { name: 'unsigned_unsafe', dtype: '<Q', value: BigInt('9007199254740993') },
  { name: 'unsigned_max', dtype: '<Q', value: BigInt('18446744073709551615') },
  { name: 'unsigned_zero', dtype: '<Q', value: BigInt(0) },
  { name: 'signed_exact_unsafe', dtype: '<q', value: BigInt('9007199254740992') },
  { name: 'timestamp_a', dtype: '<q', value: BigInt('1760000000000000000') },
  { name: 'timestamp_b', dtype: '<q', value: BigInt('1760000000000000001') },
];

async function integerFixtureBuffer() {
  const { FS } = await h5wasm.ready;
  const filename = `/integer-fixture-${crypto.randomUUID()}.h5`;
  // Reuse the genuine ordered=false enum to catch accidental replacement of
  // every json_value read with raw integer-valued enum reads.
  FS.writeFile(filename, template);
  const file = new h5wasm.File(filename, 'a');
  try {
    const group = file.create_group('integer_attributes');
    for (const { name, dtype, value } of integerCases) {
      const data = dtype === '<q' ? new BigInt64Array([value]) : new BigUint64Array([value]);
      const scalar = file.create_dataset({ name, dtype, data, shape: [] });
      file.create_dataset({ name: `${name}_vector`, dtype, data, shape: [1] });
      file.create_attribute(name, data, [], dtype);
      group.create_attribute(name, data, [], dtype);
      scalar.create_attribute('integer', data, [], dtype);
    }
    const signed = new BigInt64Array([BigInt('9007199254740993'), BigInt('-9007199254740993')]);
    const unsigned = new BigUint64Array([BigInt(0), BigInt('18446744073709551615')]);
    group.create_attribute('signed_vector', signed, [2], '<q');
    group.create_attribute('unsigned_vector', unsigned, [2], '<Q');
    group.create_attribute('signed_matrix', new BigInt64Array([...signed, ...signed]), [2, 2], '<q');
    file.create_dataset({ name: 'int32_control', data: new Int32Array([2147483647]), shape: [] });
    file.create_dataset({ name: 'float_control', data: new Float64Array([1.125]), shape: [] });
    file.create_dataset({ name: 'string_control', data: ['sample00123'], dtype: 'S', shape: [] });
    group.create_attribute('int32_control', new Int32Array([2147483647]), [], '<i');
    group.create_attribute('float_control', new Float64Array([1.125]), [], '<d');
    group.create_attribute('string_control', 'sample00123');
    const bounded = file.create_group('bounded_attributes');
    for (let index = 0; index < 31; index += 1) bounded.create_attribute(`item${index}`, index);
    bounded.create_attribute('long_integer_vector', new BigInt64Array(100).fill(signed[0]), [100], '<q');
    file.create_dataset({ name: 'long_integer_vector', data: new BigInt64Array(401).fill(signed[0]) });
    file.create_dataset({ name: 'integer_matrix', data: new BigInt64Array(600).fill(signed[0]), shape: [20, 30] });
    file.create_attribute('long_integer_vector', new BigInt64Array(100).fill(signed[0]), [100], '<q');
  } finally {
    file.close();
  }
  const bytes = FS.readFile(filename).slice();
  FS.unlink(filename);
  return bytes.buffer as ArrayBuffer;
}

function child(root: Hdf5TreeNode, name: string) {
  const node = root.children?.find((candidate) => candidate.name === name);
  if (!node) throw new Error(`Missing HDF5 fixture node: ${name}`);
  return node;
}

describe('real HDF5 integer preview precision', () => {
  let root: Hdf5TreeNode;

  beforeAll(async () => {
    const response = await request({ type: 'open', filename: 'integers.h5', buffer: await integerFixtureBuffer() });
    if (response.type !== 'ready') throw new Error('Missing generic HDF5 tree');
    expect(response.overview).toBeUndefined();
    expect(response.truncated).toBe(false);
    root = response.root;
  });

  it.each(integerCases)('keeps the exact visible decimal for scalar $name', async ({ name, dtype, value }) => {
    const response = await request({ type: 'read', path: `/${name}`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing scalar integer detail');
    expect(response.requestId).toBe(nextRequestId);
    expect(response.detail.shape).toEqual([]);
    expect(response.detail.dtype).toBe(dtype);
    expect(response.detail.truncated).toBe(false);
    expect(response.detail.preview).toBe(String(value));
    // int64 minimum is exactly representable as a binary Number, yet that
    // Number's decimal JSON text is wrong. Check the text the viewer uses.
    expect(JSON.stringify(response.detail.preview)).toBe(JSON.stringify(String(value)));
  });

  it.each(integerCases)('keeps scalar $name attributes exact on every entity', ({ name, value }) => {
    for (const node of [root, child(root, 'integer_attributes'), child(root, name)]) {
      const attribute = node.attributes.find((item) => item.name === (node.name === name ? 'integer' : name));
      expect(attribute?.value).toBe(JSON.stringify(String(value)));
    }
  });

  it.each(integerCases)('keeps the existing exact vector $name', async ({ name, value }) => {
    const response = await request({ type: 'read', path: `/${name}_vector`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing vector integer detail');
    expect(response.detail.shape).toEqual([1]);
    expect(response.detail.preview).toEqual([String(value)]);
    expect(response.detail.truncated).toBe(false);
  });

  it.each([
    { name: 'signed_vector', values: ['9007199254740993', '-9007199254740993'] },
    { name: 'unsigned_vector', values: ['0', '18446744073709551615'] },
    {
      name: 'signed_matrix',
      values: ['9007199254740993', '-9007199254740993', '9007199254740993', '-9007199254740993'],
    },
  ])('keeps exact $name attributes with the existing flat array layout', ({ name, values }) => {
    expect(child(root, 'integer_attributes').attributes.find((item) => item.name === name)?.value).toBe(
      JSON.stringify(values)
    );
  });

  it('keeps acquisition timestamps one nanosecond apart distinct', () => {
    const first = root.attributes.find((item) => item.name === 'timestamp_a')?.value;
    const second = root.attributes.find((item) => item.name === 'timestamp_b')?.value;
    expect(first).toBe('"1760000000000000000"');
    expect(second).toBe('"1760000000000000001"');
    expect(second).not.toBe(first);
  });

  it.each([
    { name: 'int32_control', value: 2147483647 },
    { name: 'float_control', value: 1.125 },
    { name: 'string_control', value: 'sample00123' },
  ])('preserves scalar and attribute $name JSON semantics', async ({ name, value }) => {
    const response = await request({ type: 'read', path: `/${name}`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing control detail');
    expect(response.detail.preview).toBe(value);
    expect(response.detail.shape).toEqual([]);
    expect(child(root, 'integer_attributes').attributes.find((item) => item.name === name)?.value).toBe(
      JSON.stringify(value)
    );
  });

  it('keeps the genuine h5py boolean enum attribute a JSON boolean', () => {
    const categorical = child(child(root, 'obs'), 'cell_type');
    expect(categorical.attributes.find((item) => item.name === 'ordered')?.value).toBe('false');
  });

  it('retains attribute count and text caps', () => {
    expect(child(root, 'bounded_attributes').attributes).toHaveLength(30);
    const summary = root.attributes.find((item) => item.name === 'long_integer_vector')?.value;
    const exact = JSON.stringify(Array(100).fill('9007199254740993'));
    expect(summary).toBe(`${exact.slice(0, 240)}...`);
    expect(summary).toHaveLength(243);
  });

  it.each([
    { name: 'long_integer_vector', shape: [401] },
    { name: 'integer_matrix', shape: [20, 30] },
  ])('retains the bounded slice and original shape for $name', async ({ name, shape }) => {
    const response = await request({ type: 'read', path: `/${name}`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing bounded integer detail');
    expect(response.detail.shape).toEqual(shape);
    expect(response.detail.preview).toEqual(Array(400).fill('9007199254740993'));
    expect(response.detail.truncated).toBe(true);
  });
});

const nonfiniteCases = [
  { name: 'nan64', data: new Float64Array([NaN]), token: 'NaN' },
  { name: 'positive_infinity64', data: new Float64Array([Infinity]), token: 'Infinity' },
  { name: 'negative_infinity64', data: new Float64Array([-Infinity]), token: '-Infinity' },
  { name: 'nan32', data: new Float32Array([NaN]), token: 'NaN' },
  { name: 'positive_infinity32', data: new Float32Array([Infinity]), token: 'Infinity' },
  { name: 'negative_infinity32', data: new Float32Array([-Infinity]), token: '-Infinity' },
];

async function nonfiniteFixtureBuffer() {
  const { FS } = await h5wasm.ready;
  const filename = `/nonfinite-fixture-${crypto.randomUUID()}.h5`;
  // The locked h5wasm fixture contains a genuine H5S_NULL dataset and
  // attribute, not a mocked null return. Add ordinary real HDF5 values to it.
  FS.writeFile(filename, readFileSync(new URL('../../../node_modules/h5wasm/test/empty.h5', import.meta.url)));
  const file = new h5wasm.File(filename, 'a');
  try {
    for (const { name, data } of nonfiniteCases) {
      const dataset = file.create_dataset({ name, data, shape: [] });
      file.create_attribute(name, data, []);
      dataset.create_attribute('copied_value', data, []);
    }
    const values = [NaN, Infinity, -Infinity, 0, 1.25, -2.5];
    for (const [name, data] of [
      ['mixed64', new Float64Array(values)],
      ['mixed32', new Float32Array(values)],
    ] as const) {
      file.create_dataset({ name, data });
      file.create_attribute(name, data);
    }
    file.create_dataset({ name: 'matrix', data: new Float64Array(values), shape: [2, 3] });
    file.create_dataset({ name: 'finite', data: new Float64Array([0, 1.25, -2.5]) });
    file.create_attribute('finite', new Float64Array([0, 1.25, -2.5]));
    file.create_dataset({ name: 'empty', data: new Float64Array([]) });
    file.create_attribute('empty', new Float64Array([]));
    for (const value of ['NaN', 'Infinity', '-Infinity', 'null']) {
      file.create_dataset({ name: `literal_${value}`, data: [value], dtype: 'S', shape: [] });
      file.create_attribute(`literal_${value}`, value);
    }
    file.create_dataset({ name: 'bounded', data: new Float64Array(401).fill(NaN) });
    file.create_attribute('bounded', new Float64Array(401).fill(NaN));
  } finally {
    file.close();
  }
  const bytes = FS.readFile(filename).slice();
  FS.unlink(filename);
  return bytes.buffer as ArrayBuffer;
}

describe('HDF5 JSON-like display policy', () => {
  it.each([null, true, false, 0, -0, 1.25, 'NaN', 'Infinity', '-Infinity', 'null', '9007199254740993', [], {}])(
    'keeps the ordinary JSON spelling of %j',
    (value) => {
      expect(formatHdf5DisplayValue(value)).toBe(JSON.stringify(value));
      expect(formatHdf5DisplayValue(value, 2)).toBe(JSON.stringify(value, null, 2));
    }
  );

  it('preserves nested finite, empty, boolean, escaped-string and exact-decimal values', () => {
    const value = {
      quoted: '"NaN"\\Infinity\n',
      decimal: '18446744073709551615',
      values: [[null, true, false, 1.25], [], {}],
    };
    expect(formatHdf5DisplayValue(value)).toBe(JSON.stringify(value));
    expect(formatHdf5DisplayValue(value, 2)).toBe(JSON.stringify(value, null, 2));
  });

  it('distinguishes nonfinite numbers from strings and null at every nested level without mutation', () => {
    const value = { NaN: [NaN, 'NaN', null], nested: { values: [Infinity, 'Infinity', -Infinity, '-Infinity'] } };
    const original = structuredClone(value);
    expect(formatHdf5DisplayValue(value)).toBe(
      '{"NaN":[NaN,"NaN",null],"nested":{"values":[Infinity,"Infinity",-Infinity,"-Infinity"]}}'
    );
    expect(formatHdf5DisplayValue(value, 2)).toBe(
      '{' +
        '\n  "NaN": [\n    NaN,\n    "NaN",\n    null\n  ],\n  "nested": {\n    "values": [\n      Infinity,\n      "Infinity",\n      -Infinity,\n      "-Infinity"\n    ]\n  }\n}'
    );
    expect(value).toEqual(original);
  });

  it('retains JSON omission and null-slot semantics for undefined values', () => {
    const value: { absent: undefined; array: (undefined | null)[] } = { absent: undefined, array: [undefined, null] };
    expect(formatHdf5DisplayValue(value)).toBe(JSON.stringify(value));
    expect(formatHdf5DisplayValue(value, 2)).toBe(JSON.stringify(value, null, 2));
    expect(formatHdf5DisplayValue(undefined)).toBeUndefined();
  });
});

describe('real HDF5 nonfinite scientific display', () => {
  let root: Hdf5TreeNode;

  beforeAll(async () => {
    const response = await request({ type: 'open', filename: 'nonfinite.h5', buffer: await nonfiniteFixtureBuffer() });
    if (response.type !== 'ready') throw new Error('Missing generic HDF5 tree');
    expect(response.overview).toBeUndefined();
    expect(response.truncated).toBe(false);
    root = response.root;
  });

  it.each(nonfiniteCases)(
    'preserves raw $name and uses an unquoted numeric attribute token',
    async ({ name, data, token }) => {
      const response = await request({ type: 'read', path: `/${name}`, requestId: ++nextRequestId });
      if (response.type !== 'detail') throw new Error('Missing nonfinite detail');
      expect(response.detail.preview).toBe(data[0]);
      expect(response.detail.shape).toEqual([]);
      expect(response.detail.truncated).toBe(false);
      // Display-only policy: numeric nonfinite tokens are unquoted, whereas
      // strings retain JSON quoting. This text is deliberately JSON-like.
      expect(root.attributes.find((item) => item.name === name)?.value).toBe(token);
      expect(child(root, name).attributes.find((item) => item.name === 'copied_value')?.value).toBe(token);
    }
  );

  it.each(['mixed64', 'mixed32', 'matrix'])('preserves the numeric values and shape of $name', async (name) => {
    const response = await request({ type: 'read', path: `/${name}`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing array detail');
    expect(response.detail.preview).toEqual([NaN, Infinity, -Infinity, 0, 1.25, -2.5]);
    expect(response.detail.shape).toEqual(name === 'matrix' ? [2, 3] : [6]);
    expect(response.detail.truncated).toBe(false);
    if (name !== 'matrix') {
      expect(root.attributes.find((item) => item.name === name)?.value).toBe('[NaN,Infinity,-Infinity,0,1.25,-2.5]');
    }
  });

  it.each(['NaN', 'Infinity', '-Infinity', 'null'])('keeps literal string %s quoted', async (value) => {
    const response = await request({ type: 'read', path: `/literal_${value}`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing string detail');
    expect(response.detail.preview).toBe(value);
    expect(root.attributes.find((item) => item.name === `literal_${value}`)?.value).toBe(JSON.stringify(value));
  });

  it.each([
    { name: 'finite', expected: [0, 1.25, -2.5], shape: [3], attribute: '[0,1.25,-2.5]' },
    { name: 'empty', expected: [], shape: [0], attribute: '[]' },
    { name: 'empty_dataset', expected: null, shape: null, attribute: 'null' },
  ])('keeps $name distinct from nonfinite numeric values', async ({ name, expected, shape, attribute }) => {
    const response = await request({ type: 'read', path: `/${name}`, requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing control detail');
    expect(response.detail.preview).toEqual(expected);
    expect(response.detail.shape).toEqual(shape);
    expect(response.detail.truncated).toBe(false);
    expect(root.attributes.find((item) => item.name === (name === 'empty_dataset' ? 'empty_attr' : name))?.value).toBe(
      attribute
    );
  });

  it('keeps existing preview and attribute text bounds', async () => {
    const response = await request({ type: 'read', path: '/bounded', requestId: ++nextRequestId });
    if (response.type !== 'detail') throw new Error('Missing bounded detail');
    expect(response.detail.preview).toEqual(Array(400).fill(NaN));
    expect(response.detail.shape).toEqual([401]);
    expect(response.detail.truncated).toBe(true);
    const text = `[${Array(400).fill('NaN').join(',')}]`;
    expect(root.attributes.find((item) => item.name === 'bounded')?.value).toBe(`${text.slice(0, 240)}...`);
  });
});

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
