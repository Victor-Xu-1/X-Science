import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import {
  decodeLiteraturePublicationCheck,
  decodeLiteraturePublicationCommand,
  type LiteraturePublicationCommand,
  type LiteraturePublicationReceipt,
} from '@/common/adapter/literaturePublicationCheck';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  readLiteraturePublicationCheck,
  runLiteraturePublicationCommand,
} from '@/renderer/services/runtime/literaturePublicationCheck';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';
import { deferredPdf } from './pdfReadingFixtures';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.http,
}));
const operation = (n = 1) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const command = (n = 1): LiteraturePublicationCommand => ({
  kind: 'check-publication',
  operation_id: operation(n),
  item_id: 'paper',
  expected_metadata_revision: 2,
});
const result = () => ({
  schema: 'x-science.literature-publication-check.v1' as const,
  item_id: 'paper',
  metadata_revision: 2,
  doi: '10.1234/source',
  snapshot: {
    doi: '10.1234/source',
    checked_at: '2026-10-27T00:00:00Z',
    notices: [{ doi: '10.1234/notice', type: 'correction', source: 'crossref' as const }],
  },
});
const receipt = (n = 1): LiteraturePublicationReceipt => ({
  schema: 'x-science.literature-publication-receipt.v1',
  operation_id: operation(n),
  result: result(),
});

describe('publication checks use the existing Library original-operation owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    installLiteratureLocks();
    mocks.http.mockReset();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => {
        throw new Error('Unexpected network in publication protocol fixture');
      })
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('decodes closed evidence without coercing null or arrays into a successful check', () => {
    expect(decodeLiteraturePublicationCheck(result()).snapshot?.notices).toHaveLength(1);
    expect(
      decodeLiteraturePublicationCheck({
        schema: result().schema,
        item_id: 'paper',
        metadata_revision: 2,
        issue: 'doi-required',
      }).issue
    ).toBe('doi-required');
    for (const bad of [
      { ...result(), issue: ['unavailable'] },
      { ...result(), issue: null },
      { ...result(), doi: undefined },
      { ...result(), unexpected: true },
      { ...result(), metadata_revision: Number.MAX_SAFE_INTEGER + 1 },
      { ...result(), snapshot: { ...result().snapshot, checked_at: '2026-02-30T00:00:00Z' } },
      {
        ...result(),
        snapshot: { ...result().snapshot, notices: [{ ...result().snapshot.notices[0], source: ['publisher'] }] },
      },
      { ...result(), snapshot: { ...result().snapshot, notices: [{ ...result().snapshot.notices[0], date: null }] } },
      { ...result(), snapshot: { ...result().snapshot, doi: '10.1234/other' } },
    ])
      expect(() => decodeLiteraturePublicationCheck(bad)).toThrow();
    expect(() => decodeLiteraturePublicationCommand({ ...command(), doi: '10.1234/untrusted' })).toThrow();
  });

  it('sends only source-bound revision and original ID, and rejects mismatched acknowledgements', async () => {
    mocks.http.mockResolvedValueOnce(result()).mockResolvedValueOnce(receipt());
    const abort = new AbortController();
    await expect(readLiteraturePublicationCheck('paper', 2, abort.signal)).resolves.toEqual(result());
    expect(mocks.http.mock.calls[0]).toEqual([
      'GET',
      '/api/literature/items/paper/publication-check?expected_metadata_revision=2',
      undefined,
      { signal: abort.signal, timeoutMs: 15000 },
    ]);
    await expect(runLiteraturePublicationCommand(command(), abort.signal)).resolves.toEqual(receipt());
    expect(mocks.http.mock.calls[1]).toEqual([
      'POST',
      '/api/literature/items/paper/publication-check',
      { operation_id: operation(), expected_metadata_revision: 2 },
      { signal: abort.signal, timeoutMs: 30000 },
    ]);
    mocks.http.mockResolvedValueOnce({ ...receipt(), result: { ...result(), metadata_revision: 3 } });
    await expect(runLiteraturePublicationCommand(command())).rejects.toThrow('acknowledgement changed');
  });

  it('keeps a lost response in the one durable slot and retries the exact request after restoration', async () => {
    const storage = literaturePendingStorage();
    const run = vi
      .fn()
      .mockRejectedValueOnce(
        new BackendHttpError({ method: 'POST', path: '/publication-check', status: 503, body: {} })
      )
      .mockResolvedValueOnce(receipt());
    const first = new LiteratureMutationController('owner', { storage, runPublication: run });
    await expect(first.submit(command())).rejects.toMatchObject({ status: 503 });
    expect(first.getSnapshot()).toMatchObject({ pending: command(), action: command(), error: 'uncertain' });
    const resumed = new LiteratureMutationController('owner', { storage, runPublication: run });
    await expect(resumed.submit(command(2))).rejects.toThrow('original');
    await expect(resumed.retry()).resolves.toEqual(receipt());
    expect(run.mock.calls.map(([value]) => value)).toEqual([command(), command()]);
    expect(resumed.getSnapshot()).toMatchObject({ pending: null, error: null, receipt: receipt() });
    expect(storage.values.size).toBe(0);
  });

  it('settles known provider failure while retaining old evidence, then permits a new operation', async () => {
    const storage = literaturePendingStorage();
    const limited = { ...receipt(), result: { ...result(), issue: 'rate-limited' as const } };
    const run = vi.fn().mockResolvedValueOnce(limited).mockResolvedValueOnce(receipt(2));
    const owner = new LiteratureMutationController('owner', { storage, runPublication: run });
    await expect(owner.submit(command())).resolves.toEqual(limited);
    expect(owner.getSnapshot()).toMatchObject({ pending: null, error: null, receipt: limited });
    await expect(owner.submit(command(2))).resolves.toEqual(receipt(2));
    expect(run.mock.calls[1][0]).toEqual(command(2));
  });

  it('projects definite rejection after releasing the original slot', async () => {
    const run = vi
      .fn()
      .mockRejectedValue(new BackendHttpError({ method: 'POST', path: '/publication-check', status: 409, body: {} }));
    const owner = new LiteratureMutationController('owner', {
      storage: literaturePendingStorage(),
      runPublication: run,
    });
    await expect(owner.submit(command())).rejects.toMatchObject({ status: 409 });
    expect(owner.getSnapshot()).toMatchObject({ pending: null, action: command(), error: 'rejected', mutating: false });
  });

  it('aborts the original transport and ignores a late receipt after an account switch', async () => {
    const reply = deferredPdf<LiteraturePublicationReceipt>();
    const run = vi.fn((_command: LiteraturePublicationCommand, _signal?: AbortSignal) => reply.promise);
    const storage = literaturePendingStorage();
    const owner = new LiteratureMutationController('owner', { storage, runPublication: run });
    const pending = owner.submit(command());
    const outcome = expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    setRendererAccountOwner('other');
    await outcome;
    expect(run.mock.calls[0][1]?.aborted).toBe(true);
    reply.resolve(receipt());
    await Promise.resolve();
    expect(owner.getSnapshot().receipt).toBeUndefined();
    expect(storage.values.size).toBe(1);
  });
});
