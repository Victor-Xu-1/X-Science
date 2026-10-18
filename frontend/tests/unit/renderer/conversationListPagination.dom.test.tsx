import { act, renderHook, waitFor } from '@testing-library/react';
import type { TChatConversation } from '@/common/config/storage';
import { expect, it, vi } from 'vitest';
const mock = vi.hoisted(() => ({
  load: vi.fn(),
  listChanged: undefined as undefined | ((value: unknown) => void),
  refresh: undefined as undefined | (() => void),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    database: { getUserConversations: { invoke: mock.load } },
    conversation: {
      listChanged: {
        on: (callback: (value: unknown) => void) => {
          mock.listChanged = callback;
        },
      },
      userCreated: { on: vi.fn() },
      responseStream: { on: vi.fn() },
      turnCompleted: { on: vi.fn() },
    },
    runtime: { statusChanged: { on: vi.fn() } },
    application: { writeRendererLog: { invoke: vi.fn(async () => undefined) } },
  },
}));
vi.mock('@/renderer/utils/emitter', () => ({
  addEventListener: (name: string, callback: () => void) => {
    if (name === 'chat.history.refresh') mock.refresh = callback;
  },
}));
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { useConversationListSync } from '@/renderer/pages/conversation/GroupedHistory/hooks/useConversationListSync';

const row = (id: string, pinned: boolean): TChatConversation => ({
  id,
  name: id,
  type: 'acp',
  created_at: 1,
  modified_at: 1,
  extra: { backend: 'xscience', pinned, presentation_revision: 1 },
});
const page = (items: TChatConversation[], cursor: string | null = null) => ({
  items,
  total: items.length,
  has_more: cursor !== null,
  next_cursor: cursor,
});

it('keeps pinned pages independent, retries the failed cursor, and fences old-account reads', async () => {
  setRendererAccountOwner('pagination-owner-a');
  mock.load.mockImplementation(async ({ section }: { section: string }) =>
    section === 'pinned' ? page([row('old-pin', true)], 'pin-page-2') : page([row('recent', false)])
  );
  const { result, unmount } = renderHook(useConversationListSync);
  await waitFor(() => expect(result.current.conversations).toHaveLength(2));
  expect(mock.load.mock.calls.map(([input]) => input.section).sort()).toEqual(['pinned', 'recent']);
  expect(result.current.hasMorePinned).toBe(true);
  mock.load.mockRejectedValueOnce(new Error('temporary pin read failure'));
  act(() => result.current.loadMorePinned());
  await waitFor(() => expect(result.current.pinnedLoadError).toBe(true));
  expect(result.current.conversations.map((item) => item.id)).toContain('old-pin');
  mock.load.mockResolvedValueOnce(page([row('next-pin', true)]));
  act(() => result.current.retryPinned());
  await waitFor(() => expect(result.current.conversations).toHaveLength(3));
  expect(mock.load.mock.calls.at(-1)?.[0]).toEqual({ limit: 100, section: 'pinned', cursor: 'pin-page-2' });
  expect(result.current.pinnedLoadError).toBe(false);
  expect(result.current.hasMorePinned).toBe(false);

  // A second client can change presentation while the runtime row is active.
  const count = mock.load.mock.calls.length;
  act(() => mock.listChanged?.({ conversation_id: 'recent', action: 'updated', presentation_changed: true }));
  await waitFor(() => expect(mock.load.mock.calls.length).toBeGreaterThan(count));
  await waitFor(() => expect(result.current.pinnedLoading).toBe(false));

  let resolve!: (value: ReturnType<typeof page>) => void;
  mock.load.mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      })
  );
  act(() => result.current.loadMorePinned());
  await waitFor(() => expect(result.current.pinnedLoading).toBe(true));
  const beforeAccountRefresh = mock.load.mock.calls.length;
  act(() => setRendererAccountOwner('pagination-owner-b'));
  mock.load.mockResolvedValue(page([]));
  await act(async () => {
    resolve(page([row('must-not-cross-account', true)]));
  });
  expect(result.current.conversations.map((item) => item.id)).not.toContain('must-not-cross-account');
  await waitFor(() => expect(mock.load.mock.calls.length).toBeGreaterThan(beforeAccountRefresh));
  await waitFor(() => expect(result.current.pinnedLoading).toBe(false));
  unmount();
});
