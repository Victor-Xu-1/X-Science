import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { decodeLiteratureStudyBatch } from '@/renderer/pages/library/literatureStudyBatchContract';
import { literatureStudySourceKey, type LiteratureStudyView } from '@/common/adapter/literatureStudySnapshot';
import { httpRequest } from '@/common/adapter/httpBridge';
import { loadLiteratureItem } from '@/renderer/services/runtime/literatureLibrary';
import { readStudyBatchResult, requestStudyBatch } from '@/renderer/pages/library/literatureStudyBatchApi';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: vi.fn() }));
vi.mock('@/renderer/services/runtime/literatureLibrary', () => ({ loadLiteratureItem: vi.fn() }));
const item: LiteratureItem = {
  id: 'paper',
  title: 'Synthetic source',
  metadata_revision: 1,
  metadata: {},
  project_ids: [],
  attachments: [],
  deleted: false,
};
const uuid = '00000000-0000-4000-8000-000000000001';
const value = () => ({
  schema: 'x-science.literature-study-batch.v1',
  limit: 5,
  concurrency: 2,
  batch: {
    id: uuid,
    revision: 1,
    paused: false,
    rows: [
      {
        item_id: item.id,
        state: 'succeeded',
        result_revision: 1,
        request: {
          operation_id: uuid,
          language: 'en-US',
          expected_source_key: literatureStudySourceKey(item),
          expected_revision: 0,
        },
      },
    ],
  },
});
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('batch-owner');
  vi.clearAllMocks();
});
afterEach(resetRendererAccountScopeForTest);

describe('batch overview wire and result identity', () => {
  it.each([{ state: ['pending'] }, { state: { value: 'pending' } }, { state: 1 }])(
    'rejects non-string checkpoint state $state',
    ({ state }) => {
      const initial = value();
      const malformed: unknown = {
        ...initial,
        batch: { ...initial.batch, rows: [{ item_id: item.id, request: initial.batch.rows[0].request, state }] },
      };
      expect(() => decodeLiteratureStudyBatch(malformed)).toThrow();
    }
  );

  it('accepts a bounded typed checkpoint and rejects unproven success or extra state', () => {
    expect(decodeLiteratureStudyBatch(value()).batch?.rows[0].result_revision).toBe(1);
    const unknown = { ...value(), secondQueue: [] as unknown[] };
    expect(() => decodeLiteratureStudyBatch(unknown)).toThrow();
    const bad = value();
    delete (bad.batch.rows[0] as { result_revision?: number }).result_revision;
    expect(() => decodeLiteratureStudyBatch(bad)).toThrow();
    expect(() => decodeLiteratureStudyBatch({ ...value(), concurrency: 3 })).toThrow();
  });

  it('reads the original owner-authorized paper and checks the frozen result revision', async () => {
    vi.mocked(loadLiteratureItem).mockResolvedValue(item);
    const view: LiteratureStudyView = {
      schema: 'x-science.literature-study.v1',
      source_key: literatureStudySourceKey(item),
      revision: 1,
      available: true,
      snapshot: {
        source_key: literatureStudySourceKey(item),
        coverage: 'abstract',
        model: 'fixture',
        sections: { question: 'Frozen result', population: null, methods: null, results: null, limitations: null },
      },
    };
    vi.mocked(httpRequest).mockResolvedValue(view);
    const row = decodeLiteratureStudyBatch(value()).batch!.rows[0];
    const result = await readStudyBatchResult(uuid, 0, row, new AbortController().signal);
    expect(result.view.revision).toBe(1);
    vi.mocked(httpRequest).mockResolvedValue({ ...view, revision: 2 });
    await expect(readStudyBatchResult(uuid, 0, row, new AbortController().signal)).rejects.toThrow('no longer matches');
  });

  it('discards a late read after account change', async () => {
    let resolve!: (value: unknown) => void;
    vi.mocked(httpRequest).mockReturnValue(
      new Promise((done) => {
        resolve = done;
      })
    );
    const pending = requestStudyBatch(new AbortController().signal);
    setRendererAccountOwner('different-owner');
    resolve(value());
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('rejects another batch returned to an exact advance request', async () => {
    vi.mocked(httpRequest).mockResolvedValue(value());
    await expect(
      requestStudyBatch(new AbortController().signal, {
        action: 'advance',
        batch_id: '00000000-0000-4000-8000-000000000099',
        rows: [0],
      })
    ).rejects.toThrow('different operation');
  });
});
