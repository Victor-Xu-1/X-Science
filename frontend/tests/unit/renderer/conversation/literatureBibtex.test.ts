import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { loadLiteratureBibtex } from '@/renderer/services/runtime/literatureBibtex';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
describe('Library BibTeX export transport completeness', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('export-account');
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it('exports all201 explicit IDs through bounded requests without truncation or provider/mutation calls', async () => {
    const ids = Array.from({ length: 201 }, (_, index) => `paper-${index}`);
    const groups: string[][] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        expect(String(input)).toContain('/api/literature/export-bibtex');
        expect(init?.method).toBe('POST');
        const batch = JSON.parse(String(init?.body)).item_ids as string[];
        groups.push(batch);
        return json({
          schema: 'x-science.literature-bibtex.v1',
          item_ids: batch,
          bibtex: batch.map((id) => `@misc{${id}, title={Stored}}`).join('\n'),
        });
      })
    );
    const text = await loadLiteratureBibtex(ids, new AbortController().signal);
    expect(groups.map((group) => group.length)).toEqual([200, 1]);
    expect(groups.flat()).toEqual(ids);
    expect(text.match(/@misc\{/g)).toHaveLength(201);
  });
  it('rejects a failed suffix or mismatched identity instead of returning partial text', async () => {
    const ids = Array.from({ length: 201 }, (_, index) => `paper-${index}`);
    let calls = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        const batch = JSON.parse(String(init?.body)).item_ids;
        return ++calls === 1
          ? json({ schema: 'x-science.literature-bibtex.v1', item_ids: batch, bibtex: '@misc{first, title={Stored}}' })
          : json({ code: 'LITERATURE_UNAVAILABLE' }, 404);
      })
    );
    await expect(loadLiteratureBibtex(ids, new AbortController().signal)).rejects.toThrow();
    expect(calls).toBe(2);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        json({
          schema: 'x-science.literature-bibtex.v1',
          item_ids: ['foreign'],
          bibtex: '@misc{foreign, title={Wrong}}',
        })
      )
    );
    await expect(loadLiteratureBibtex(['owned'], new AbortController().signal)).rejects.toThrow('Invalid Library');
  });
});
