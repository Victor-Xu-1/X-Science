import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { TChatConversation } from '@/common/config/storage';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import {
  isConversationArchiveUnavailable,
  setConversationArchived,
} from '@/renderer/services/runtime/conversationArchive';
import {
  ARCHIVE_UNDO_DURATION_MS,
  expireArchiveUndo,
  getArchiveOperationsSnapshot,
  runConversationArchive,
  undoConversationArchive,
} from '@/renderer/services/runtime/conversationArchiveOperations';

const conversation = (archived = false, revision = 4): TChatConversation => ({
  id: 'frame-a',
  type: 'acp',
  name: 'Research notes',
  created_at: 1,
  modified_at: 2,
  status: 'finished',
  extra: {
    backend: 'xscience',
    frame_incarnation_id: 'inc-a',
    presentation_revision: revision,
    ...(archived ? { archived_at: 1000 } : {}),
  },
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
const response = (archived: boolean, changed = true) =>
  json({ conversation: conversation(archived, changed ? 5 : 4), changed });
const bodyOf = (mock: ReturnType<typeof vi.fn>, index = 0) => JSON.parse(String(mock.mock.calls[index][1].body));

beforeEach(() => {
  setRendererAccountOwner('archive-test-owner');
});
afterEach(() => {
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('typed conversation archive contract', () => {
  it('does not manufacture mutation authority from an unavailable presentation', async () => {
    const item = conversation();
    Object.assign(item.extra, { presentation_unavailable: true });
    const fetchMock = vi.fn(async () => response(true));
    vi.stubGlobal('fetch', fetchMock);
    await expect(setConversationArchived(item, true)).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it('freezes exact incarnation/revision and desired state, preserving the canonical returned presentation', async () => {
    const fetchMock = vi.fn(async () => response(true));
    vi.stubGlobal('fetch', fetchMock);
    const result = await setConversationArchived(conversation(), true);
    expect(result.acknowledgement).toBe('confirmed');
    expect(result.changed).toBe(true);
    expect(result.conversation.extra).toMatchObject({
      frame_incarnation_id: 'inc-a',
      presentation_revision: 5,
      archived_at: 1000,
    });
    expect(bodyOf(fetchMock)).toEqual({
      archived: true,
      expected_incarnation_id: 'inc-a',
      expected_presentation_revision: 4,
    });
    expect(fetchMock.mock.calls[0][0]).toContain('/api/conversations/frame-a/archive');
    expect(fetchMock.mock.calls[0][1].method).toBe('POST');
  });

  it.each([true, false])('retains a legitimate confirmed no-op for desired archive=%s', async (archived) => {
    const fetchMock = vi.fn(async () => response(archived, false));
    vi.stubGlobal('fetch', fetchMock);
    const result = await setConversationArchived(conversation(archived), archived);
    expect(result.changed).toBe(false);
    expect(result.conversation.extra.presentation_revision).toBe(4);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it.each([true, false])(
    'reconciles a lost no-op without a new revision or mutation for desired archive=%s',
    async (archived) => {
      const fetchMock = vi
        .fn()
        .mockRejectedValueOnce(new TypeError('reply lost'))
        .mockResolvedValueOnce(json(conversation(archived)));
      vi.stubGlobal('fetch', fetchMock);
      const result = await setConversationArchived(conversation(archived), archived);
      expect(result).toMatchObject({ changed: false, acknowledgement: 'reconciled' });
      expect(result.conversation.extra.presentation_revision).toBe(4);
      expect(fetchMock.mock.calls.map((call) => call[1].method)).toEqual(['POST', 'GET']);
    }
  );

  it('reconciles a committed archive followed by notification failure through one read, without a fabricated Undo', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ detail: 'notification unavailable' }, 500))
      .mockResolvedValueOnce(json(conversation(true, 5)));
    vi.stubGlobal('fetch', fetchMock);
    const result = await runConversationArchive(conversation(), true);
    expect(result).toMatchObject({ changed: false, acknowledgement: 'reconciled' });
    expect(fetchMock.mock.calls.map((call) => call[1].method)).toEqual(['POST', 'GET']);
    expect(getArchiveOperationsSnapshot().pending.size).toBe(0);
    expect(getArchiveOperationsSnapshot().notices).toEqual([]);
  });

  it('does not mask an explicit stale-intent rejection by rereading or resubmitting', async () => {
    const fetchMock = vi.fn(async () => json({ code: 'CONVERSATION_PRESENTATION_STALE' }, 409));
    vi.stubGlobal('fetch', fetchMock);
    await expect(runConversationArchive(conversation(), true)).rejects.toMatchObject({ status: 409 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(getArchiveOperationsSnapshot().pending.size).toBe(0);
    expect(getArchiveOperationsSnapshot().notices).toEqual([]);
  });

  it.each(['incarnation', 'revision', 'identity', 'state'])(
    'does not adopt a contradictory %s from an acknowledgement or readback',
    async (field) => {
      const wrong = conversation(true, 5);
      if (field === 'incarnation') wrong.extra.frame_incarnation_id = 'replacement';
      if (field === 'revision') wrong.extra.presentation_revision = 7;
      if (field === 'identity') wrong.id = 'frame-b';
      if (field === 'state') delete wrong.extra.archived_at;
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(json({ conversation: wrong, changed: true }))
        .mockResolvedValueOnce(json(wrong));
      vi.stubGlobal('fetch', fetchMock);
      await expect(setConversationArchived(conversation(), true)).rejects.toThrow();
      expect(fetchMock.mock.calls.map((call) => call[1].method)).toEqual(['POST', 'GET']);
    }
  );

  it('fences a delayed acknowledgement after account replacement without a GET in the next account', async () => {
    let finish!: (value: Response) => void;
    const fetchMock = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        })
    );
    vi.stubGlobal('fetch', fetchMock);
    const pending = runConversationArchive(conversation(), true);
    await Promise.resolve();
    setRendererAccountOwner('replacement-owner');
    finish(response(true));
    await expect(pending).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(getArchiveOperationsSnapshot().pending.size).toBe(0);
    expect(getArchiveOperationsSnapshot().notices).toEqual([]);
  });

  it('shares an identical pending intent across consumers and refuses an inverse or refreshed-revision mutation', async () => {
    let finish!: (value: Response) => void;
    const fetchMock = vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        })
    );
    vi.stubGlobal('fetch', fetchMock);
    const first = runConversationArchive(conversation(), true);
    expect(runConversationArchive(conversation(), true)).toBe(first);
    await expect(runConversationArchive(conversation(), false)).rejects.toThrow();
    await expect(runConversationArchive(conversation(false, 5), true)).rejects.toThrow();
    await Promise.resolve();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    finish(response(true));
    await first;
    expect(getArchiveOperationsSnapshot().pending.size).toBe(0);
    expect(getArchiveOperationsSnapshot().notices).toHaveLength(1);
  });

  it('bounds an unresponsive POST, performs only one readback and releases the shared pending reservation', async () => {
    vi.useFakeTimers();
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(
        (_url: string, options: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            options.signal!.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {
              once: true,
            });
          })
      )
      .mockResolvedValueOnce(json(conversation(true, 5)));
    vi.stubGlobal('fetch', fetchMock);
    const result = runConversationArchive(conversation(), true);
    await vi.advanceTimersByTimeAsync(15_001);
    await expect(result).resolves.toMatchObject({ acknowledgement: 'reconciled', changed: false });
    expect(fetchMock.mock.calls.map((call) => call[1].method)).toEqual(['POST', 'GET']);
    expect(getArchiveOperationsSnapshot().pending.size).toBe(0);
    expect(getArchiveOperationsSnapshot().notices).toEqual([]);
  });

  it('keeps an in-flight Undo across the notice deadline and shows its failure using only the original CAS', async () => {
    vi.useFakeTimers();
    let finish!: (value: Response) => void;
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(response(true))
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          })
      );
    vi.stubGlobal('fetch', fetchMock);
    await runConversationArchive(conversation(), true);
    const notice = getArchiveOperationsSnapshot().notices[0];
    await vi.advanceTimersByTimeAsync(7_900);
    const undone = undoConversationArchive(notice.key);
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(200);
    expireArchiveUndo();
    expect(getArchiveOperationsSnapshot().notices).toHaveLength(1);
    expect(getArchiveOperationsSnapshot().undoing.has(notice.key)).toBe(true);
    finish(json({ code: 'CONVERSATION_PRESENTATION_STALE' }, 409));
    await expect(undone).rejects.toMatchObject({ status: 409 });
    const failed = getArchiveOperationsSnapshot().notices[0];
    expect(failed.failed).toBe(true);
    expect(failed.conversation.extra.presentation_revision).toBe(5);
    expect(bodyOf(fetchMock, 1)).toEqual({
      archived: false,
      expected_incarnation_id: 'inc-a',
      expected_presentation_revision: 5,
    });
    expect(getArchiveOperationsSnapshot().pending.size).toBe(0);
    expect(getArchiveOperationsSnapshot().undoing.size).toBe(0);
    // As in the reference store, the failure can be displayed for eight seconds;
    // no automatic retry or refreshed authority is produced during that period.
    await vi.advanceTimersByTimeAsync(ARCHIVE_UNDO_DURATION_MS + 1);
    expireArchiveUndo();
    expect(getArchiveOperationsSnapshot().notices).toEqual([]);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('expires an unused Undo without issuing any restore request', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn(async () => response(true));
    vi.stubGlobal('fetch', fetchMock);
    await runConversationArchive(conversation(), true);
    const key = getArchiveOperationsSnapshot().notices[0].key;
    await vi.advanceTimersByTimeAsync(ARCHIVE_UNDO_DURATION_MS + 1);
    expireArchiveUndo();
    await undoConversationArchive(key);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(getArchiveOperationsSnapshot().notices).toEqual([]);
  });

  it('does not use historical task status as a permanent archive blocker, while real active input remains blocked', () => {
    expect(isConversationArchiveUnavailable({ ...conversation(), status: 'running' })).toBe(false);
    expect(isConversationArchiveUnavailable(conversation(), true)).toBe(true);
    const paused = {
      state: 'paused',
      is_processing: false,
      pending_confirmations: 0,
      has_task: true,
      can_send_message: true,
      turn_id: null,
    } as const;
    expect(isConversationArchiveUnavailable({ ...conversation(), runtime: paused })).toBe(false);
    for (const state of [
      'running',
      'starting',
      'cancelling',
      'waiting_input',
      'waiting_approval',
      'waiting_confirmation',
    ] as const)
      expect(isConversationArchiveUnavailable({ ...conversation(), runtime: { ...paused, state } })).toBe(true);
  });
});
