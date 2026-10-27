import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: mocks.http }));
import { loadCompleteLiteratureSelection } from '@/renderer/services/runtime/literatureSelection';

describe('Complete membership uses the existing authenticated Library read', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('selection-owner');
    vi.resetAllMocks();
  });
  afterEach(() => vi.restoreAllMocks());
  it('uses the same filters and complete-selection flag, without cursor or page limit', async () => {
    mocks.http.mockResolvedValue({ schema: 'x-science.literature-selection.v1', item_ids: ['a'], total_count: 1 });
    const controller = new AbortController();
    expect(
      (
        await loadCompleteLiteratureSelection(
          { query: ' study ', projectId: 'p', collectionId: 'c', readingStatus: 'reading' },
          controller.signal
        )
      ).item_ids
    ).toEqual(['a']);
    const [method, path, body, options] = mocks.http.mock.calls[0];
    expect(method).toBe('GET');
    expect(body).toBeUndefined();
    const query = new URL(path, 'http://localhost').searchParams;
    expect(Object.fromEntries(query)).toEqual({
      all_item_ids: 'true',
      query: 'study',
      project_id: 'p',
      collection_id: 'c',
      reading_status: 'reading',
    });
    expect(options.signal).toBe(controller.signal);
  });
  it('fences pre-cancelled requests and late responses from the previous account', async () => {
    const aborted = new AbortController();
    aborted.abort();
    await expect(loadCompleteLiteratureSelection({}, aborted.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(mocks.http).not.toHaveBeenCalled();
    let resolve!: (value: unknown) => void;
    mocks.http.mockReturnValue(
      new Promise((done) => {
        resolve = done;
      })
    );
    const pending = loadCompleteLiteratureSelection({}, new AbortController().signal);
    setRendererAccountOwner('another-owner');
    resolve({ schema: 'x-science.literature-selection.v1', item_ids: ['old'], total_count: 1 });
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
  });
});
