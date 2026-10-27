import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureItem, LiteraturePage } from '@/common/adapter/literatureLibrary';
import { useLibraryWorkbench } from '@/renderer/pages/library/useLibraryWorkbench';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const mocks = vi.hoisted(() => ({
  page: vi.fn(),
  item: vi.fn(),
  collections: vi.fn(),
  memberships: vi.fn(),
  projects: vi.fn(),
}));
vi.mock('@/renderer/services/runtime/literatureLibrary', () => ({
  loadLiteraturePage: mocks.page,
  loadLiteratureItem: mocks.item,
}));
vi.mock('@/renderer/services/runtime/literatureCollections', () => ({
  loadLiteratureCollections: mocks.collections,
  loadLiteratureItemCollections: mocks.memberships,
}));
vi.mock('@/renderer/services/xScienceGateway', () => ({ loadXScienceProjects: mocks.projects }));

const item = (id = 'paper-a', reading_status: LiteratureItem['reading_status'] = 'unread'): LiteratureItem => ({
  id,
  title: `Synthetic ${id}`,
  metadata_revision: 1,
  reading_status,
  metadata: {},
  deleted: false,
  project_ids: [],
  attachments: [],
});
const page = (): LiteraturePage => ({ schema: 'x-science.literature-page.v1', items: [item()] });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function startReads() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}
async function deliver<T>(pending: ReturnType<typeof deferred<T>>, value: T) {
  await act(async () => {
    pending.resolve(value);
    await pending.promise;
  });
}

describe('Library public hook quiet data refresh', () => {
  const unexpectedFetch = vi.fn(() => {
    throw new Error('Unexpected real HTTP in the quiet-refresh hook test');
  });
  beforeEach(() => {
    vi.useFakeTimers();
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('quiet-owner');
    mocks.page.mockReset().mockImplementation(async () => page());
    mocks.item.mockReset().mockImplementation(async (id: string) => item(id));
    mocks.collections.mockReset().mockResolvedValue({ schema: 'x-science.literature-collections.v1', collections: [] });
    mocks.memberships.mockReset().mockImplementation(async (id: string) => ({ item_id: id, collection_ids: [] }));
    mocks.projects.mockReset().mockResolvedValue({ projects: [] });
    unexpectedFetch.mockClear();
    vi.stubGlobal('fetch', unexpectedFetch);
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    try {
      expect(unexpectedFetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('keeps the ready detail during refreshData and updates its reading status without reloading unrelated reads', async () => {
    const { result } = renderHook(() => useLibraryWorkbench('', '', 'paper-a'));
    await startReads();
    const original = result.current.detail.value;
    expect(result.current.detail).toMatchObject({ status: 'ready', value: { reading_status: 'unread' } });
    const pending = deferred<LiteratureItem>();
    mocks.item.mockReturnValueOnce(pending.promise);
    act(() => result.current.refreshData());
    expect(result.current.detail).toMatchObject({ status: 'ready', refreshing: true, refreshError: false });
    expect(result.current.detail.value).toBe(original);
    await startReads();
    expect(mocks.page).toHaveBeenCalledTimes(2);
    expect(mocks.item).toHaveBeenCalledTimes(2);
    for (const read of [mocks.projects, mocks.collections, mocks.memberships]) expect(read).toHaveBeenCalledTimes(1);
    expect(result.current.detail.value).toBe(original);
    const updated = item('paper-a', 'read');
    await deliver(pending, updated);
    expect(result.current.detail).toMatchObject({ status: 'ready', value: { reading_status: 'read' } });
    expect(result.current.detail.value).toBe(updated);
    expect(result.current.detail.refreshing).not.toBe(true);
    expect(result.current.detail.refreshError).not.toBe(true);
    for (const read of [mocks.projects, mocks.collections, mocks.memberships]) expect(read).toHaveBeenCalledTimes(1);
  });

  it('discards old quiet replies and clears retained values across item and account changes', async () => {
    const { result, rerender } = renderHook(({ selected }) => useLibraryWorkbench('', '', selected), {
      initialProps: { selected: 'paper-a' },
    });
    await startReads();
    const oldItem = deferred<LiteratureItem>();
    mocks.item.mockReturnValueOnce(oldItem.promise);
    act(() => result.current.refreshData());
    await startReads();
    const oldItemSignal = mocks.item.mock.calls.at(-1)![1] as AbortSignal;
    rerender({ selected: 'paper-b' });
    expect(result.current.detail.status).toBe('loading');
    expect(result.current.detail.value).toBeUndefined();
    expect(oldItemSignal.aborted).toBe(true);
    await startReads();
    const currentB = result.current.detail.value;
    expect(currentB?.id).toBe('paper-b');
    await deliver(oldItem, { ...item('paper-a', 'read'), title: 'Late item A' });
    expect(result.current.detail.value).toBe(currentB);

    const oldAccount = deferred<LiteratureItem>();
    mocks.item.mockReturnValueOnce(oldAccount.promise);
    act(() => result.current.refreshData());
    await startReads();
    const oldAccountSignal = mocks.item.mock.calls.at(-1)![1] as AbortSignal;
    const priorAccount = result.current.account;
    const nextOwner = { ...item('paper-b', 'reading'), title: 'Next owner detail' };
    mocks.item.mockResolvedValueOnce(nextOwner);
    act(() => {
      setRendererAccountOwner('next-quiet-owner');
    });
    expect(result.current.account).not.toBe(priorAccount);
    expect(result.current.detail.status).toBe('loading');
    expect(result.current.detail.value).toBeUndefined();
    expect(oldAccountSignal.aborted).toBe(true);
    await startReads();
    expect(result.current.detail.value).toBe(nextOwner);
    await deliver(oldAccount, { ...item('paper-b', 'read'), title: 'Late previous owner' });
    expect(result.current.detail.value).toBe(nextOwner);
  });

  it('retains the old detail for transient refresh errors but clears it on authoritative HTTP403 and HTTP404', async () => {
    const { result } = renderHook(() => useLibraryWorkbench('', '', 'paper-a'));
    await startReads();
    const original = result.current.detail.value;
    mocks.item.mockRejectedValueOnce(new TypeError('Controlled transient read failure'));
    act(() => result.current.refreshData());
    await startReads();
    expect(result.current.detail).toMatchObject({ status: 'ready', refreshing: false, refreshError: true });
    expect(result.current.detail.value).toBe(original);

    for (const status of [403, 404]) {
      expect(result.current.detail.status).toBe('ready');
      mocks.item.mockRejectedValueOnce(
        new BackendHttpError({ method: 'GET', path: '/api/literature/items/paper-a', status, body: {} })
      );
      act(() => result.current.refreshData());
      expect(result.current.detail.refreshing).toBe(true);
      await startReads();
      expect(result.current.detail.status).toBe('error');
      expect(result.current.detail.value).toBeUndefined();
      if (status === 403) {
        mocks.item.mockResolvedValueOnce(item('paper-a', 'read'));
        act(() => result.current.refreshData());
        await startReads();
        expect(result.current.detail).toMatchObject({ status: 'ready', value: { reading_status: 'read' } });
      }
    }
  });
});
