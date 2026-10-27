import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  decodeLiteratureCollectionCommand,
  decodeLiteratureCollectionPage,
  decodeLiteratureCollectionReceipt,
  normalizeLiteratureCollectionText,
  type LiteratureCollection,
  type LiteratureCollectionCommand,
} from '@/common/adapter/literatureCollections';
import {
  loadLiteratureCollections,
  loadLiteratureItemCollections,
  runLiteratureCollectionCommand,
} from '@/renderer/services/runtime/literatureCollections';
import { loadLiteraturePage } from '@/renderer/services/runtime/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { deferredPdf } from './pdfReadingFixtures';
import { installLiteratureLocks } from './literatureMutationLockFixtures';

const mocks = vi.hoisted(() => {
  const unexpectedFetch = vi.fn(() => {
    throw new Error('Unexpected unmocked HTTP in a Library contract test');
  });
  vi.stubGlobal('fetch', unexpectedFetch);
  return { http: vi.fn(), unexpectedFetch };
});
vi.mock('@/common/adapter/httpBridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.http,
  isBackendHttpError: () => false,
}));
const operation = '00000000-0000-4000-8000-000000000084';
const collection = (patch: Partial<LiteratureCollection> = {}): LiteratureCollection => ({
  id: 'collection',
  revision: 1,
  name: 'Research',
  description: '',
  parent_id: null,
  sort_order: 0,
  item_count: 0,
  ...patch,
});
const create = (): LiteratureCollectionCommand => ({
  kind: 'collection-create',
  operation_id: operation,
  name: 'Research',
});
const created = () => ({
  schema: 'x-science.literature-collection-receipt.v1' as const,
  operation_id: operation,
  action: 'create' as const,
  collection_id: 'collection',
  collection: collection(),
});
const storage = () => {
  const values = new Map<string, string>();
  return {
    values,
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
};

describe('ordinary collections share the Library authority', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    installLiteratureLocks();
    mocks.http.mockReset();
    expect(mocks.unexpectedFetch).not.toHaveBeenCalled();
    mocks.unexpectedFetch.mockClear();
    vi.stubGlobal('fetch', mocks.unexpectedFetch);
  });
  afterEach(() => {
    try {
      expect(mocks.unexpectedFetch).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('normalizes the reference name grammar and counts UTF-16 units after normalization', () => {
    expect(normalizeLiteratureCollectionText('  Ｒesearch\t  e\u0301\n')).toBe('Research é');
    expect(normalizeLiteratureCollectionText('文'.repeat(200))).toHaveLength(200);
    expect(normalizeLiteratureCollectionText('😀'.repeat(100))).toHaveLength(200);
    expect(() => normalizeLiteratureCollectionText('😀'.repeat(101))).toThrow();
    expect(() => normalizeLiteratureCollectionText('\uD800')).toThrow();
    expect(() => normalizeLiteratureCollectionText(' '.repeat(20))).toThrow();
    expect(() => normalizeLiteratureCollectionText('文'.repeat(1001), true)).toThrow();
    expect(decodeLiteratureCollectionCommand({ ...create(), name: ' Ｒesearch ' })).toMatchObject({
      name: 'Research',
      description: '',
    });
    expect(() => decodeLiteratureCollectionCommand({ ...create(), unexpected: true })).toThrow();
  });

  it('rejects duplicate, orphaned or cyclic collection trees instead of displaying a false empty tree', () => {
    const page = (collections: unknown[]) => ({ schema: 'x-science.literature-collections.v1', collections });
    expect(
      decodeLiteratureCollectionPage(page([collection(), collection({ id: 'child', parent_id: 'collection' })]))
        .collections
    ).toHaveLength(2);
    expect(() => decodeLiteratureCollectionPage(page([collection(), collection()]))).toThrow();
    expect(() => decodeLiteratureCollectionPage(page([collection({ parent_id: 'missing' })]))).toThrow();
    expect(() =>
      decodeLiteratureCollectionPage(
        page([collection({ parent_id: 'child' }), collection({ id: 'child', parent_id: 'collection' })])
      )
    ).toThrow();
    expect(() => decodeLiteratureCollectionPage(page([collection({ item_count: -1 })]))).toThrow();
  });

  it('requires real command-bound collection acknowledgements and never substitutes an item receipt', () => {
    expect(decodeLiteratureCollectionReceipt(created(), create())).toMatchObject({ collection: { id: 'collection' } });
    expect(() => decodeLiteratureCollectionReceipt({ ...created(), operation_id: 'other' }, create())).toThrow();
    expect(() =>
      decodeLiteratureCollectionReceipt({ ...created(), collection: undefined, item: { id: 'collection' } }, create())
    ).toThrow();
    const edit = {
      kind: 'collection-update' as const,
      operation_id: operation,
      collection_id: 'collection',
      expected_revision: 1,
      name: 'Edited',
    };
    expect(() =>
      decodeLiteratureCollectionReceipt(
        { ...created(), action: 'update', collection: collection({ name: 'Edited', revision: 3 }) },
        edit
      )
    ).toThrow();
    expect(() => decodeLiteratureCollectionCommand({ ...edit, parent_id: 'other' })).toThrow();
    const member = {
      kind: 'collection-membership' as const,
      operation_id: operation,
      collection_id: 'collection',
      item_id: 'paper',
      included: true,
    };
    expect(() =>
      decodeLiteratureCollectionReceipt(
        { ...created(), action: 'membership', item_id: 'other', included: true },
        member
      )
    ).toThrow();
  });

  it('routes collection reads, membership, deletion and live item filtering through their exact coordinates', async () => {
    mocks.http.mockResolvedValueOnce({ schema: 'x-science.literature-collections.v1', collections: [collection()] });
    await expect(loadLiteratureCollections()).resolves.toMatchObject({ collections: [{ id: 'collection' }] });
    mocks.http.mockResolvedValueOnce({
      schema: 'x-science.literature-collection-membership.v1',
      item_id: 'paper',
      collection_ids: ['collection'],
    });
    await expect(loadLiteratureItemCollections('paper')).resolves.toMatchObject({ collection_ids: ['collection'] });
    const member = {
      kind: 'collection-membership' as const,
      operation_id: operation,
      collection_id: 'collection',
      item_id: 'paper',
      included: true,
    };
    mocks.http.mockResolvedValueOnce({
      schema: 'x-science.literature-collection-receipt.v1',
      action: 'membership',
      operation_id: operation,
      collection_id: 'collection',
      item_id: 'paper',
      included: true,
    });
    await runLiteratureCollectionCommand(member);
    expect(mocks.http.mock.calls[2].slice(0, 3)).toEqual([
      'PUT',
      '/api/literature/collections/collection/items/paper',
      { operation_id: operation, included: true },
    ]);
    mocks.http.mockResolvedValueOnce({
      schema: 'x-science.literature-collection-receipt.v1',
      action: 'delete',
      operation_id: operation,
      collection_id: 'collection',
    });
    await runLiteratureCollectionCommand({
      kind: 'collection-delete',
      operation_id: operation,
      collection_id: 'collection',
    });
    expect(mocks.http.mock.calls[3].slice(0, 3)).toEqual([
      'DELETE',
      '/api/literature/collections/collection',
      { operation_id: operation },
    ]);
    mocks.http.mockResolvedValueOnce({ schema: 'x-science.literature-page.v1', items: [] });
    await loadLiteraturePage({ collectionId: 'collection', cursor: 'bound' });
    expect(mocks.http.mock.calls[4][1]).toBe('/api/literature/items?limit=20&collection_id=collection&cursor=bound');
  });

  it('rejects late collection reads and membership for a different item or account', async () => {
    mocks.http.mockResolvedValueOnce({
      schema: 'x-science.literature-collection-membership.v1',
      item_id: 'other',
      collection_ids: [],
    });
    await expect(loadLiteratureItemCollections('paper')).rejects.toThrow();
    const pending = deferredPdf<unknown>();
    mocks.http.mockReturnValueOnce(pending.promise);
    const read = loadLiteratureCollections();
    setRendererAccountOwner('other');
    pending.resolve({ schema: 'x-science.literature-collections.v1', collections: [collection()] });
    await expect(read).rejects.toThrow('account changed');
  });

  it('restores the original lost collection request and retries it through the same durable slot', async () => {
    const saved = storage();
    const runCollection = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('lost acknowledgement'))
      .mockResolvedValueOnce(created());
    const first = new LiteratureMutationController('owner', { storage: saved, runCollection });
    await expect(first.submit(create())).rejects.toThrow('lost acknowledgement');
    expect(first.getSnapshot()).toMatchObject({
      error: 'uncertain',
      pending: { kind: 'collection-create', operation_id: operation },
    });
    const resumed = new LiteratureMutationController('owner', { storage: saved, runCollection });
    await expect(resumed.retry()).resolves.toMatchObject({ action: 'create' });
    expect(runCollection.mock.calls.map((call) => call[0])).toEqual([
      decodeLiteratureCollectionCommand(create()),
      decodeLiteratureCollectionCommand(create()),
    ]);
    expect(saved.values.size).toBe(0);
  });

  it('prevents an item mutation from competing with an in-flight collection request in another renderer', async () => {
    const saved = storage();
    const pending = deferredPdf<ReturnType<typeof created>>();
    const runCollection = vi.fn().mockReturnValue(pending.promise);
    const run = vi.fn();
    const first = new LiteratureMutationController('owner', { storage: saved, runCollection, run });
    const second = new LiteratureMutationController('owner', { storage: saved, runCollection, run });
    const accepted = first.submit(create());
    await vi.waitFor(() => expect(runCollection).toHaveBeenCalledTimes(1));
    await expect(
      second.submit({
        kind: 'edit',
        operation_id: '00000000-0000-4000-8000-000000000085',
        item_id: 'paper',
        expected_revision: 1,
        title: 'Edited',
      })
    ).rejects.toThrow('original Library change');
    expect(run).not.toHaveBeenCalled();
    pending.resolve(created());
    await accepted;
    expect(saved.values.size).toBe(0);
  });

  it('preserves the original pending request when an old-account acknowledgement arrives', async () => {
    const saved = storage();
    const pending = deferredPdf<ReturnType<typeof created>>();
    const runCollection = vi.fn().mockReturnValue(pending.promise);
    const first = new LiteratureMutationController('owner', { storage: saved, runCollection });
    const request = first.submit(create());
    await vi.waitFor(() => expect(runCollection).toHaveBeenCalledTimes(1));
    setRendererAccountOwner('other');
    pending.resolve(created());
    await expect(request).rejects.toBeInstanceOf(DOMException);
    await expect(request).rejects.toMatchObject({ name: 'AbortError' });
    expect(saved.values.size).toBe(1);
    const other = new LiteratureMutationController('other', { storage: saved, runCollection });
    expect(other.getSnapshot().pending).toBeNull();
    expect(other.getSnapshot().receipt).toBeUndefined();
  });
});
