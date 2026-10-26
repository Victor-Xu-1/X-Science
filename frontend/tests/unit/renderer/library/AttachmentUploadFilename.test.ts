import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  createUploadIntent,
  encodePendingRecord,
  withInitReceipt,
} from '@/renderer/services/runtime/literatureUploadIntent';
import { uploadXScienceProjectAttachment } from '@/renderer/services/onboardingService';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from '../conversation/literatureMutationLockFixtures';

type Storage = ReturnType<typeof literaturePendingStorage>;
type Options = NonNullable<ConstructorParameters<typeof LiteratureMutationController>[1]>;
const id = (value: number) => `00000000-0000-4000-8000-${String(value).padStart(12, '0')}`;
const key = 'x-science.literature-mutation.v1:owner';
const checksum = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad';
const ack = { upload_id: id(3), chunk_size: 16, total_chunks: 1 };
const file = (name: string, bytes = 'abc') => new File([bytes], name, { type: 'application/pdf' });
const receipt = (operationId: string): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1',
  operation_id: operationId,
  item: {
    id: 'library-item',
    title: 'Paper',
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
            filename: 'paper.pdf',
            mime_type: 'application/pdf',
            size_bytes: 3,
            checksum,
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
function owner(storage: Storage) {
  let sequence = 0;
  const uuid = vi.fn(() => id(++sequence));
  const run = vi.fn<NonNullable<Options['run']>>(async (command) => receipt(command.operation_id));
  return { controller: new LiteratureMutationController('owner', { storage, uuid, run }), uuid, run };
}
function retained(filename: string, storage: Storage) {
  const initial = createUploadIntent({
    projectId: 'owned-project',
    title: 'Paper',
    filename,
    size: 3,
    contentType: 'application/pdf',
    hash: checksum,
    catalogueOperationId: id(1),
    initOperationId: id(2),
  });
  const intent = withInitReceipt(initial, ack);
  const raw = encodePendingRecord('owner', intent);
  storage.setItem(key, raw);
  return { intent, raw };
}
function server(statusFilename: unknown, storage?: Storage, allowInit = true) {
  const calls: Array<{ path: string; init?: RequestInit }> = [];
  const chunks: string[] = [];
  const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
    const path = String(input);
    calls.push({ path, init });
    const respond = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });
    if (path === '/api/artifacts/upload/init') {
      if (!allowInit) throw new Error('Recovery must not allocate another upload');
      return respond(ack);
    }
    if (path === `/api/artifacts/upload/status/${id(3)}`) {
      if (storage) expect(JSON.parse(storage.getItem(key)!).upload.init_ack).toEqual(ack);
      return respond({
        chunk_size: 16,
        total_size: 3,
        total_chunks: 1,
        missing_chunks: [0],
        ...(statusFilename === undefined ? {} : { filename: statusFilename }),
      });
    }
    if (path === '/api/artifacts/upload/chunk') {
      const body = init?.body as FormData;
      expect(body.get('upload_id')).toBe(id(3));
      expect(body.get('chunk_index')).toBe('0');
      const chunk = body.get('chunk');
      if (!chunk || typeof chunk === 'string') throw new Error('Expected the actual file chunk');
      chunks.push(await chunk.text());
      return respond({});
    }
    if (path === `/api/artifacts/upload/finalize?upload_id=${id(3)}`) {
      expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(`onboarding-artifact-finalize-${id(3)}`);
      return respond({
        artifact_id: 'source-artifact',
        version_id: 'source-version',
        filename: statusFilename,
        size_bytes: 3,
        checksum,
      });
    }
    throw new Error(`Unexpected real upload request: ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { calls, chunks, fetchMock };
}

describe('Attachment upload filename contract', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    installLiteratureLocks();
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Unexpected network request')));
  });
  afterEach(() => {
    setRendererAccountOwner(null);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('uploads first admitted normalized and BOM filenames without changing raw intent', async () => {
    for (const [rawName, admittedName] of [
      [' \u0085paper.pdf\u00a0 ', 'paper.pdf'],
      ['\ufeffpaper.pdf', '\ufeffpaper.pdf'],
    ]) {
      const storage = literaturePendingStorage();
      const x = owner(storage);
      const http = server(admittedName, storage);
      const result = await x.controller.importPdf({ projectId: 'owned-project', title: 'Paper', file: file(rawName) });
      expect(result.operation_id).toBe(id(1));
      expect(http.calls.map((call) => call.path)).toEqual([
        '/api/artifacts/upload/init',
        `/api/artifacts/upload/status/${id(3)}`,
        '/api/artifacts/upload/chunk',
        `/api/artifacts/upload/finalize?upload_id=${id(3)}`,
      ]);
      const init = http.calls[0].init!;
      expect(JSON.parse(init.body as string)).toEqual({
        operation_id: id(2),
        project_id: 'owned-project',
        filename: rawName,
        total_size: 3,
        content_type: 'application/pdf',
        chunk_size: 0,
      });
      expect(new Headers(init.headers).get('Idempotency-Key')).toBe(id(2));
      expect(http.chunks).toEqual(['abc']);
      expect(x.run).toHaveBeenCalledExactlyOnceWith({
        kind: 'import',
        operation_id: id(1),
        artifact_id: 'source-artifact',
        version_id: 'source-version',
        title: 'Paper',
        project_id: 'owned-project',
      });
      expect(storage.getItem(key)).toBeNull();
    }
  });

  it('recovers the original upload with a reselected equal-byte File and no new init', async () => {
    const rawName = '\ufeffpaper.pdf\u0085';
    const storage = literaturePendingStorage();
    const original = retained(rawName, storage);
    const restored = owner(storage);
    const http = server('\ufeffpaper.pdf', storage, false);
    expect(restored.controller.getSnapshot().pending).toEqual(original.intent);
    await restored.controller.retry(new File(['abc'], 'renamed.pdf', { type: 'text/plain' }));
    expect(restored.uuid).not.toHaveBeenCalled();
    expect(http.calls.map((call) => call.path)).toEqual([
      `/api/artifacts/upload/status/${id(3)}`,
      '/api/artifacts/upload/chunk',
      `/api/artifacts/upload/finalize?upload_id=${id(3)}`,
    ]);
    expect(http.chunks).toEqual(['abc']);
    expect(restored.run.mock.calls[0][0]).toMatchObject({ operation_id: id(1), artifact_id: 'source-artifact' });
    expect(storage.getItem(key)).toBeNull();
  });

  it('rejects distinct or invalid status filenames and changed File bytes', async () => {
    for (const [localName, admittedName] of [
      ['paper.pdf', '\ufeffpaper.pdf'],
      ['\ufeffpaper.pdf', 'paper.pdf'],
      ['part a.pdf', 'parta.pdf'],
      ['paper.pdf', undefined],
      ['paper.pdf', null],
      ['paper.pdf', 7],
      ['paper.pdf', ' \u0085 '],
    ]) {
      const http = server(admittedName);
      await expect(
        uploadXScienceProjectAttachment('owned-project', file(localName as string), {
          pending: { projectId: 'owned-project', uploadId: id(3) },
        })
      ).rejects.toThrow('Attachment upload status response is invalid');
      expect(http.calls).toHaveLength(1);
      expect(http.calls[0].path).toBe(`/api/artifacts/upload/status/${id(3)}`);
      expect(http.chunks).toEqual([]);
    }
    const storage = literaturePendingStorage();
    const original = retained(' paper.pdf', storage);
    const restored = owner(storage);
    const http = server('paper.pdf', storage, false);
    await expect(restored.controller.retry(file(' paper.pdf', 'abd'))).rejects.toThrow('File bytes differ');
    expect(restored.controller.getSnapshot().uploadState).toBe('file-mismatch');
    expect(storage.getItem(key)).toBe(original.raw);
    expect(http.fetchMock).not.toHaveBeenCalled();
    expect(restored.run).not.toHaveBeenCalled();
  });
});
