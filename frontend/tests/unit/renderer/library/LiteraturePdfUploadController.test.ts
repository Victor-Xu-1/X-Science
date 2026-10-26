import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { digestLibraryFile } from '@/renderer/services/runtime/libraryFileDigest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import type {
  OnboardingAttachmentInitReceipt,
  OnboardingUploadedArtifact,
} from '@/renderer/services/onboardingService';
import { installLiteratureLocks, literaturePendingStorage } from '../conversation/literatureMutationLockFixtures';

type Options = NonNullable<ConstructorParameters<typeof LiteratureMutationController>[1]>;
const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const hash = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad';
const key = 'x-science.literature-mutation.v1:owner';
const file = () => new File(['abc'], 'reference.pdf', { type: 'application/pdf', lastModified: 1 });
const ack: OnboardingAttachmentInitReceipt = { upload_id: id(3), chunk_size: 2, total_chunks: 2 };
const artifact: OnboardingUploadedArtifact = {
  artifactId: 'artifact-original',
  versionId: 'source-version-original',
  filename: 'reference.pdf',
  sizeBytes: 3,
  checksum: hash,
};
const receipt = (operationId = id(1)): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1',
  operation_id: operationId,
  item: {
    id: 'library-item',
    title: 'Title',
    metadata_revision: 1,
    metadata: {},
    deleted: false,
    project_ids: ['owned-project'],
    attachments: [
      {
        id: 'library-attachment',
        kind: 'fullText',
        versions: [
          {
            id: 'library-version',
            attachment_id: 'library-attachment',
            version_number: 1,
            filename: 'reference.pdf',
            mime_type: 'application/pdf',
            size_bytes: 3,
            checksum: hash,
            page_count: 1,
            available: true,
          },
        ],
      },
    ],
  },
  attachment_id: 'library-attachment',
  version_id: 'library-version',
});
const backend = (status: number, code: string) =>
  new BackendHttpError({ method: 'POST', path: '/api/literature/import-artifact-version', status, body: { code } });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
function setup(storage = literaturePendingStorage(), overrides: Options = {}) {
  let sequence = 0;
  const initialize = vi.fn<NonNullable<Options['initialize']>>().mockResolvedValue(ack);
  const upload = vi.fn<NonNullable<Options['upload']>>().mockResolvedValue(artifact);
  const run = vi.fn<NonNullable<Options['run']>>().mockImplementation(async (command) => receipt(command.operation_id));
  const digest = vi.fn<NonNullable<Options['digest']>>().mockResolvedValue(hash);
  const uuid = vi.fn(() => id(++sequence));
  const controller = new LiteratureMutationController('owner', {
    storage,
    initialize,
    upload,
    run,
    digest,
    uuid,
    ...overrides,
  });
  return { controller, storage, initialize, upload, run, digest, uuid };
}
const pending = (storage: ReturnType<typeof literaturePendingStorage>) => {
  const value = storage.getItem(key);
  return value ? JSON.parse(value) : null;
};
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

describe('Library PDF upload owner', () => {
  let request: ReturnType<typeof installLiteratureLocks>;
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    request = installLiteratureLocks();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('binds every ACK durably before the next write and returns Library identities', async () => {
    const x = setup();
    const order: string[] = [];
    x.initialize.mockImplementation(async (raw) => {
      expect(pending(x.storage).upload).toMatchObject({
        phase: 'init',
        init: raw,
        operation_id: id(1),
        file_sha256: hash,
      });
      expect(raw.operation_id).toBe(id(2));
      order.push('init');
      return ack;
    });
    x.upload.mockImplementation(async (_project, _file, options) => {
      expect(pending(x.storage).upload).toMatchObject({ phase: 'upload', init_ack: ack });
      expect(options?.pending).toEqual({ projectId: 'owned-project', uploadId: ack.upload_id });
      order.push('upload');
      return artifact;
    });
    x.run.mockImplementation(async (command) => {
      expect(pending(x.storage).upload).toMatchObject({ phase: 'catalogue', artifact, command });
      expect(command).toMatchObject({
        kind: 'import',
        operation_id: id(1),
        artifact_id: artifact.artifactId,
        version_id: artifact.versionId,
      });
      order.push('catalogue');
      return receipt(command.operation_id);
    });
    await expect(x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })).resolves.toEqual(
      receipt()
    );
    expect(order).toEqual(['init', 'upload', 'catalogue']);
    expect(request).toHaveBeenCalledTimes(1);
    expect(request.mock.calls[0][0]).toBe(key);
    expect(pending(x.storage)).toBeNull();
    expect(x.controller.getSnapshot()).toMatchObject({
      pending: null,
      mutating: false,
      receipt: receipt(),
      uploadState: null,
    });
    expect(x.initialize).toHaveBeenCalledTimes(1);
    expect(x.upload).toHaveBeenCalledTimes(1);
    expect(x.run).toHaveBeenCalledTimes(1);
    const acknowledged = x.controller.getSnapshot().receipt;
    if (acknowledged?.schema !== 'x-science.literature-receipt.v1') throw new Error('Expected Library receipt');
    expect(acknowledged.version_id).not.toBe(artifact.versionId);
  });

  it('stops at each failed storage boundary without dispatching the next write', async () => {
    for (const boundary of [1, 2, 3, 'remove', 'rejection-proof'] as const) {
      const storage = literaturePendingStorage();
      const set = storage.setItem;
      const remove = storage.removeItem;
      let writes = 0;
      storage.setItem = (name, value) => {
        if (++writes === (boundary === 'rejection-proof' ? 4 : boundary)) throw new Error('storage fault');
        set(name, value);
      };
      storage.removeItem = (name) => {
        if (boundary === 'remove') throw new Error('storage fault');
        remove(name);
      };
      const x = setup(storage);
      if (boundary === 'rejection-proof') x.run.mockRejectedValueOnce(backend(400, 'LITERATURE_INVALID'));
      await expect(
        x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
      ).rejects.toThrow('storage');
      expect(x.initialize).toHaveBeenCalledTimes(boundary === 1 ? 0 : 1);
      expect(x.upload).toHaveBeenCalledTimes(boundary === 1 || boundary === 2 ? 0 : 1);
      expect(x.run).toHaveBeenCalledTimes(boundary === 'remove' || boundary === 'rejection-proof' ? 1 : 0);
      expect(x.controller.getSnapshot()).toMatchObject({ error: 'storage', mutating: false });
      expect(['initializing', 'uploading', 'catalogue']).not.toContain(x.controller.getSnapshot().uploadState);
      if (boundary === 1) expect(pending(storage)).toBeNull();
      else
        expect(pending(storage).upload.phase).toBe(boundary === 2 ? 'init' : boundary === 3 ? 'upload' : 'catalogue');
      if (boundary === 'rejection-proof') {
        expect(pending(storage).upload.catalogue_rejected).toBe(false);
        expect(x.controller.getSnapshot().canDiscardRejectedUpload).toBe(false);
      }
    }
  });

  it('replays an unknown init with the exact original UUID and raw body', async () => {
    const x = setup();
    x.initialize.mockRejectedValueOnce(new TypeError('ACK lost'));
    await expect(x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })).rejects.toThrow(
      'ACK lost'
    );
    const original = pending(x.storage).upload;
    expect(original.phase).toBe('init');
    await x.controller.retry();
    expect(x.initialize.mock.calls.map(([raw]) => raw)).toEqual([original.init, original.init]);
    expect(x.uuid).toHaveBeenCalledTimes(2);
    expect(x.upload).toHaveBeenCalledTimes(1);
    expect(pending(x.storage)).toBeNull();
  });

  it('excludes another renderer from replacing an active upload intent', async () => {
    vi.useFakeTimers();
    const first = setup();
    const held = deferred<OnboardingAttachmentInitReceipt>();
    first.initialize.mockImplementationOnce(() => held.promise);
    const original = first.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() });
    await vi.advanceTimersByTimeAsync(0);
    expect(first.initialize).toHaveBeenCalledTimes(1);
    const frozen = first.storage.getItem(key);
    let otherSequence = 10;
    const other = setup(first.storage, { uuid: () => id(++otherSequence) });
    const replacing = other.controller.importPdf({ projectId: 'owned-project', title: 'Other', file: file() });
    const rejected = expect(replacing).rejects.toThrow(/storage|lock/i);
    await vi.advanceTimersByTimeAsync(5_000);
    await rejected;
    expect(first.storage.getItem(key)).toBe(frozen);
    expect(other.initialize).not.toHaveBeenCalled();
    held.resolve(ack);
    await vi.advanceTimersByTimeAsync(0);
    await original;
    expect(other.initialize).not.toHaveBeenCalled();
    expect(pending(first.storage)).toBeNull();
  });

  it('retains an init conflict and never authorizes discard', async () => {
    const x = setup();
    x.initialize.mockRejectedValueOnce(backend(409, 'ATTACHMENT_INIT_IDEMPOTENCY_CONFLICT'));
    await expect(
      x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
    ).rejects.toMatchObject({ status: 409 });
    expect(pending(x.storage).upload.phase).toBe('init');
    expect(x.controller.getSnapshot()).toMatchObject({ uploadState: 'conflict', canDiscardRejectedUpload: false });
    await expect(x.controller.discardRejectedPdfImport()).rejects.toThrow('not definitely rejected');
    expect(x.upload).not.toHaveBeenCalled();
  });

  it('clears only a matching typed507 pre-admission rejection', async () => {
    const x = setup();
    x.initialize.mockRejectedValueOnce(backend(507, 'ATTACHMENT_INIT_WRITE_REJECTED'));
    await expect(
      x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
    ).rejects.toMatchObject({ status: 507 });
    expect(pending(x.storage)).toBeNull();
    expect(x.controller.getSnapshot()).toMatchObject({ pending: null, error: 'rejected', mutating: false });
    expect(x.upload).not.toHaveBeenCalled();
    expect(x.run).not.toHaveBeenCalled();
  });

  it('requires equal full bytes after reload and preserves original request metadata', async () => {
    const first = setup(undefined, { digest: digestLibraryFile });
    first.upload.mockRejectedValueOnce(new TypeError('transfer interrupted'));
    await expect(
      first.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
    ).rejects.toThrow('transfer interrupted');
    const durable = first.storage.getItem(key);
    const restored = setup(first.storage, { digest: digestLibraryFile });
    await expect(restored.controller.retry()).rejects.toThrow('original File');
    await expect(
      restored.controller.retry(new File(['abd'], 'reference.pdf', { type: 'application/pdf', lastModified: 1 }))
    ).rejects.toThrow('bytes differ');
    expect(first.storage.getItem(key)).toBe(durable);
    expect(restored.initialize).not.toHaveBeenCalled();
    expect(restored.upload).not.toHaveBeenCalled();
    await restored.controller.retry(new File(['abc'], 'renamed.pdf', { type: 'text/plain' }));
    expect(restored.initialize).not.toHaveBeenCalled();
    expect(restored.uuid).not.toHaveBeenCalled();
    expect(restored.upload.mock.calls[0][1]).toMatchObject({ name: 'reference.pdf', type: 'application/pdf', size: 3 });
    expect(restored.upload.mock.calls[0][2]?.pending?.uploadId).toBe(ack.upload_id);
  });

  it.each(['init', 'upload', 'catalogue', 'catalogue-rejection'] as const)(
    'settles cancel during noncooperative %s and preserves the next flight',
    async (stage) => {
      const x = setup();
      const lateInit = deferred<OnboardingAttachmentInitReceipt>();
      const lateArtifact = deferred<OnboardingUploadedArtifact>();
      const lateReceipt = deferred<LiteratureReceipt>();
      const nextReceipt = deferred<LiteratureReceipt>();
      x.run.mockImplementation(() => nextReceipt.promise);
      if (stage === 'init') x.initialize.mockImplementationOnce(() => lateInit.promise);
      else if (stage === 'upload') x.upload.mockImplementationOnce(() => lateArtifact.promise);
      else x.run.mockImplementationOnce(() => lateReceipt.promise);
      const first = x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() });
      const rejected = expect(first).rejects.toMatchObject({ name: 'AbortError' });
      await vi.waitFor(() =>
        expect(stage === 'init' ? x.initialize : stage === 'upload' ? x.upload : x.run).toHaveBeenCalledTimes(1)
      );
      x.controller.cancelPdfImport();
      await rejected;
      const retry = x.controller.retry();
      await vi.waitFor(() => expect(x.run).toHaveBeenCalledTimes(stage.startsWith('catalogue') ? 2 : 1));
      const nextDurable = x.storage.getItem(key);
      if (stage === 'init') lateInit.resolve(ack);
      else if (stage === 'upload') lateArtifact.resolve(artifact);
      else if (stage === 'catalogue-rejection') lateReceipt.reject(new TypeError('late rejected result'));
      else lateReceipt.resolve(receipt());
      await tick();
      expect(x.storage.getItem(key)).toBe(nextDurable);
      expect(x.controller.getSnapshot()).toMatchObject({ mutating: true, pending: { phase: 'catalogue' } });
      expect(x.controller.retry()).toBe(retry);
      nextReceipt.resolve(receipt());
      await retry;
      expect(pending(x.storage)).toBeNull();
      expect(x.uuid).toHaveBeenCalledTimes(2);
    }
  );

  it('cancels a queued native lock promptly and rejects its late callback', async () => {
    const gate = deferred<void>();
    request = installLiteratureLocks(gate.promise);
    const x = setup();
    const first = x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() });
    const rejected = expect(first).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    x.controller.cancelPdfImport();
    await rejected;
    expect(pending(x.storage)).toBeNull();
    gate.resolve();
    await tick();
    expect(x.initialize).not.toHaveBeenCalled();
    expect(pending(x.storage)).toBeNull();
  });

  it('aborts account replacement without deleting the original durable intent', async () => {
    const x = setup();
    const late = deferred<OnboardingAttachmentInitReceipt>();
    x.initialize.mockImplementationOnce(() => late.promise);
    const first = x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() });
    const rejected = expect(first).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(x.initialize).toHaveBeenCalledTimes(1));
    const original = x.storage.getItem(key);
    setRendererAccountOwner('other');
    await rejected;
    late.resolve(ack);
    await tick();
    expect(x.upload).not.toHaveBeenCalled();
    expect(x.storage.getItem(key)).toBe(original);
    const other = new LiteratureMutationController('other', { storage: x.storage });
    expect(other.getSnapshot().pending).toBeNull();
    setRendererAccountOwner('owner');
    const restored = setup(x.storage);
    expect(restored.controller.getSnapshot()).toMatchObject({
      pending: { phase: 'init' },
      uploadState: 'file-required',
    });
    await expect(restored.controller.retry()).rejects.toThrow('original File');
    expect(restored.initialize).not.toHaveBeenCalled();
  });

  it('retries the exact catalogue command without File or another upload', async () => {
    const x = setup();
    x.run.mockRejectedValueOnce(backend(400, 'LITERATURE_INVALID'));
    await expect(
      x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
    ).rejects.toMatchObject({ code: 'LITERATURE_INVALID' });
    const original = pending(x.storage).upload.command;
    expect(x.controller.getSnapshot().canDiscardRejectedUpload).toBe(true);
    const restored = setup(x.storage);
    await expect(restored.controller.discardRejectedPdfImport()).rejects.toThrow('not definitely rejected');
    await restored.controller.retry();
    expect(restored.run.mock.calls[0][0]).toEqual(original);
    expect(restored.initialize).not.toHaveBeenCalled();
    expect(restored.upload).not.toHaveBeenCalled();
    expect(pending(x.storage)).toBeNull();
  });

  it('allows explicit discard only after the exact current typed rejection', async () => {
    const x = setup();
    x.run.mockRejectedValueOnce(backend(404, 'LITERATURE_UNAVAILABLE'));
    await expect(
      x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
    ).rejects.toMatchObject({ code: 'LITERATURE_UNAVAILABLE' });
    expect(pending(x.storage).upload.phase).toBe('catalogue');
    await x.controller.discardRejectedPdfImport();
    expect(pending(x.storage)).toBeNull();
    expect(x.upload).toHaveBeenCalledTimes(1);
    expect(x.run).toHaveBeenCalledTimes(1);
  });

  it('invalidates an old tab rejection proof before a newer unknown catalogue attempt', async () => {
    const first = setup();
    first.run.mockRejectedValueOnce(backend(400, 'LITERATURE_INVALID'));
    await expect(
      first.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
    ).rejects.toBeDefined();
    expect(pending(first.storage).upload.catalogue_rejected).toBe(true);
    expect(first.controller.getSnapshot().canDiscardRejectedUpload).toBe(true);
    const newer = setup(first.storage);
    newer.run.mockRejectedValueOnce(new TypeError('newer ACK lost'));
    await expect(newer.controller.retry()).rejects.toThrow('newer ACK lost');
    expect(pending(first.storage).upload.catalogue_rejected).toBe(false);
    const unknown = first.storage.getItem(key);
    await expect(first.controller.discardRejectedPdfImport()).rejects.toThrow('not definitely rejected');
    expect(first.storage.getItem(key)).toBe(unknown);
    expect(first.controller.getSnapshot().canDiscardRejectedUpload).toBe(false);
    expect(newer.initialize).not.toHaveBeenCalled();
    expect(newer.upload).not.toHaveBeenCalled();
  });

  it('ignores an old cleanup token without cancelling the new flight or legacy command', async () => {
    const x = setup();
    const oldReply = deferred<LiteratureReceipt>();
    const newReply = deferred<LiteratureReceipt>();
    x.run.mockImplementationOnce(() => oldReply.promise).mockImplementationOnce(() => newReply.promise);
    const old = x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() });
    const cancelled = expect(old).rejects.toMatchObject({ name: 'AbortError' });
    await vi.waitFor(() => expect(x.run).toHaveBeenCalledTimes(1));
    x.controller.cancelPdfImport(old);
    await cancelled;
    const current = x.controller.retry();
    let settled = false;
    void current.then(
      () => {
        settled = true;
      },
      () => {
        settled = true;
      }
    );
    await vi.waitFor(() => expect(x.run).toHaveBeenCalledTimes(2));
    const durable = x.storage.getItem(key);
    x.controller.cancelPdfImport(old);
    oldReply.resolve(receipt());
    await tick();
    expect(settled).toBe(false);
    expect(x.controller.getSnapshot().mutating).toBe(true);
    expect(x.storage.getItem(key)).toBe(durable);
    expect(x.controller.retry()).toBe(current);
    newReply.resolve(receipt());
    await current;
    const legacyReply = deferred<LiteratureReceipt>();
    x.run.mockImplementationOnce(() => legacyReply.promise);
    const legacy = x.controller.submit({
      kind: 'edit',
      operation_id: id(77),
      item_id: 'library-item',
      expected_revision: 1,
      title: 'Title',
    });
    await vi.waitFor(() => expect(x.run).toHaveBeenCalledTimes(3));
    x.controller.cancelPdfImport();
    x.controller.cancelPdfImport(old);
    expect(x.controller.getSnapshot().mutating).toBe(true);
    legacyReply.resolve(receipt(id(77)));
    await legacy;
  });

  it('never grants discard for catalogue conflicts unknown errors or malformed receipts', async () => {
    for (const outcome of [
      backend(409, 'LITERATURE_CONFLICT'),
      backend(500, 'LITERATURE_INTERNAL'),
      backend(400, ''),
      { ...receipt(), operation_id: id(99) },
    ]) {
      const x = setup();
      if (outcome instanceof Error) x.run.mockRejectedValueOnce(outcome);
      else x.run.mockResolvedValueOnce(outcome);
      await expect(
        x.controller.importPdf({ projectId: 'owned-project', title: 'Title', file: file() })
      ).rejects.toBeDefined();
      expect(x.controller.getSnapshot().canDiscardRejectedUpload).toBe(false);
      await expect(x.controller.discardRejectedPdfImport()).rejects.toThrow('not definitely rejected');
      expect(pending(x.storage).upload.phase).toBe('catalogue');
      expect(x.upload).toHaveBeenCalledTimes(1);
    }
  });
});
