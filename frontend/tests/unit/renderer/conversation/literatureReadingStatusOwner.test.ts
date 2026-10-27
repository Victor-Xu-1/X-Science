import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { LiteratureDetailUnavailableError } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatusCommand } from '@/common/adapter/literatureReadingStatus';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { loadLiteratureItem } from '@/renderer/services/runtime/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';
import { deferredPdf } from './pdfReadingFixtures';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.http,
}));
const operation = (n = 1) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const command = (n = 1): LiteratureReadingStatusCommand => ({
  kind: 'set-reading-status',
  operation_id: operation(n),
  item_ids: ['paper-b', 'paper-a', 'paper-b'],
  reading_status: 'read',
});
const receipt = (n = 1) => ({
  schema: 'x-science.literature-reading-status-receipt.v1',
  operation_id: operation(n),
  item_ids: ['paper-b', 'paper-a'],
  reading_status: 'read',
});

describe('reading status shares the original Library mutation owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('reading-owner');
    installLiteratureLocks();
    mocks.http.mockReset();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => {
        throw new Error('Unexpected real network');
      })
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it('restores a lost acknowledgement and retries the original ordered targets without another command', async () => {
    const storage = literaturePendingStorage();
    mocks.http
      .mockRejectedValueOnce(new BackendHttpError({ method: 'POST', path: '/reading-status', status: 503, body: {} }))
      .mockResolvedValueOnce(receipt());
    const owner = new LiteratureMutationController('reading-owner', { storage });
    await expect(owner.submit(command())).rejects.toMatchObject({ status: 503 });
    expect(owner.getSnapshot()).toMatchObject({ pending: command(), error: 'uncertain' });
    const resumed = new LiteratureMutationController('reading-owner', { storage });
    await expect(resumed.submit(command(2))).rejects.toThrow('original');
    await expect(resumed.submit({ ...command(), item_ids: ['paper-a', 'paper-b'] })).rejects.toThrow('original');
    await expect(resumed.retry()).resolves.toEqual(receipt());
    expect(mocks.http.mock.calls.map((call) => call.slice(0, 3))).toEqual(
      [0, 1].map(() => [
        'POST',
        '/api/literature/items/reading-status',
        { operation_id: operation(), item_ids: ['paper-b', 'paper-a', 'paper-b'], reading_status: 'read' },
      ])
    );
    expect(resumed.getSnapshot()).toMatchObject({ pending: null, error: null, receipt: receipt() });
    expect(storage.values.size).toBe(0);
  });

  it('coalesces the exact operation and freezes targets while blocking a competing publication write', async () => {
    const reply = deferredPdf<unknown>();
    mocks.http.mockReturnValueOnce(reply.promise);
    const owner = new LiteratureMutationController('reading-owner', { storage: literaturePendingStorage() });
    const targets = ['paper-b', 'paper-a', 'paper-b'];
    const pending = owner.submit({ ...command(), item_ids: targets });
    targets[0] = 'wrong';
    expect(owner.submit(command())).toBe(pending);
    await expect(
      owner.submit({
        kind: 'check-publication',
        operation_id: operation(2),
        item_id: 'paper-a',
        expected_metadata_revision: 1,
      })
    ).rejects.toThrow('original');
    await vi.waitFor(() => expect(mocks.http).toHaveBeenCalledTimes(1));
    expect(mocks.http.mock.calls[0][2].item_ids).toEqual(command().item_ids);
    reply.resolve(receipt());
    await expect(pending).resolves.toEqual(receipt());
    expect(mocks.http).toHaveBeenCalledTimes(1);
  });

  it('releases a definite rejection without auto-resending and permits an explicit next operation', async () => {
    const storage = literaturePendingStorage();
    mocks.http
      .mockRejectedValueOnce(new BackendHttpError({ method: 'POST', path: '/reading-status', status: 409, body: {} }))
      .mockResolvedValueOnce(receipt(2));
    const owner = new LiteratureMutationController('reading-owner', { storage });
    await expect(owner.submit(command())).rejects.toMatchObject({ status: 409 });
    expect(owner.getSnapshot()).toMatchObject({ pending: null, action: command(), error: 'rejected', mutating: false });
    expect(storage.values.size).toBe(0);
    await expect(owner.retry()).rejects.toThrow('No Library request');
    expect(mocks.http).toHaveBeenCalledTimes(1);
    await expect(owner.submit(command(2))).resolves.toEqual(receipt(2));
  });

  it('aborts on account change and never settles a late old-account receipt', async () => {
    const reply = deferredPdf<unknown>();
    mocks.http.mockReturnValueOnce(reply.promise);
    const storage = literaturePendingStorage();
    const owner = new LiteratureMutationController('reading-owner', { storage });
    const pending = owner.submit(command());
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(mocks.http).toHaveBeenCalledTimes(1));
    setRendererAccountOwner('other-owner');
    await rejected;
    expect(mocks.http.mock.calls[0][3].signal.aborted).toBe(true);
    reply.resolve(receipt());
    await Promise.resolve();
    expect(owner.getSnapshot().receipt).toBeUndefined();
    expect(storage.values.size).toBe(1);
    const other = new LiteratureMutationController('other-owner', { storage });
    expect(other.getSnapshot().pending).toBeNull();
    expect(mocks.http).toHaveBeenCalledTimes(1);
  });

  it('distinguishes invalid or deleted authoritative detail from a retryable transport failure', async () => {
    const item = {
      id: 'paper-a',
      title: 'Synthetic paper',
      metadata_revision: 1,
      metadata: {},
      deleted: false,
      project_ids: [],
      attachments: [],
    };
    for (const invalid of [
      { ...item, deleted: true },
      { ...item, id: 'other-paper' },
      { ...item, reading_status: null },
    ]) {
      mocks.http.mockResolvedValueOnce(invalid);
      await expect(loadLiteratureItem('paper-a')).rejects.toBeInstanceOf(LiteratureDetailUnavailableError);
    }
    const unavailable = new BackendHttpError({ method: 'GET', path: '/items/paper-a', status: 503, body: {} });
    mocks.http.mockRejectedValueOnce(unavailable);
    await expect(loadLiteratureItem('paper-a')).rejects.toBe(unavailable);
  });
});
