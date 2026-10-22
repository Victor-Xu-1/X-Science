import { beforeEach, describe, expect, it, vi } from 'vitest';
import { decodeLiteratureItem, type LiteratureCommand } from '@/common/adapter/literatureLibrary';
import {
  loadLibraryReadingOptions,
  loadLiteraturePage,
  runLiteratureCommand,
} from '@/renderer/services/runtime/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { deferredPdf, pdfContext } from './pdfReadingFixtures';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: mocks.http, isBackendHttpError: () => false }));
const operationId = '00000000-0000-4000-8000-000000000001';
const item = () => ({
  id: 'paper',
  title: 'Independent paper',
  metadata_revision: 1,
  metadata: { authors: 'A. Researcher' },
  deleted: false,
  project_ids: ['project'],
  attachments: [
    {
      id: 'attachment',
      kind: 'fullText',
      versions: [
        {
          id: 'litv_original',
          attachment_id: 'attachment',
          version_number: 1,
          filename: 'paper.pdf',
          mime_type: 'application/pdf',
          size_bytes: 512,
          checksum: 'a'.repeat(64),
          page_count: 9,
          available: true,
        },
      ],
    },
  ],
});
const command = (): LiteratureCommand => ({
  kind: 'import',
  operation_id: operationId,
  artifact_id: 'artifact',
  version_id: 'exact-v1',
  title: 'Independent paper',
  project_id: 'project',
});
const receipt = () => ({
  schema: 'x-science.literature-receipt.v1' as const,
  operation_id: operationId,
  item: item(),
  attachment_id: 'attachment',
  version_id: 'litv_original',
});
const storage = () => {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
    values,
  };
};

describe('L382 independent Library service and mutation owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    mocks.http.mockReset();
  });
  it('reads the owner catalog and inspects a real independent attachment through the existing reading API', async () => {
    mocks.http
      .mockResolvedValueOnce({ schema: 'x-science.literature-page.v1', items: [item()], next_cursor: 'next' })
      .mockImplementationOnce(async (_method, _path, body) => ({
        schema: 'x-science.pdf-reading-candidates.v1',
        scope: pdfContext().scope,
        revision: 1,
        candidates: body.sources.map((source: unknown) => ({
          source,
          eligible: true,
          page_count: 9,
          size_bytes: 512,
          checksum: 'a'.repeat(64),
        })),
      }));
    const result = await loadLibraryReadingOptions(pdfContext(), {
      query: 'research & methods',
      signal: new AbortController().signal,
    });
    expect(mocks.http.mock.calls[0][1]).toBe('/api/literature/items?limit=20&query=research+%26+methods');
    expect(result.nextCursor).toBe('next');
    expect(result.options[0].source).toEqual({
      source_kind: 'literature-attachment-version',
      item_id: 'paper',
      attachment_id: 'attachment',
      version_id: 'litv_original',
    });
    expect(result.options[0].source).not.toHaveProperty('artifact_id');
  });
  it('does not accept cross-account responses or silently substitute a conflicting version relation', async () => {
    const pending = deferredPdf<unknown>();
    mocks.http.mockReturnValue(pending.promise);
    const request = loadLiteraturePage();
    setRendererAccountOwner('other');
    pending.resolve({ schema: 'x-science.literature-page.v1', items: [item()] });
    await expect(request).rejects.toThrow('account changed');
    const forged = item();
    forged.attachments[0].versions[0].attachment_id = 'different';
    expect(() => decodeLiteratureItem(forged)).toThrow('Invalid literature');
  });
  it('accepts the server opaque cursor beyond the unrelated identifier bound', async () => {
    const cursor = Buffer.from(
      JSON.stringify({ Query: 'q'.repeat(500), Owner: 'owner', After: 'paper', Project: '' })
    ).toString('base64url');
    expect(cursor.length).toBeGreaterThan(512);
    mocks.http.mockResolvedValue({ schema: 'x-science.literature-page.v1', items: [item()], next_cursor: cursor });
    await expect(loadLiteraturePage()).resolves.toMatchObject({ next_cursor: cursor });
    mocks.http.mockResolvedValue({ schema: 'x-science.literature-page.v1', items: [], next_cursor: 'a'.repeat(4097) });
    await expect(loadLiteraturePage()).rejects.toThrow('Invalid literature');
  });
  it('validates the exact mutation receipt and keeps routing identity out of the payload', async () => {
    mocks.http.mockResolvedValue(receipt());
    await expect(runLiteratureCommand(command())).resolves.toMatchObject({ version_id: 'litv_original' });
    expect(mocks.http.mock.calls[0].slice(0, 3)).toEqual([
      'POST',
      '/api/literature/import-artifact-version',
      {
        operation_id: operationId,
        artifact_id: 'artifact',
        version_id: 'exact-v1',
        title: 'Independent paper',
        project_id: 'project',
      },
    ]);
    mocks.http.mockResolvedValue({ ...receipt(), operation_id: 'different' });
    await expect(runLiteratureCommand(command())).rejects.toThrow('Invalid literature');
  });
  it('restores an unknown import and retries the original immutable request after controller recreation', async () => {
    const saved = storage();
    const run = vi.fn().mockRejectedValueOnce(new TypeError('lost receipt')).mockResolvedValueOnce(receipt());
    const first = new LiteratureMutationController('owner', { storage: saved, run });
    await expect(first.submit(command())).rejects.toThrow('lost receipt');
    expect(first.getSnapshot()).toMatchObject({ error: 'uncertain', pending: command(), mutating: false });
    const resumed = new LiteratureMutationController('owner', { storage: saved, run });
    expect(resumed.getSnapshot().pending).toEqual(command());
    await resumed.retry();
    expect(run.mock.calls.map((call) => call[0])).toEqual([command(), command()]);
    expect(saved.values.size).toBe(0);
    expect(resumed.getSnapshot()).toMatchObject({ pending: null, error: null, receipt: receipt() });
  });
  it('normalizes an edit once and verifies its original revision and metadata acknowledgement', async () => {
    const edit: LiteratureCommand = {
      kind: 'edit',
      operation_id: operationId,
      item_id: 'paper',
      expected_revision: 1,
      title: '  Edited title  ',
      metadata: { authors: 'A', z: 1 },
    };
    mocks.http.mockResolvedValue({
      ...receipt(),
      attachment_id: undefined,
      version_id: undefined,
      item: { ...item(), title: 'Edited title', metadata_revision: 2, metadata: { z: 1, authors: 'A' } },
    });
    await expect(runLiteratureCommand(edit)).resolves.toMatchObject({ item: { title: 'Edited title' } });
    expect(mocks.http.mock.calls[0][2]).toMatchObject({ title: 'Edited title', expected_revision: 1 });
    mocks.http.mockResolvedValue({
      ...receipt(),
      item: { ...item(), title: 'Edited title', metadata_revision: 5, metadata: { authors: 'A', z: 1 } },
    });
    await expect(runLiteratureCommand(edit)).rejects.toThrow('edit not acknowledged');
  });
  it('does not write when original-request storage is unavailable or duplicate a pending click', async () => {
    const run = vi.fn();
    const broken = new LiteratureMutationController('owner', {
      run,
      storage: {
        getItem: () => null,
        setItem: () => {
          throw new Error('quota');
        },
        removeItem: () => undefined,
      },
    });
    await expect(broken.submit(command())).rejects.toThrow('storage unavailable');
    expect(run).not.toHaveBeenCalled();
    const pending = deferredPdf<ReturnType<typeof receipt>>();
    run.mockReturnValue(pending.promise);
    const controller = new LiteratureMutationController('owner', { run, storage: storage() });
    const first = controller.submit(command());
    const second = controller.submit(command());
    expect(first).toBe(second);
    expect(run).toHaveBeenCalledTimes(1);
    pending.resolve(receipt());
    await first;
  });
});
