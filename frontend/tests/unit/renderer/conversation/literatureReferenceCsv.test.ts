import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { loadLiteratureReferenceExport } from '@/renderer/services/runtime/literatureReferenceExport';
import {
  isLiteratureExportFormat,
  literatureExportFilename,
  literatureExportFormats,
  type LiteratureExportFormat,
} from '@/common/adapter/literatureReferenceExport';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const format = 'csv' as LiteratureExportFormat;
const columns = [
  'item_id',
  'title',
  'authors',
  'year',
  'item_type',
  'publication',
  'volume',
  'issue',
  'pages',
  'doi',
  'pmid',
  'pmcid',
  'arxiv_id',
  'url',
  'abstract',
  'language',
];
const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
const header = columns.map(quote).join(',') + '\r\n';
const batch = (ids: readonly string[]) =>
  header +
  ids
    .map((id) => [id, '研究, "quoted"\r\nsecond line', ...Array<string>(14).fill('')].map(quote).join(','))
    .join('\r\n') +
  '\r\n';
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const response = (ids: string[], content = batch(ids)) =>
  json({ schema: 'x-science.literature-export.v1', item_ids: ids, format, content });

describe('Library bibliographic CSV transport', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('csv-owner');
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it('registers CSV on the existing format contract and safe filename owner', () => {
    expect(isLiteratureExportFormat('csv')).toBe(true);
    expect((literatureExportFormats as Record<string, unknown>).csv).toEqual({
      extension: 'csv',
      mimeType: 'text/csv;charset=utf-8',
    });
    expect(literatureExportFilename('研究 / selected', format)).toBe('研究 - selected.csv');
  });
  it('exports 205 ordered IDs in bounded batches with one BOM/header and exact multiline bytes', async () => {
    const ids = Array.from({ length: 205 }, (_, index) => `paper-${index}`);
    const groups: string[][] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        expect(String(input)).toContain('/api/literature/export-references');
        expect(init?.method).toBe('POST');
        const body = JSON.parse(String(init?.body));
        expect(body.format).toBe('csv');
        groups.push(body.item_ids);
        return response(body.item_ids);
      })
    );
    const text = await loadLiteratureReferenceExport(ids, format, new AbortController().signal);
    expect(groups.map((group) => group.length)).toEqual([200, 5]);
    expect(groups.flat()).toEqual(ids);
    expect(text).toBe('\uFEFF' + batch(ids));
    expect(text.match(/\uFEFF/gu)).toHaveLength(1);
    expect(text.split(header)).toHaveLength(2);
  });
  it.each(['header', 'bom', 'lf', 'missing-row', 'extra-row', 'wrong-row-id', 'unclosed', 'identity', 'format'])(
    'rejects a malformed %s batch instead of treating text as complete CSV',
    async (kind) => {
      const fetcher = vi.fn(async () => {
        let content = batch(['owned']);
        if (kind === 'header') content = content.replace('item_id', 'unexpected');
        if (kind === 'bom') content = '\uFEFF' + content;
        if (kind === 'lf') content = content.replaceAll('\r\n', '\n');
        if (kind === 'missing-row') content = header;
        if (kind === 'extra-row') content = batch(['owned', 'extra']);
        if (kind === 'wrong-row-id') content = batch(['other']);
        if (kind === 'unclosed') content = content.slice(0, -3);
        return json({
          schema: 'x-science.literature-export.v1',
          item_ids: kind === 'identity' ? ['other'] : ['owned'],
          format: kind === 'format' ? 'ris' : 'csv',
          content,
        });
      });
      vi.stubGlobal('fetch', fetcher);
      await expect(loadLiteratureReferenceExport(['owned'], format, new AbortController().signal)).rejects.toThrow(
        'Invalid Library'
      );
      expect(fetcher).toHaveBeenCalledTimes(1);
    }
  );
  it('rejects a failed second batch without returning a partial file and permits a complete retry', async () => {
    const ids = Array.from({ length: 205 }, (_, index) => `paper-${index}`);
    let calls = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        const body = JSON.parse(String(init?.body));
        return ++calls === 2 ? json({ code: 'LITERATURE_UNAVAILABLE' }, 404) : response(body.item_ids);
      })
    );
    await expect(loadLiteratureReferenceExport(ids, format, new AbortController().signal)).rejects.toThrow();
    expect(calls).toBe(2);
    expect(await loadLiteratureReferenceExport(ids, format, new AbortController().signal)).toBe('\uFEFF' + batch(ids));
    expect(calls).toBe(4);
  });
  it('keeps UTF8 envelopes bounded and binds a spreadsheet-shielded row ID to its original identity', async () => {
    const ids = Array.from({ length: 205 }, (_, index) => `${index}-${'é'.repeat(240)}`);
    const groups: string[][] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        expect(new TextEncoder().encode(String(init?.body)).length).toBeLessThanOrEqual(64 * 1024);
        const body = JSON.parse(String(init?.body));
        groups.push(body.item_ids);
        return response(body.item_ids);
      })
    );
    expect(await loadLiteratureReferenceExport(ids, format, new AbortController().signal)).toBe('\uFEFF' + batch(ids));
    expect(groups[0].length).toBeLessThan(200);
    expect(groups.flat()).toEqual(ids);
    const shielded = batch(['=opaque-id']).replace('"=opaque-id"', '"\'=opaque-id"');
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => response(['=opaque-id'], shielded))
    );
    expect(await loadLiteratureReferenceExport(['=opaque-id'], format, new AbortController().signal)).toBe(
      '\uFEFF' + shielded
    );
  });
  it.each(['abort', 'account-aba'] as const)('fences a late CSV response after %s', async (boundary) => {
    let release!: (value: Response) => void;
    const held = new Promise<Response>((resolve) => {
      release = resolve;
    });
    const fetcher = vi.fn(() => held);
    vi.stubGlobal('fetch', fetcher);
    const abort = new AbortController();
    const loading = loadLiteratureReferenceExport(['owned'], format, abort.signal);
    const rejected = expect(loading).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
    if (boundary === 'abort') abort.abort();
    else {
      setRendererAccountOwner('other');
      setRendererAccountOwner('csv-owner');
    }
    release(response(['owned']));
    await rejected;
  });
});
