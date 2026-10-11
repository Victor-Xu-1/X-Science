import type { TChatConversation } from '@/common/config/storage';
import { act, fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const state = vi.hoisted(() => ({
  conversations: [] as TChatConversation[],
  pinned: [] as TChatConversation[],
  rowRender: vi.fn(() => ({ isMobile: true })),
  navigate: vi.fn(),
  user: { id: 'test-owner' },
  inactive: () => false,
  noop: vi.fn(),
  collapsedSections: new Set<string>(),
}));

vi.mock('react-router', async () => ({
  ...(await vi.importActual<typeof import('react-router')>('react-router')),
  useNavigate: () => state.navigate,
  useParams: () => ({ projectId: 'project-a' }),
  useLocation: () => ({ pathname: '/projects/project-a', state: null }),
}));
vi.mock('@/renderer/hooks/context/AuthContext', () => ({ useAuth: () => ({ user: state.user }) }));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({ useLayoutContext: state.rowRender }));
vi.mock('@/renderer/pages/conversation/conversationRoute', () => ({
  prefetchConversationRoute: async () => undefined,
  warmConversationNavigation: async () => undefined,
}));
vi.mock('@/renderer/pages/conversation/GroupedHistory/ProjectHistorySection', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/GroupedHistory/BatchSelectionPanel', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/GroupedHistory/hooks/useDragAndDrop', () => ({
  useDragAndDrop: () => ({
    sensors: [],
    activeId: null,
    activeConversation: null,
    handleDragStart: state.noop,
    handleDragEnd: state.noop,
    handleDragCancel: state.noop,
    isDragEnabled: false,
  }),
}));
vi.mock('@/renderer/pages/conversation/GroupedHistory/hooks/useConversations', () => ({
  useConversations: () => ({
    conversations: state.conversations,
    pinnedConversations: state.pinned,
    timelineSections: [
      {
        timeline: 'Today',
        items: state.conversations.map((conversation) => ({ type: 'conversation', time: 1, conversation })),
      },
    ],
    isConversationGenerating: state.inactive,
    hasCompletionUnread: state.inactive,
    collapsedSections: state.collapsedSections,
    toggleSection: state.noop,
    hasMoreConversations: false,
    isLoadingMoreConversations: false,
    loadMoreConversations: state.noop,
  }),
}));

import WorkspaceGroupedHistory from '@/renderer/pages/conversation/GroupedHistory';

const conversation = (id: string, name: string): TChatConversation => ({
  id,
  name,
  type: 'acp',
  desc: '',
  created_at: 1,
  modified_at: 1,
  status: 'running',
  extra: { backend: 'synonbiomed', project_id: 'project-a', frame_status: 'running' },
});

describe('GroupedHistory production row memoization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.removeItem('synonbiomed.active-project-id');
    state.conversations = [conversation('first', 'First task'), conversation('neighbor', 'Neighbor task')];
    state.pinned = [];
  });

  it('renders only the changed real row when a neighboring task snapshot updates', async () => {
    const onSessionClick = vi.fn();
    const { rerender } = await renderWithI18n(<WorkspaceGroupedHistory onSessionClick={onSessionClick} />, 'en-US');
    const neighbor = screen.getByRole('button', { name: 'Neighbor task', exact: true });
    const initialRenders = state.rowRender.mock.calls.length;
    expect(initialRenders).toBeGreaterThanOrEqual(2);

    state.conversations = [
      {
        ...state.conversations[0],
        name: 'First task completed',
        status: 'finished',
        extra: { ...state.conversations[0].extra, frame_status: 'completed' },
      },
      state.conversations[1],
    ];
    rerender(<WorkspaceGroupedHistory onSessionClick={onSessionClick} />);

    expect(screen.getByRole('button', { name: 'First task completed', exact: true })).toBeInTheDocument();
    expect(screen.getByTestId('conversation-success-first')).toBeInTheDocument();
    expect(screen.queryByTestId('conversation-running-first')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Neighbor task', exact: true })).toBe(neighbor);
    // The production row calls this hook once per render. Its memoized neighbor
    // keeps all props, including behaviorful handlers, rather than ignoring them.
    expect(state.rowRender.mock.calls.length - initialRenders).toBe(1);
  });

  it('keeps pinned navigation and task selection current across batch-mode and callback changes', async () => {
    const pinned = conversation('pinned', 'Pinned task');
    state.pinned = [pinned];
    const firstCallback = vi.fn();
    const latestCallback = vi.fn();
    const { rerender } = await renderWithI18n(
      <WorkspaceGroupedHistory batchMode={false} onSessionClick={firstCallback} />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'First task', exact: true }));
    expect(state.navigate).toHaveBeenLastCalledWith('/conversation/first');
    expect(firstCallback).toHaveBeenCalledOnce();
    state.navigate.mockClear();

    await act(async () => {
      rerender(<WorkspaceGroupedHistory batchMode onSessionClick={latestCallback} />);
    });
    const task = screen.getByRole('button', { name: 'First task', exact: true });
    expect(task).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(task);
    expect(task).toHaveAttribute('aria-pressed', 'true');
    expect(state.navigate).not.toHaveBeenCalled();
    expect(latestCallback).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Pinned task', exact: true }));
    expect(state.navigate).toHaveBeenLastCalledWith('/conversation/pinned');
    expect(latestCallback).toHaveBeenCalledOnce();
    expect(firstCallback).toHaveBeenCalledOnce();

    state.navigate.mockClear();
    rerender(<WorkspaceGroupedHistory batchMode={false} onSessionClick={latestCallback} />);
    expect(screen.getByRole('button', { name: 'First task', exact: true })).not.toHaveAttribute('aria-pressed');
    fireEvent.click(screen.getByRole('button', { name: 'First task', exact: true }));
    expect(state.navigate).toHaveBeenLastCalledWith('/conversation/first');
    expect(latestCallback).toHaveBeenCalledTimes(2);
  });
});
