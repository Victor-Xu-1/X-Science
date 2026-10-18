import { act, renderHook, waitFor } from '@testing-library/react';
import { Message } from '@arco-design/web-react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { navigateMock, prefetchConversationRouteMock, warmConversationNavigationMock } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
  prefetchConversationRouteMock: vi.fn().mockResolvedValue(undefined),
  warmConversationNavigationMock: vi.fn().mockResolvedValue(undefined),
}));
const moveRuntime = vi.hoisted(() => ({ projects: vi.fn(), move: vi.fn() }));
const notebookRuntime = vi.hoisted(() => ({
  read: vi.fn(),
  download: vi.fn(),
  runtimeView: vi.fn(),
  ownerId: 'owner-a',
}));

afterEach(() => vi.restoreAllMocks());

vi.mock('@/renderer/pages/conversation/GroupedHistory/conversationActionRuntime', () => ({
  loadXScienceProjects: moveRuntime.projects,
  moveXScienceSession: moveRuntime.move,
  loadXScienceExecutionLogPage: notebookRuntime.read,
  downloadFileFromUrl: notebookRuntime.download,
  getXScienceSessionExportUrl: (id: string) => `/api/frames/${id}/export`,
  sanitizeFileName: (name: string) => name,
}));
vi.mock('@/renderer/pages/conversation/runtime/conversationRuntimeViewStore', () => ({
  getConversationRuntimeViewSnapshot: notebookRuntime.runtimeView,
}));

vi.mock('react-router', () => ({
  useNavigate: () => navigateMock,
  useParams: () => ({ id: 'conversation-current' }),
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@/renderer/hooks/context/AuthContext', () => ({
  useAuth: () => ({ user: { id: notebookRuntime.ownerId } }),
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
import type { XScienceExecutionLogPage } from '@/renderer/services/xScienceRuntimeOperations';
import { useConversationActions } from '@/renderer/pages/conversation/GroupedHistory/hooks/useConversationActions';

const conversation: TChatConversation = {
  id: 'conversation-target',
  type: 'acp',
  name: 'Target',
  desc: '',
  created_at: 1,
  modified_at: 1,
  status: 'finished',
  extra: { backend: 'xscience' },
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

const notebookPage = (ids: string[], nextBefore: string | null = null): XScienceExecutionLogPage => ({
  records: ids.map((id) => ({ kind: 'save', id, label: id, actor: null, detail: null, at: null })),
  nextBefore,
  total: ids.length,
});

describe('session notebook read ownership and export', () => {
  beforeEach(() => {
    notebookRuntime.ownerId = 'owner-a';
    notebookRuntime.read.mockReset().mockResolvedValue(notebookPage([]));
    notebookRuntime.download.mockReset().mockResolvedValue(undefined);
    notebookRuntime.runtimeView.mockReset().mockReturnValue(undefined);
    vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  it.each(['success', 'failure'] as const)('ignores an old %s across A to B to A openings', async (outcome) => {
    const first = deferred<XScienceExecutionLogPage>();
    const second = deferred<XScienceExecutionLogPage>();
    notebookRuntime.read
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
      .mockResolvedValueOnce(notebookPage(['current']));
    const { result } = renderActions();
    let older!: Promise<void>;
    let middle!: Promise<void>;
    act(() => {
      older = result.current.handleViewNotebook(conversation);
    });
    await waitFor(() => expect(notebookRuntime.read).toHaveBeenCalledTimes(1));
    act(() => {
      middle = result.current.handleViewNotebook({ ...conversation, id: 'other' });
    });
    await waitFor(() => expect(notebookRuntime.read).toHaveBeenCalledTimes(2));
    await act(async () => {
      await result.current.handleViewNotebook(conversation);
    });
    expect(notebookRuntime.read.mock.calls[0][2].signal.aborted).toBe(true);
    expect(notebookRuntime.read.mock.calls[1][2].signal.aborted).toBe(true);
    await act(async () => {
      if (outcome === 'success') first.resolve(notebookPage(['stale']));
      else first.reject(new Error('stale failure'));
      second.resolve(notebookPage(['middle']));
      await Promise.all([older, middle]);
    });
    expect(result.current.notebookTarget?.id).toBe(conversation.id);
    expect(result.current.notebookRecords.map((record) => record.id)).toEqual(['current']);
    expect(result.current.notebookLoading).toBe(false);
    expect(result.current.notebookError).toBeNull();
  });

  it('fences an old account even after switching back to the same account', async () => {
    const first = deferred<XScienceExecutionLogPage>();
    notebookRuntime.read
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(notebookPage(['new-account-opening']));
    const { result, rerender } = renderActions();
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.handleViewNotebook(conversation);
    });
    await waitFor(() => expect(notebookRuntime.read).toHaveBeenCalledTimes(1));
    notebookRuntime.ownerId = 'owner-b';
    rerender();
    expect(result.current.notebookTarget).toBeNull();
    expect(notebookRuntime.read.mock.calls[0][2].signal.aborted).toBe(true);
    notebookRuntime.ownerId = 'owner-a';
    rerender();
    await act(async () => {
      await result.current.handleViewNotebook(conversation);
    });
    await act(async () => {
      first.resolve(notebookPage(['old-account']));
      await pending;
    });
    expect(result.current.notebookRecords.map((record) => record.id)).toEqual(['new-account-opening']);
  });

  it.each(['close', 'unmount'] as const)('aborts a notebook read on %s', async (action) => {
    const read = deferred<XScienceExecutionLogPage>();
    notebookRuntime.read.mockReturnValueOnce(read.promise);
    const { result, unmount } = renderActions();
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.handleViewNotebook(conversation);
    });
    await waitFor(() => expect(notebookRuntime.read).toHaveBeenCalledTimes(1));
    if (action === 'close') act(() => result.current.handleNotebookClose());
    else unmount();
    expect(notebookRuntime.read.mock.calls[0][2].signal.aborted).toBe(true);
    await act(async () => {
      read.resolve(notebookPage(['late']));
      await pending;
    });
    if (action === 'close') {
      expect(result.current.notebookTarget).toBeNull();
      expect(result.current.notebookLoading).toBe(false);
      expect(result.current.notebookRecords).toEqual([]);
    }
  });

  it('retains records and retries precisely the failed cursor without duplicate reads', async () => {
    const older = deferred<XScienceExecutionLogPage>();
    notebookRuntime.read
      .mockResolvedValueOnce(notebookPage(['recent'], 'older-cursor'))
      .mockReturnValueOnce(older.promise)
      .mockResolvedValueOnce(notebookPage(['old', 'recent']));
    const { result } = renderActions();
    await act(async () => {
      await result.current.handleViewNotebook(conversation);
    });
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.handleNotebookLoadOlder();
      void result.current.handleNotebookLoadOlder();
    });
    await waitFor(() => expect(notebookRuntime.read).toHaveBeenCalledTimes(2));
    expect(result.current.notebookRecords.map((record) => record.id)).toEqual(['recent']);
    await act(async () => {
      older.reject(new Error('page failed'));
      await pending;
    });
    expect(result.current.notebookError).toBe('conversation.history.sessionNotebook.loadFailed');
    await act(async () => {
      await result.current.handleNotebookRetry();
    });
    expect(notebookRuntime.read.mock.calls.map((call) => call.slice(0, 2))).toEqual([
      [conversation.id, null],
      [conversation.id, 'older-cursor'],
      [conversation.id, 'older-cursor'],
    ]);
    expect(result.current.notebookRecords.map((record) => record.id)).toEqual(['old', 'recent']);
    expect(result.current.notebookHasOlder).toBe(false);
    expect(result.current.notebookError).toBeNull();
    expect(notebookRuntime.download).not.toHaveBeenCalled();
  });

  it('retries an initial read and keeps notebook available while running', async () => {
    notebookRuntime.read.mockRejectedValueOnce(new Error('initial failure')).mockResolvedValueOnce(notebookPage([]));
    const { result } = renderActions();
    await act(async () => {
      await result.current.handleViewNotebook({ ...conversation, status: 'running' });
    });
    expect(result.current.notebookError).not.toBeNull();
    await act(async () => {
      await result.current.handleNotebookRetry();
    });
    expect(notebookRuntime.read.mock.calls.map((call) => call[1])).toEqual([null, null]);
    expect(result.current.notebookError).toBeNull();
    expect(result.current.notebookRecords).toEqual([]);
  });

  it('uses the existing export endpoint and blocks current canonical running/waiting state', async () => {
    const { result } = renderActions();
    await act(async () => {
      await result.current.handleExport(conversation);
    });
    expect(notebookRuntime.download).toHaveBeenCalledWith(
      '/api/frames/conversation-target/export',
      'Target.x-science-session.json'
    );
    notebookRuntime.download.mockClear();
    for (const state of ['running', 'waiting_input', 'waiting_confirmation']) {
      notebookRuntime.runtimeView.mockReturnValue({
        hasBackendRuntime: true,
        state,
        isProcessing: state === 'running',
        pendingConfirmations: state === 'waiting_confirmation' ? 1 : 0,
        hasTask: true,
        taskStatus: 'pending',
        canSendMessage: false,
        activeTurnId: conversation.id,
      });
      await act(async () => {
        await result.current.handleExport(conversation);
      });
    }
    expect(notebookRuntime.download).not.toHaveBeenCalled();
    // Admission may begin while the action runtime import is resolving.
    notebookRuntime.runtimeView.mockReset().mockReturnValueOnce(undefined).mockReturnValue({ localSubmitting: true });
    await act(async () => {
      await result.current.handleExport(conversation);
    });
    expect(notebookRuntime.download).not.toHaveBeenCalled();
  });
});

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
