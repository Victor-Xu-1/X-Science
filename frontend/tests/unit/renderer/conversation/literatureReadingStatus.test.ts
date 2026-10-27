import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  decodeLiteratureItem,
  literatureItemReadingStatus,
  type LiteratureItem,
} from '@/common/adapter/literatureLibrary';
import {
  decodeLiteratureReadingStatus,
  decodeLiteratureReadingStatusCommand,
  decodeLiteratureReadingStatusReceipt,
  literatureReadingStatuses,
  type LiteratureReadingStatusCommand,
} from '@/common/adapter/literatureReadingStatus';
import { runLiteratureReadingStatusCommand } from '@/renderer/services/runtime/literatureReadingStatus';
import { loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.http,
}));
const operationId = '00000000-0000-4000-8000-000000000001';
const legacyItem = () => ({
  id: 'paper-a',
  title: 'Synthetic reference',
  metadata_revision: 3,
  metadata: { authors: ['A. Author'] },
  deleted: false,
  project_ids: ['owned-project'],
  attachments: [],
});
const command = () =>
  decodeLiteratureReadingStatusCommand({
    kind: 'set-reading-status',
    operation_id: operationId,
    item_ids: ['paper-b', 'paper-a', 'paper-b'],
    reading_status: 'reading',
  });
const receipt = (input: LiteratureReadingStatusCommand) => ({
  schema: 'x-science.literature-reading-status-receipt.v1',
  operation_id: input.operation_id,
  item_ids: [...new Set(input.item_ids)],
  reading_status: input.reading_status,
});
const page = () => ({ schema: 'x-science.literature-page.v1', items: [{ ...legacyItem(), reading_status: 'read' }] });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('Reading-status protocol and typed transport', () => {
  const unexpectedFetch = vi.fn(() => {
    throw new Error('Unexpected real HTTP in a reading-status protocol test');
  });
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('reading-status-owner');
    mocks.http.mockReset();
    unexpectedFetch.mockClear();
    vi.stubGlobal('fetch', unexpectedFetch);
  });
  afterEach(() => {
    try {
      expect(unexpectedFetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('roundtrips legacy omission without inserting state and defaults only its presentation', () => {
    const original = legacyItem();
    const decoded = decodeLiteratureItem(original);
    const roundtrip = decodeLiteratureItem(JSON.parse(JSON.stringify(decoded)));
    expect(decoded).toEqual(original);
    expect(roundtrip).toEqual(original);
    expect(Object.hasOwn(decoded, 'reading_status')).toBe(false);
    expect(Object.hasOwn(roundtrip, 'reading_status')).toBe(false);
    expect(literatureItemReadingStatus(decoded)).toBe('unread');
    for (const value of literatureReadingStatuses) {
      const explicit = decodeLiteratureItem({ ...original, reading_status: value });
      expect(explicit.reading_status).toBe(value);
      expect(literatureItemReadingStatus(explicit)).toBe(value);
    }
    for (const value of [null, undefined, 'finished', 1]) {
      expect(() => decodeLiteratureItem({ ...original, reading_status: value })).toThrow();
      expect(() => decodeLiteratureReadingStatus(value)).toThrow();
    }
    expect(() => literatureItemReadingStatus({ reading_status: null } as unknown as LiteratureItem)).toThrow();
  });

  it('freezes the original command and preserves duplicates and order before the 200-target execution limit', () => {
    const ids = ['paper-b', 'paper-a', 'paper-b'];
    const input = { kind: 'set-reading-status', operation_id: operationId, item_ids: ids, reading_status: 'reading' };
    const decoded = decodeLiteratureReadingStatusCommand(input);
    expect(Object.isFrozen(decoded)).toBe(true);
    expect(Object.isFrozen(decoded.item_ids)).toBe(true);
    ids[0] = 'changed';
    ids.push('late-caller-target');
    expect(decoded.item_ids).toEqual(['paper-b', 'paper-a', 'paper-b']);
    expect(Reflect.set(decoded, 'reading_status', 'read')).toBe(false);
    expect(() => (decoded.item_ids as string[]).push('extra')).toThrow(TypeError);
    expect(
      decodeLiteratureReadingStatusCommand({ ...input, item_ids: Array(200).fill('same-paper') }).item_ids
    ).toHaveLength(200);
    for (const item_ids of [[], Array(201).fill('same-paper')])
      expect(() => decodeLiteratureReadingStatusCommand({ ...input, item_ids })).toThrow();
    expect(() => decodeLiteratureReadingStatusCommand({ ...input, metadata: {} })).toThrow();
    expect(() => decodeLiteratureReadingStatusCommand({ ...input, reading_status: null })).toThrow();
  });

  it('binds the ACK to the original UUID, status and first-occurrence target order', () => {
    const input = command();
    const acknowledged = receipt(input);
    const decoded = decodeLiteratureReadingStatusReceipt(acknowledged, input);
    expect(decoded.item_ids).toEqual(['paper-b', 'paper-a']);
    expect(Object.isFrozen(decoded)).toBe(true);
    expect(Object.isFrozen(decoded.item_ids)).toBe(true);
    for (const altered of [
      { operation_id: '00000000-0000-4000-8000-000000000002' },
      { reading_status: 'read' },
      { item_ids: ['paper-a', 'paper-b'] },
      { item_ids: ['paper-b', 'foreign-paper'] },
      { item_ids: ['paper-b', 'paper-a', 'paper-b'] },
      { item_ids: ['paper-b'] },
      { owner: 'untrusted' },
    ])
      expect(() => decodeLiteratureReadingStatusReceipt({ ...acknowledged, ...altered }, input)).toThrow();
  });

  it('uses the exact typed POST and scoped GET while discarding both late responses after an account change', async () => {
    const input = command();
    const abort = new AbortController();
    mocks.http.mockResolvedValueOnce(receipt(input));
    await expect(runLiteratureReadingStatusCommand(input, abort.signal)).resolves.toEqual(receipt(input));
    expect(mocks.http.mock.calls[0]).toEqual([
      'POST',
      '/api/literature/items/reading-status',
      { operation_id: input.operation_id, item_ids: ['paper-b', 'paper-a', 'paper-b'], reading_status: 'reading' },
      { signal: abort.signal, timeoutMs: 30_000 },
    ]);
    mocks.http.mockResolvedValueOnce(page());
    await expect(
      loadLiteraturePage({
        query: '  q & r  ',
        projectId: 'owned-project',
        collectionId: 'owned-collection',
        readingStatus: 'read',
        cursor: 'next-cursor',
        signal: abort.signal,
      })
    ).resolves.toMatchObject({ items: [{ reading_status: 'read' }] });
    const url = new URL(mocks.http.mock.calls[1][1], 'https://fixture.invalid');
    expect(url.pathname).toBe('/api/literature/items');
    expect(Object.fromEntries(url.searchParams)).toEqual({
      limit: '20',
      query: 'q & r',
      project_id: 'owned-project',
      collection_id: 'owned-collection',
      reading_status: 'read',
      cursor: 'next-cursor',
    });
    expect(mocks.http.mock.calls[1][0]).toBe('GET');
    expect(mocks.http.mock.calls[1][3]).toEqual({ signal: abort.signal, timeoutMs: 15_000 });
    abort.abort();
    expect(mocks.http.mock.calls[0][3].signal.aborted).toBe(true);
    const latePost = deferred<unknown>();
    const latePage = deferred<unknown>();
    mocks.http.mockReturnValueOnce(latePost.promise).mockReturnValueOnce(latePage.promise);
    const post = runLiteratureReadingStatusCommand(input);
    const get = loadLiteraturePage({ readingStatus: 'reading' });
    const postRejected = expect(post).rejects.toThrow('account changed');
    const getRejected = expect(get).rejects.toThrow('account changed');
    setRendererAccountOwner('next-owner');
    latePost.resolve(receipt(input));
    latePage.resolve(page());
    await Promise.all([postRejected, getRejected]);
    expect(mocks.http).toHaveBeenCalledTimes(4);
  });
});
