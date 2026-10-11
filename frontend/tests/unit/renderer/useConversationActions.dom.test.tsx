import { act, renderHook, waitFor } from '@testing-library/react';
import { Message } from '@arco-design/web-react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { navigateMock, prefetchConversationRouteMock, warmConversationNavigationMock } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
  prefetchConversationRouteMock: vi.fn().mockResolvedValue(undefined),
  warmConversationNavigationMock: vi.fn().mockResolvedValue(undefined),
}));
const moveRuntime = vi.hoisted(() => ({ projects: vi.fn(), move: vi.fn() }));

afterEach(() => vi.restoreAllMocks());

vi.mock('@/renderer/pages/conversation/GroupedHistory/conversationActionRuntime', () => ({
  loadSynonBiomedProjects: moveRuntime.projects,
  moveSynonBiomedSession: moveRuntime.move,
}));

vi.mock('react-router', () => ({
  useNavigate: () => navigateMock,
  useParams: () => ({ id: 'conversation-current' }),
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@/renderer/hooks/context/AuthContext', () => ({
  useAuth: () => ({ user: { id: 'owner-a' } }),
}));
vi.mock('@/renderer/pages/conversation/conversationRoute', () => ({
  prefetchConversationRoute: prefetchConversationRouteMock,
  warmConversationNavigation: warmConversationNavigationMock,
}));
vi.mock('@/renderer/pages/conversation/conversationRoutePerformance', () => ({
  beginConversationRoutePerformance: vi.fn(),
}));
vi.mock('@/renderer/utils/ui/focus', () => ({
  blockMobileInputFocus: vi.fn(),
  blurActiveElement: vi.fn(),
}));

import type { TChatConversation } from '@/common/config/storage';
import { useConversationActions } from '@/renderer/pages/conversation/GroupedHistory/hooks/useConversationActions';

const conversation: TChatConversation = {
  id: 'conversation-target',
  type: 'acp',
  name: 'Target',
  desc: '',
  created_at: 1,
  modified_at: 1,
  status: 'finished',
  extra: { backend: 'synonbiomed' },
};

describe('useConversationActions route intent', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    moveRuntime.projects.mockReset().mockResolvedValue([]);
    moveRuntime.move.mockReset().mockResolvedValue(undefined);
  });

  it('keeps pointer intent lightweight and starts exactly the mounted message request on click', async () => {
    let releaseWarmup: (() => void) | undefined;
    warmConversationNavigationMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          releaseWarmup = resolve;
        })
    );
    const { result } = renderHook(() =>
      useConversationActions({
        batchMode: false,
        selectedConversationIds: new Set(),
        setSelectedConversationIds: vi.fn(),
        toggleSelectedConversation: vi.fn(),
      })
    );

    act(() => result.current.handleConversationPrefetch(conversation));
    expect(prefetchConversationRouteMock).toHaveBeenCalledOnce();
    expect(warmConversationNavigationMock).not.toHaveBeenCalled();

    act(() => result.current.handleConversationClick(conversation));
    expect(warmConversationNavigationMock).toHaveBeenCalledOnce();
    expect(warmConversationNavigationMock).toHaveBeenCalledWith({
      ownerId: 'owner-a',
      conversationId: 'conversation-target',
      summary: conversation,
    });
    expect(navigateMock).toHaveBeenCalledWith('/conversation/conversation-target');
    expect(releaseWarmup).toBeTypeOf('function');
    await act(async () => releaseWarmup?.());
  });
});

const renderActions = () =>
  renderHook(() =>
    useConversationActions({
      batchMode: false,
      selectedConversationIds: new Set(),
      setSelectedConversationIds: vi.fn(),
      toggleSelectedConversation: vi.fn(),
    })
  );

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((accept, decline) => {
    resolve = accept;
    reject = decline;
  });
  return { promise, resolve, reject };
};

describe('move dialog read ownership', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    moveRuntime.projects.mockReset().mockResolvedValue([]);
    moveRuntime.move.mockReset().mockResolvedValue(undefined);
    vi.spyOn(Message, 'success').mockImplementation(() => undefined as never);
  });

  it('closes and aborts a pending catalogue read', async () => {
    const read = deferred<[]>();
    moveRuntime.projects.mockReturnValueOnce(read.promise);
    const { result } = renderActions();
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.handleMoveStart(conversation);
    });
    await waitFor(() => expect(moveRuntime.projects).toHaveBeenCalledOnce());
    act(() => result.current.handleMoveCancel());
    expect(result.current.moveTarget).toBeNull();
    expect(result.current.moveLoading).toBe(false);
    expect(moveRuntime.projects.mock.calls[0]?.[0]?.signal.aborted).toBe(true);
    await act(async () => {
      read.resolve([]);
      await pending;
    });
    expect(result.current.moveTarget).toBeNull();
  });

  it.each(['success', 'failure'] as const)(
    'ignores a cancelled read %s after another dialog opens',
    async (outcome) => {
      const first = deferred<unknown[]>();
      const second = deferred<unknown[]>();
      moveRuntime.projects.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
      const { result } = renderActions();
      let older!: Promise<void>;
      let newer!: Promise<void>;
      act(() => {
        older = result.current.handleMoveStart(conversation);
      });
      await waitFor(() => expect(moveRuntime.projects).toHaveBeenCalledTimes(1));
      act(() => result.current.handleMoveCancel());
      act(() => {
        newer = result.current.handleMoveStart({ ...conversation, id: 'conversation-next' });
      });
      await waitFor(() => expect(moveRuntime.projects).toHaveBeenCalledTimes(2));
      await act(async () => {
        if (outcome === 'success') first.resolve([{ projectId: 'stale', name: 'Old catalogue' }]);
        else first.reject(new Error('Old request failed'));
        await older;
      });
      expect(result.current.moveTarget?.id).toBe('conversation-next');
      expect(result.current.moveLoading).toBe(true);
      expect(result.current.moveProjects).toEqual([]);
      expect(result.current.moveError).toBeNull();
      await act(async () => {
        second.resolve([{ projectId: 'current', name: 'Current catalogue' }]);
        await newer;
      });
      expect(result.current.moveProjects).toEqual([{ projectId: 'current', name: 'Current catalogue' }]);
      expect(result.current.moveLoading).toBe(false);
    }
  );

  it('aborts an owned catalogue read on unmount', async () => {
    const read = deferred<[]>();
    moveRuntime.projects.mockReturnValueOnce(read.promise);
    const { result, unmount } = renderActions();
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.handleMoveStart(conversation);
    });
    await waitFor(() => expect(moveRuntime.projects).toHaveBeenCalledOnce());
    unmount();
    expect(moveRuntime.projects.mock.calls[0]?.[0]?.signal.aborted).toBe(true);
    await act(async () => {
      read.resolve([]);
      await pending;
    });
  });

  it('keeps an in-flight move owned until the server settles', async () => {
    moveRuntime.projects.mockResolvedValue([{ projectId: 'target-project', name: 'Target project' }]);
    const write = deferred<void>();
    moveRuntime.move.mockReturnValueOnce(write.promise);
    const { result } = renderActions();
    await act(async () => {
      await result.current.handleMoveStart(conversation);
    });
    let saving!: Promise<void>;
    act(() => {
      saving = result.current.handleMoveConfirm('target-project');
    });
    await waitFor(() => expect(moveRuntime.move).toHaveBeenCalledOnce());
    act(() => result.current.handleMoveCancel());
    expect(result.current.moveTarget?.id).toBe(conversation.id);
    expect(result.current.moveLoading).toBe(true);
    await act(async () => {
      write.resolve();
      await saving;
    });
    expect(result.current.moveTarget).toBeNull();
    expect(moveRuntime.move).toHaveBeenCalledWith(conversation.id, 'target-project');
  });

  it('keeps a failed move available for retry and prevents duplicate writes', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    moveRuntime.projects.mockResolvedValue([{ projectId: 'target-project', name: 'Target project' }]);
    const write = deferred<void>();
    moveRuntime.move.mockReturnValueOnce(write.promise).mockResolvedValueOnce(undefined);
    const { result } = renderActions();
    await act(async () => {
      await result.current.handleMoveStart(conversation);
    });
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.handleMoveConfirm('target-project');
      void result.current.handleMoveConfirm('target-project');
    });
    await waitFor(() => expect(moveRuntime.move).toHaveBeenCalledOnce());
    await act(async () => {
      write.reject(new Error('Write failed'));
      await pending;
    });
    expect(result.current.moveTarget?.id).toBe(conversation.id);
    expect(result.current.moveError).toBe('conversation.history.sessionMove.failed');
    expect(result.current.moveLoading).toBe(false);
    await act(async () => {
      await result.current.handleMoveConfirm('target-project');
    });
    expect(moveRuntime.move).toHaveBeenCalledTimes(2);
    expect(result.current.moveTarget).toBeNull();
  });
});
