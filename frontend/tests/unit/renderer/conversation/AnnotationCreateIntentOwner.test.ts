import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { AnnotationCreateIntentOwner } from '@/renderer/services/runtime/AnnotationCreateIntentOwner';
import {
  annotationIntentStorageKey,
  type AnnotationCreateIntent,
} from '@/renderer/services/runtime/annotationCreateIntent';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import type { XScienceArtifactAnnotation } from '@/renderer/services/xScienceAnnotations';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';

const target = { kind: 'literature' as const, versionId: 'litv_one' };
const checksum = 'a'.repeat(64);
const source = { targetKey: 'litv:litv_one', checksum };
const body = { type: 'point' as const, text: 'Original note', xPercent: 50, yPercent: 50, pageNumber: 2 };
const key = annotationIntentStorageKey('owner', target);
const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const row = (text = 'Current edited note', id = uuid(1)): XScienceArtifactAnnotation => ({
  id,
  artifactId: null,
  type: 'point',
  text,
  targetKey: source.targetKey,
  contentChecksum: checksum,
  label: '①',
  xPercent: 50,
  yPercent: 50,
  startLine: null,
  startColumn: null,
  endLine: null,
  endColumn: null,
  selectionText: null,
  pageNumber: 2,
  selectionPrefix: null,
  screenshotArtifactId: null,
  elementSelector: null,
  elementDescriptor: null,
  addressedAt: null,
  addressedInFrameId: null,
  createdAt: '2026-11-01T00:00:00Z',
});
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('owner');
  installLiteratureLocks();
});
afterEach(() => {
  setRendererAccountOwner('');
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('one durable annotation create intent', () => {
  it('persists before POST and replays the frozen input after lost ACK and restore', async () => {
    const storage = literaturePendingStorage();
    const firstSend = vi.fn(async (intent: AnnotationCreateIntent) => {
      expect(JSON.parse(storage.getItem(key)!)).toMatchObject({ operationId: intent.operationId, body });
      throw new TypeError('ACK lost');
    });
    const first = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send: firstSend });
    await expect(first.start(body, source)).rejects.toThrow('ACK lost');
    const retained = storage.getItem(key);
    const loadSource = vi.fn().mockRejectedValue(new Error('today metadata is unavailable'));
    const send = vi.fn().mockResolvedValue(row());
    const restored = new AnnotationCreateIntentOwner('owner', target, {
      storage,
      loadSource,
      send,
      uuid: () => uuid(2),
    });
    await expect(restored.retryFrozen()).resolves.toMatchObject({ text: 'Current edited note' });
    expect(send.mock.calls[0][0]).toEqual(JSON.parse(retained!));
    expect(loadSource).not.toHaveBeenCalled();
    expect(storage.getItem(key)).toBeNull();
  });

  it('refuses a new draft instead of overwriting an unresolved original body', async () => {
    const storage = literaturePendingStorage();
    const send = vi.fn().mockRejectedValue(new Error('transport failure'));
    const owner = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send });
    await expect(owner.start(body, source)).rejects.toThrow();
    const original = storage.getItem(key);
    await expect(owner.start({ ...body, text: 'Different draft' }, source)).rejects.toThrow();
    expect(storage.getItem(key)).toBe(original);
    expect(send).toHaveBeenCalledTimes(1);
  });

  it.each([
    [404, '', 'sourceUnavailable'],
    [409, '', 'uncertain'],
    [409, 'ANNOTATION_CREATE_IDEMPOTENCY_CONFLICT', 'conflict'],
    [503, 'ANNOTATION_CREATE_OUTCOME_UNKNOWN', 'uncertain'],
  ] as const)('retains the exact intent for status%s/code%s', async (status, code, expected) => {
    const storage = literaturePendingStorage();
    const send = vi.fn().mockRejectedValue(
      new BackendHttpError({
        method: 'POST',
        path: '/api/literature/attachment-versions/litv_one/annotations',
        status,
        body: { code, detail: 'Unavailable' },
      })
    );
    const owner = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send });
    await expect(owner.start(body, source)).rejects.toBeInstanceOf(BackendHttpError);
    expect(JSON.parse(storage.getItem(key)!)).toMatchObject({ operationId: uuid(1), body });
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: expected });
  });

  it('accepts only typed committed-missing410 and does not recreate the note', async () => {
    const storage = literaturePendingStorage();
    const send = vi.fn().mockRejectedValue(
      new BackendHttpError({
        method: 'POST',
        path: '/api/annotations',
        status: 410,
        body: { code: 'ANNOTATION_COMMITTED_RESULT_MISSING' },
      })
    );
    const owner = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send });
    await expect(owner.start(body, source)).resolves.toBeNull();
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: 'missing', pending: null });
    await expect(owner.retryFrozen()).resolves.toBeNull();
    expect(send).toHaveBeenCalledTimes(1);
  });

  it('keeps a newer intent when a duplicate old ACK finishes late', async () => {
    const storage = literaturePendingStorage();
    const oldReply = deferred<XScienceArtifactAnnotation>();
    const nextReply = deferred<XScienceArtifactAnnotation>();
    const oldSend = vi.fn(() => oldReply.promise);
    const a = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send: oldSend });
    const first = a.start(body, source);
    await vi.waitFor(() => expect(oldSend).toHaveBeenCalledOnce());
    const other = new AnnotationCreateIntentOwner('owner', target, { storage, send: vi.fn().mockResolvedValue(row()) });
    await other.retryFrozen();
    const nextSend = vi.fn(() => nextReply.promise);
    const next = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(2), send: nextSend });
    const second = next.start({ ...body, text: 'Next note' }, source);
    await vi.waitFor(() => expect(nextSend).toHaveBeenCalledOnce());
    oldReply.resolve(row());
    await expect(first).resolves.toBeNull();
    expect(JSON.parse(storage.getItem(key)!)).toMatchObject({ operationId: uuid(2), body: { text: 'Next note' } });
    nextReply.resolve(row('Next note', uuid(2)));
    await second;
  });

  it('does not project a late old ACK after another tab confirmed and deleted the note', async () => {
    const storage = literaturePendingStorage();
    const accepted = row();
    // Protocol fixture only: the other tab first confirms this exact create, then deletes its result.
    const canonicalNotes = new Map([[accepted.id, accepted]]);
    const oldReply = deferred<XScienceArtifactAnnotation>();
    const oldSend = vi.fn(() => oldReply.promise);
    const a = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send: oldSend });
    const oldAttempt = a.start(body, source);
    await vi.waitFor(() => expect(oldSend).toHaveBeenCalledOnce());
    const b = new AnnotationCreateIntentOwner('owner', target, {
      storage,
      send: vi.fn(async () => canonicalNotes.get(accepted.id)!),
    });
    await b.retryFrozen();
    expect(storage.getItem(key)).toBeNull();
    canonicalNotes.delete(accepted.id);
    oldReply.resolve(accepted);
    await expect(oldAttempt).resolves.toBeNull();
    expect(a.getSnapshot()).toMatchObject({ status: 'idle', busy: false, pending: null });
    expect(a.getSnapshot().lastSuccess).toBeUndefined();
    expect(canonicalNotes.has(accepted.id)).toBe(false);
    expect(storage.getItem(key)).toBeNull();
  });

  it('retains the original when ACK has a different source checksum', async () => {
    const storage = literaturePendingStorage();
    const owner = new AnnotationCreateIntentOwner('owner', target, {
      storage,
      uuid: () => uuid(1),
      send: vi.fn().mockResolvedValue({ ...row(), contentChecksum: 'b'.repeat(64) }),
    });
    await expect(owner.start(body, source)).rejects.toThrow('acknowledgement');
    expect(storage.getItem(key)).not.toBeNull();
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: 'uncertain' });
  });

  it('isolates stable account slots and fences an old account ACK without erasing its intent', async () => {
    const storage = literaturePendingStorage();
    const reply = deferred<XScienceArtifactAnnotation>();
    const send = vi.fn(() => reply.promise);
    const first = new AnnotationCreateIntentOwner('owner', target, { storage, uuid: () => uuid(1), send });
    const outcome = first.start(body, source).catch((error: unknown) => error);
    await vi.waitFor(() => expect(send).toHaveBeenCalledOnce());
    const original = storage.getItem(key);
    setRendererAccountOwner('other-owner');
    const otherSend = vi.fn();
    const other = new AnnotationCreateIntentOwner('other-owner', target, { storage, send: otherSend });
    expect(other.getSnapshot().pending).toBeNull();
    reply.resolve(row());
    expect(await outcome).toBeInstanceOf(Error);
    expect(storage.getItem(key)).toBe(original);
    expect(otherSend).not.toHaveBeenCalled();
    setRendererAccountOwner('owner');
    const restored = new AnnotationCreateIntentOwner('owner', target, {
      storage,
      send: vi.fn().mockResolvedValue(row()),
    });
    await expect(restored.retryFrozen()).resolves.toMatchObject({ text: 'Current edited note' });
    expect(storage.getItem(key)).toBeNull();
  });

  it.each([
    { name: 'missing point X', input: { ...body, xPercent: null } },
    { name: 'missing point Y', input: { ...body, yPercent: null } },
    { name: 'out of range coordinate', input: { ...body, xPercent: 101 } },
    { name: 'missing selected text', input: { ...body, type: 'text_selection' as const, selectionText: '  ' } },
    { name: 'missing screenshot source', input: { ...body, type: 'screenshot' as const } },
    { name: 'missing HTML anchor', input: { ...body, type: 'html_element' as const } },
    { name: 'zero page', input: { ...body, pageNumber: 0 } },
    { name: 'fractional page', input: { ...body, pageNumber: 1.5 } },
  ])('keeps a locally invalid $name draft out of the persistent slot and permits correction', async ({ input }) => {
    const storage = literaturePendingStorage();
    const send = vi.fn().mockResolvedValue(row());
    const allocate = vi.fn(() => uuid(1));
    const owner = new AnnotationCreateIntentOwner('owner', target, { storage, send, uuid: allocate });
    await expect((async () => owner.start(input, source))()).rejects.toThrow('Invalid original');
    expect(storage.getItem(key)).toBeNull();
    expect(send).not.toHaveBeenCalled();
    expect(allocate).not.toHaveBeenCalled();
    await expect(owner.start(body, source)).resolves.toMatchObject({ id: uuid(1) });
    expect(send).toHaveBeenCalledOnce();
    expect(storage.getItem(key)).toBeNull();
  });

  it('rejects a malformed persisted intent rather than silently replacing or sending it', async () => {
    const storage = literaturePendingStorage();
    const raw = JSON.stringify({
      schema: 'x-science.annotation-create.v1',
      ownerId: 'owner',
      operationId: uuid(1),
      target,
      source: { ...source, unexpected: true },
      body,
    });
    storage.setItem(key, raw);
    const send = vi.fn();
    const loadSource = vi.fn();
    const owner = new AnnotationCreateIntentOwner('owner', target, { storage, send, loadSource });
    await expect(owner.retryFrozen()).rejects.toThrow('Invalid original');
    expect(owner.getSnapshot()).toMatchObject({ status: 'storage', busy: false });
    expect(storage.getItem(key)).toBe(raw);
    expect(send).not.toHaveBeenCalled();
    expect(loadSource).not.toHaveBeenCalled();
  });

  it('does not send when the original intent cannot be persisted', async () => {
    const storage = literaturePendingStorage();
    vi.spyOn(storage, 'setItem').mockImplementation(() => {
      throw new Error('Storage unavailable');
    });
    const send = vi.fn().mockResolvedValue(row());
    const owner = new AnnotationCreateIntentOwner('owner', target, { storage, send });
    await expect(owner.start(body, source)).rejects.toThrow('Storage unavailable');
    expect(send).not.toHaveBeenCalled();
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: 'storage' });
  });

  it('ends a bounded HTTP wait as uncertain while retaining its frozen request', async () => {
    vi.useFakeTimers();
    const storage = literaturePendingStorage();
    const send = vi.fn(() => new Promise<XScienceArtifactAnnotation>(() => {}));
    const owner = new AnnotationCreateIntentOwner('owner', target, {
      storage,
      send,
      timeoutMs: 20,
      uuid: () => uuid(1),
    });
    const outcome = owner.start(body, source).catch((error: unknown) => error);
    await vi.advanceTimersByTimeAsync(0);
    expect(send).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(21);
    expect(await outcome).toBeInstanceOf(Error);
    expect(owner.getSnapshot()).toMatchObject({ busy: false, status: 'uncertain' });
    expect(storage.getItem(key)).not.toBeNull();
  });
});
