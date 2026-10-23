import { afterEach, expect, it, vi } from 'vitest';
import { decodeRelatedResult, type RelatedRequest, type RelatedRecord } from '@/common/adapter/literatureRelated';
import { loadRelatedLiterature } from '@/renderer/services/runtime/literatureRelated';

const mock = vi.hoisted(() => ({ request: vi.fn(), account: 'owner-A' }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: mock.request }));
vi.mock('@/renderer/services/rendererAccountScope', () => ({ getRendererAccountScopeToken: () => mock.account }));
const request: RelatedRequest = {
  item_id: 'seed-item',
  expected_metadata_revision: 3,
  direction: 'references',
  offset: 0,
};
const paper: RelatedRecord = {
  paper_id: 'a'.repeat(40),
  title: 'A real returned title',
  abstract: 'Returned abstract',
  authors: ['J. Example', 'Research Group'],
  year: null,
  issued_text: '',
  container_title: 'Journal',
  item_type: 'journalArticle',
  url: `https://www.semanticscholar.org/paper/${'a'.repeat(40)}`,
  identifiers: [{ scheme: 'doi', value: '10.1000/example' }],
  volume: '',
  pages: '',
};
const result = () => ({
  schema: 'x-science.literature-related.v1',
  item_id: 'seed-item',
  metadata_revision: 3,
  direction: 'references',
  state: 'ready',
  resolution_token: '11111111-1111-4111-8111-111111111111',
  seed: paper,
  records: [paper],
});
afterEach(() => {
  vi.clearAllMocks();
  mock.account = 'owner-A';
});

it('preserves bibliographic authors and unknown year without fabricating a readable PDF', () => {
  const decoded = decodeRelatedResult(result(), request);
  expect(decoded.records[0]).toEqual(paper);
  expect(decoded.records[0]).not.toHaveProperty('attachments');
  expect(decoded.records[0]).not.toHaveProperty('available');
});
it.each([{ item_id: 'other-item' }, { metadata_revision: 4 }, { direction: 'citations' }])(
  'rejects an outcome from another request: %j',
  (patch) => {
    expect(() => decodeRelatedResult({ ...result(), ...patch }, request)).toThrow(
      'Invalid related literature response'
    );
  }
);
it('requires confirmation-only results and distinguishes failure from an empty successful page', () => {
  expect(() => decodeRelatedResult({ ...result(), state: 'match-required' }, request)).toThrow();
  expect(decodeRelatedResult({ ...result(), state: 'match-required', records: [] }, request).state).toBe(
    'match-required'
  );
  expect(() => decodeRelatedResult({ ...result(), state: 'unavailable', records: [] }, request)).toThrow();
  expect(decodeRelatedResult({ ...result(), state: 'unavailable', issue: 'timeout', records: [] }, request).issue).toBe(
    'timeout'
  );
  expect(decodeRelatedResult({ ...result(), records: [] }, request).state).toBe('ready');
});
it('rejects forged URLs and non-advancing or duplicated provider pages', () => {
  expect(() => decodeRelatedResult({ ...result(), records: [{ ...paper, url: 'javascript:bad' }] }, request)).toThrow();
  expect(() => decodeRelatedResult({ ...result(), records: [paper, paper] }, request)).toThrow();
  expect(() => decodeRelatedResult({ ...result(), next_offset: 0 }, request)).toThrow();
  expect(() => decodeRelatedResult({ ...result(), records: Array(21).fill(paper) }, request)).toThrow();
});
it('uses the existing HTTP bridge with cancellation and validates the returned owner coordinates', async () => {
  mock.request.mockResolvedValue(result());
  const signal = new AbortController().signal;
  expect((await loadRelatedLiterature(request, signal)).records).toHaveLength(1);
  expect(mock.request).toHaveBeenCalledWith('POST', '/api/literature/related', request, { signal, timeoutMs: 35_000 });
});
it.each(['account', 'abort'])('discards a late response after %s changes', async (kind) => {
  let finish!: (value: unknown) => void;
  mock.request.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      })
  );
  const controller = new AbortController();
  const pending = loadRelatedLiterature(request, controller.signal);
  if (kind === 'account') mock.account = 'owner-B';
  else controller.abort();
  finish(result());
  await expect(pending).rejects.toThrow('Literature query owner changed');
});
