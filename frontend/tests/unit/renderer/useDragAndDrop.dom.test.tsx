import { act, cleanup, renderHook } from '@testing-library/react';
import type { DragEndEvent, DragStartEvent } from '@dnd-kit/core';
import type { TChatConversation } from '@/common/config/storage';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const mock = vi.hoisted(() => ({
  scope: 'owner-a',
  reset: undefined as undefined | (() => void),
  reorder: vi.fn(),
  error: vi.fn(),
  emit: vi.fn(),
}));
vi.mock('@/renderer/services/rendererAccountScope', () => ({
  getRendererAccountScopeToken: () => mock.scope,
  registerRendererAccountReset: (_name: string, callback: () => void) => {
    mock.reset = callback;
    return () => {
      mock.reset = undefined;
    };
  },
}));
vi.mock('@/renderer/services/runtime/conversationPresentation', () => ({ reorderPinnedConversations: mock.reorder }));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({ useLayoutContext: () => ({ isMobile: false }) }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { emit: mock.emit } }));
vi.mock('@arco-design/web-react', () => ({ Message: { error: mock.error } }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import { useDragAndDrop } from '@/renderer/pages/conversation/GroupedHistory/hooks/useDragAndDrop';

const rows = ['first', 'second'].map((id): TChatConversation => ({
  id,
  name: id,
  type: 'acp',
  created_at: 1,
  modified_at: 1,
  extra: { backend: 'xscience', pinned: true },
}));
const end = { active: { id: 'first' }, over: { id: 'second' } } as DragEndEvent;
const params = { pinnedConversations: rows, batchMode: false, collapsed: false, pinnedListComplete: true };

beforeEach(() => {
  vi.clearAllMocks();
  mock.reorder.mockReset();
  mock.scope = 'owner-a';
});
afterEach(cleanup);

it('clears the dragged conversation when the account generation changes', () => {
  const { result } = renderHook(() => useDragAndDrop(params));
  act(() => result.current.handleDragStart({ active: { id: 'first' } } as DragStartEvent));
  expect(result.current.activeId).toBe('first');
  act(() => {
    mock.scope = 'owner-b';
    mock.reset?.();
  });
  expect(result.current.activeId).toBeNull();
  expect(result.current.activeConversation).toBeNull();
});

it('does not let an older account completion unlock a newer reorder', async () => {
  const releases: Array<() => void> = [];
  mock.reorder.mockImplementation(() => new Promise<void>((resolve) => releases.push(resolve)));
  const { result } = renderHook(() => useDragAndDrop(params));
  let first!: Promise<void>;
  act(() => {
    first = result.current.handleDragEnd(end);
  });
  act(() => {
    mock.scope = 'owner-b';
    mock.reset?.();
  });
  let second!: Promise<void>;
  act(() => {
    second = result.current.handleDragEnd(end);
  });
  expect(mock.reorder).toHaveBeenCalledTimes(2);
  await act(async () => {
    releases[0]();
    await first;
  });
  expect(result.current.isDragEnabled).toBe(false);
  let third!: Promise<void>;
  act(() => {
    third = result.current.handleDragEnd(end);
  });
  const callsWhilePending = mock.reorder.mock.calls.length;
  await act(async () => {
    releases.slice(1).forEach((release) => release());
    await Promise.all([second, third]);
  });
  expect(callsWhilePending).toBe(2);
  expect(mock.error).not.toHaveBeenCalled();
});
