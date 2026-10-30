import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { loadLiteratureReferenceExport } from '@/renderer/services/runtime/literatureReferenceExport';
import { literatureExportFilename, type LiteratureExportFormat } from '@/common/adapter/literatureReferenceExport';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const content = (ids: string[], format: LiteratureExportFormat) =>
  ids
    .map((id) => (format === 'bibtex' ? `@misc{${id}, title={Stored}}` : `TY  - JOUR\nTI  - ${id}\nER  - `))
    .join('\n\n');
describe('Library reference export transport completeness', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('export-account');
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it.each(['bibtex', 'ris'] as const)(
    'exports all201 explicit IDs as %s through bounded requests without truncation',
    async (format) => {
      const ids = Array.from({ length: 201 }, (_, index) => `paper-${index}`);
      const groups: string[][] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          expect(String(input)).toContain('/api/literature/export-references');
          expect(init?.method).toBe('POST');
          const body = JSON.parse(String(init?.body));
          expect(body.format).toBe(format);
          groups.push(body.item_ids);
          return json({ schema: 'x-science.literature-export.v1', ...body, content: content(body.item_ids, format) });
        })
      );
      const text = await loadLiteratureReferenceExport(ids, format, new AbortController().signal);
      expect(groups.map((group) => group.length)).toEqual([200, 1]);
      expect(groups.flat()).toEqual(ids);
      expect(text.match(format === 'bibtex' ? /@misc\{/g : /^TY  - /gm)).toHaveLength(201);
    }
  );
  it.each(['bibtex', 'ris'] as const)(
    'rejects failed suffix or mismatched %s identity/format instead of returning partial content',
    async (format) => {
      const ids = Array.from({ length: 201 }, (_, i) => `paper-${i}`);
      let calls = 0;
      vi.stubGlobal(
        'fetch',
        vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
          const body = JSON.parse(String(init?.body));
          return ++calls === 1
            ? json({ schema: 'x-science.literature-export.v1', ...body, content: content(body.item_ids, format) })
            : json({ code: 'LITERATURE_UNAVAILABLE' }, 404);
        })
      );
      await expect(loadLiteratureReferenceExport(ids, format, new AbortController().signal)).rejects.toThrow();
      expect(calls).toBe(2);
      vi.stubGlobal(
        'fetch',
        vi.fn(async () =>
          json({
            schema: 'x-science.literature-export.v1',
            item_ids: ['owned'],
            format: format === 'bibtex' ? 'ris' : 'bibtex',
            content: 'Wrong format',
          })
        )
      );
      await expect(loadLiteratureReferenceExport(['owned'], format, new AbortController().signal)).rejects.toThrow(
        'Invalid Library'
      );
      vi.stubGlobal(
        'fetch',
        vi.fn(async () =>
          json({ schema: 'x-science.literature-export.v1', item_ids: ['foreign'], format, content: 'Wrong identity' })
        )
      );
      await expect(loadLiteratureReferenceExport(['owned'], format, new AbortController().signal)).rejects.toThrow(
        'Invalid Library'
      );
    }
  );
  it('bounds UTF8 request bodies as well as IDcount without imposing a total selection cap', async () => {
    const ids = Array.from({ length: 201 }, (_, i) => `${i}-${'é'.repeat(240)}`);
    const groups: string[][] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        expect(new TextEncoder().encode(String(init?.body)).length).toBeLessThanOrEqual(64 * 1024);
        const body = JSON.parse(String(init?.body));
        expect(body.item_ids.length).toBeLessThanOrEqual(200);
        groups.push(body.item_ids);
        return json({ schema: 'x-science.literature-export.v1', ...body, content: content(body.item_ids, 'ris') });
      })
    );
    const text = await loadLiteratureReferenceExport(ids, 'ris', new AbortController().signal);
    expect(groups.flat()).toEqual(ids);
    expect(groups[0].length).toBeLessThan(200);
    expect(text.match(/^TY  - /gm)).toHaveLength(201);
    expect(literatureExportFilename('Ｐroject / Test-references', 'ris')).toBe('Project - Test-references.ris');
  });
  it('hands a safe filename to the existing browser owner without losing Unicode text', () => {
    expect(literatureExportFilename('研究/\u0000Test\n Name-references', 'ris')).toBe('研究- Test Name-references.ris');
  });
});
