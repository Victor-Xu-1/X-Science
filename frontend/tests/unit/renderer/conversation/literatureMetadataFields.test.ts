import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  decodeLiteratureCommand,
  type LiteratureCommand,
  type LiteratureReceipt,
} from '@/common/adapter/literatureLibrary';
import { runLiteratureCommand } from '@/renderer/services/runtime/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';

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
const operationId = '00000000-0000-4000-8000-000000000023';
const pendingKey = 'x-science.literature-mutation.v1:owner';
const command = (): Extract<LiteratureCommand, { kind: 'edit-metadata' }> => ({
  kind: 'edit-metadata',
  operation_id: operationId,
  item_id: 'paper',
  expected_revision: 7,
  metadata_patch: { set: { authors: 'Edited author', nullable: null }, remove: ['obsolete'] },
});
const receipt = (
  metadata: Record<string, unknown> = { authors: 'Edited author', nullable: null, opaque: Infinity }
): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1',
  operation_id: operationId,
  item: {
    id: 'paper',
    title: 'Original title',
    metadata_revision: 8,
    metadata,
    deleted: false,
    project_ids: [],
    attachments: [],
  },
});

describe('Library field metadata command contract', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    mocks.http.mockReset();
    expect(mocks.unexpectedFetch).not.toHaveBeenCalled();
    mocks.unexpectedFetch.mockClear();
    vi.stubGlobal('fetch', mocks.unexpectedFetch);
    installLiteratureLocks();
  });
  afterEach(() => {
    try {
      expect(mocks.unexpectedFetch).not.toHaveBeenCalled();
    } finally {
      vi.restoreAllMocks();
      vi.unstubAllGlobals();
    }
  });

  it('snapshots changed JSON fields while keeping null removal and own proto keys distinct', () => {
    const patch = JSON.parse(
      '{"set":{"__proto__":{"validated":true},"nullable":null,"nested":{"values":[1,true,"x",null]}},"remove":["obsolete"]}'
    ) as Record<string, unknown>;
    const input = { ...command(), metadata_patch: patch };
    const decoded = decodeLiteratureCommand(input);
    expect(decoded.kind).toBe('edit-metadata');
    if (decoded.kind !== 'edit-metadata') throw new Error('Unexpected command kind');
    const set = decoded.metadata_patch.set!;
    expect(Object.hasOwn(set, '__proto__')).toBe(true);
    expect(Object.getPrototypeOf(set)).toBe(Object.prototype);
    expect(set.__proto__).toEqual({ validated: true });
    expect(Object.hasOwn(set, 'nullable')).toBe(true);
    expect(set.nullable).toBeNull();
    expect(decoded.metadata_patch.remove).toEqual(['obsolete']);
    ((patch.set as Record<string, unknown>).nested as { values: unknown[] }).values[0] = 99;
    (patch.remove as string[]).push('changed-after-submit');
    expect(set.nested).toEqual({ values: [1, true, 'x', null] });
    expect(decoded.metadata_patch.remove).toEqual(['obsolete']);
    expect(Object.hasOwn({}, 'validated')).toBe(false);
  });

  it('rejects invalid operators presence and actual values before a request can be rewritten', async () => {
    const sparse = Array<string>(1);
    let tooDeep: unknown = 'value';
    for (let depth = 0; depth < 16; depth++) tooDeep = { nested: tooDeep };
    const invalidPatches: unknown[] = [
      null,
      undefined,
      {},
      { set: null },
      { remove: null },
      { set: {} },
      { remove: [] },
      { set: { a: 1 }, remove: ['a'] },
      { remove: ['a', 'a'] },
      { remove: [null] },
      { remove: sparse },
      { unknown: true, set: { a: 1 } },
      { set: { a: Infinity } },
      { set: { a: NaN } },
      { set: { a: undefined } },
      { set: { a: () => 1 } },
      { set: { a: new Date(0) } },
      { set: { a: Array(1) } },
      { set: { a: tooDeep } },
    ];
    for (const metadata_patch of invalidPatches)
      await expect(runLiteratureCommand({ ...command(), metadata_patch } as LiteratureCommand)).rejects.toThrow(
        'Invalid literature'
      );
    for (const forbidden of [
      { metadata: {} },
      { metadata: undefined },
      { deleted: false },
      { deleted: undefined },
      { extra: true },
    ])
      expect(() => decodeLiteratureCommand({ ...command(), ...forbidden })).toThrow('Invalid literature');
    expect(() => decodeLiteratureCommand({ ...command(), kind: 'edit', title: 'Wrong downgrade' })).toThrow(
      'Invalid literature'
    );
    expect(mocks.http).not.toHaveBeenCalled();
  });

  it('uses the original PATCH route and checks only requested own fields despite untouched nonfinite metadata', async () => {
    const protoSet = JSON.parse('{"__proto__":{"mark":true},"nullable":null}');
    const input = { ...command(), title: '  Updated title  ', metadata_patch: { set: protoSet, remove: ['obsolete'] } };
    const metadata = JSON.parse(
      '{"__proto__":{"mark":true},"nullable":null,"untouched":{"integer":9007199254740993,"fraction":0.12345678901234567890123456789,"exponent":1e400}}'
    );
    mocks.http.mockResolvedValue({ ...receipt(metadata), item: { ...receipt(metadata).item, title: 'Updated title' } });
    await expect(runLiteratureCommand(input)).resolves.toMatchObject({ item: { title: 'Updated title' } });
    expect(mocks.http.mock.calls[0].slice(0, 3)).toEqual([
      'PATCH',
      '/api/literature/items/paper',
      {
        operation_id: operationId,
        expected_revision: 7,
        title: 'Updated title',
        metadata_patch: { set: protoSet, remove: ['obsolete'] },
      },
    ]);
    expect(Object.hasOwn(mocks.http.mock.calls[0][2], 'metadata')).toBe(false);
    expect(metadata.untouched.exponent).toBe(Infinity);
    expect(Object.hasOwn(metadata, '__proto__')).toBe(true);
    expect(Object.getPrototypeOf(metadata)).toBe(Object.prototype);
    // Inherited Object.prototype.__proto__ is not an own metadata field.
    mocks.http.mockResolvedValue(receipt({ untouched: Infinity }));
    await expect(
      runLiteratureCommand({ ...command(), metadata_patch: { remove: ['__proto__'] } })
    ).resolves.toBeDefined();
    mocks.http.mockResolvedValue(receipt(JSON.parse('{"__proto__":null,"untouched":1e400}')));
    await expect(runLiteratureCommand({ ...command(), metadata_patch: { remove: ['__proto__'] } })).rejects.toThrow(
      'metadata edit not acknowledged'
    );
  });

  it('rejects missing or inherited changed fields retained removals and mismatched revision or title', async () => {
    const inherited = Object.create({ authors: 'Edited author' }) as Record<string, unknown>;
    inherited.nullable = null;
    const expected = (metadata?: Record<string, unknown>) => {
      const value = receipt(metadata);
      return { ...value, item: { ...value.item, title: 'Expected title' } };
    };
    const invalid = [
      expected({ nullable: null, opaque: Infinity }),
      expected(inherited),
      expected({ authors: 'Wrong author', nullable: null }),
      expected({ authors: 'Edited author', nullable: null, obsolete: null }),
      { ...expected(), item: { ...expected().item, metadata_revision: 6 } },
      { ...expected(), item: { ...expected().item, metadata_revision: 7 } },
      { ...expected(), item: { ...expected().item, metadata_revision: 9 } },
      { ...expected(), item: { ...expected().item, title: 'Wrong title' } },
    ];
    for (const value of invalid) {
      mocks.http.mockResolvedValue(value);
      await expect(runLiteratureCommand({ ...command(), title: 'Expected title' })).rejects.toThrow(
        'metadata edit not acknowledged'
      );
    }
    mocks.http.mockResolvedValue(receipt({ authors: 'Edited author' }));
    await expect(runLiteratureCommand(command())).rejects.toThrow('metadata edit not acknowledged');
  });

  it('retains the exact field operation in the original slot across uncertainty input mutation and retry', async () => {
    const storage = literaturePendingStorage();
    const locks = installLiteratureLocks();
    const input = command();
    const original = structuredClone(input);
    mocks.http.mockRejectedValueOnce(new TypeError('lost field receipt')).mockResolvedValueOnce(receipt());
    const first = new LiteratureMutationController('owner', { storage });
    const pending = first.submit(input);
    input.metadata_patch.set!.authors = 'Changed after submit';
    input.metadata_patch.remove!.push('unrelated');
    await expect(pending).rejects.toThrow('lost field receipt');
    const raw = storage.getItem(pendingKey)!;
    expect(JSON.parse(raw)).toEqual({ ownerId: 'owner', command: original });
    expect(first.getSnapshot()).toMatchObject({ pending: original, error: 'uncertain', mutating: false });
    const resumed = new LiteratureMutationController('owner', { storage });
    await resumed.retry();
    expect(mocks.http.mock.calls.map((call) => call.slice(0, 3))).toEqual([
      [
        'PATCH',
        '/api/literature/items/paper',
        { operation_id: operationId, expected_revision: 7, metadata_patch: original.metadata_patch },
      ],
      [
        'PATCH',
        '/api/literature/items/paper',
        { operation_id: operationId, expected_revision: 7, metadata_patch: original.metadata_patch },
      ],
    ]);
    expect(locks.mock.calls.every(([key]) => key === pendingKey)).toBe(true);
    expect(storage.values.size).toBe(0);
    expect(resumed.getSnapshot()).toMatchObject({ pending: null, error: null });
  });

  it('refuses changed-payload or old-kind aliases without replacing the original pending field operation', async () => {
    const storage = literaturePendingStorage();
    mocks.http.mockRejectedValue(new TypeError('unknown outcome'));
    const first = new LiteratureMutationController('owner', { storage });
    await expect(first.submit(command())).rejects.toThrow('unknown outcome');
    const before = storage.getItem(pendingKey);
    const resumed = new LiteratureMutationController('owner', { storage });
    await expect(
      resumed.submit({ ...command(), metadata_patch: { set: { authors: 'Different payload' } } })
    ).rejects.toThrow('original Library');
    expect(() => decodeLiteratureCommand({ ...command(), kind: 'edit', title: 'Wrong downgrade' })).toThrow(
      'Invalid literature'
    );
    expect(storage.getItem(pendingKey)).toBe(before);
    expect(mocks.http).toHaveBeenCalledTimes(1);
    expect(JSON.parse(before!).command.kind).toBe('edit-metadata');
  });
});
