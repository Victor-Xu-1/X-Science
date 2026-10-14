import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import type { TChatConversation } from '@/common/config/storage';

const swrMock = vi.fn();
const navigateMock = vi.fn();
const mutateMock = vi.fn(async () => undefined);
const conversationGetMock = vi.fn();
const closePreviewMock = vi.fn();
const syncTitleMock = vi.fn();
let routeConversationId = 'frame-missing';
let listChangedListener:
  | ((event: { conversation_id: string; action: 'created' | 'updated' | 'deleted' }) => void)
  | undefined;
let reconnectedListener: (() => void) | undefined;

vi.mock('swr', () => ({
  default: (...args: unknown[]) => swrMock(...args),
}));

vi.mock('react-router', () => ({
  useNavigate: () => navigateMock,
  useParams: () => ({ id: routeConversationId }),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      get: { invoke: (...args: unknown[]) => conversationGetMock(...args) },
      listChanged: {
        on: (listener: typeof listChangedListener) => {
          listChangedListener = listener;
          return () => {
            if (listChangedListener === listener) listChangedListener = undefined;
          };
        },
      },
    },
  },
}));

vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => ({ closePreview: closePreviewMock }),
}));

vi.mock('@/renderer/hooks/chat/useAutoTitle', () => ({
  useAutoTitle: () => ({ syncTitleFromHistory: syncTitleMock }),
}));

vi.mock('@/renderer/hooks/context/AuthContext', () => ({
  useAuth: () => ({ user: { id: 'owner-test' } }),
}));

vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  useRealtime: () => ({
    runtime: {
      subscribeReconnected: (listener: () => void) => {
        reconnectedListener = listener;
        return () => {
          if (reconnectedListener === listener) reconnectedListener = undefined;
        };
      },
    },
  }),
}));

vi.mock('@/renderer/pages/conversation/components/ChatConversation', () => ({
  default: ({ conversation }: { conversation: { name: string } }) => <div>{conversation.name}</div>,
}));

import ChatConversationIndex from '@/renderer/pages/conversation';
import {
  clearConversationReadCaches,
  rememberConversationRouteSnapshot,
} from '@/renderer/pages/conversation/utils/conversationCache';

describe('ChatConversationIndex unavailable states', () => {
  beforeEach(() => {
    clearConversationReadCaches();
    swrMock.mockReset();
    navigateMock.mockClear();
    mutateMock.mockReset();
    mutateMock.mockResolvedValue(undefined);
    conversationGetMock.mockReset();
    closePreviewMock.mockClear();
    syncTitleMock.mockClear();
    routeConversationId = 'frame-missing';
    listChangedListener = undefined;
    reconnectedListener = undefined;
  });

  afterEach(() => vi.restoreAllMocks());

  it('shows a compact loading state while the initial conversation request is loading', async () => {
    swrMock.mockReturnValue({
      data: undefined,
      error: undefined,
      isLoading: true,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    expect(screen.getByTestId('conversation-loading-surface')).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByTestId('conversation-loading-surface')).toHaveAttribute('role', 'status');
    expect(screen.getByTestId('conversation-loading-indicator')).toBeInTheDocument();
    expect(screen.queryByAltText('X-Science')).not.toBeInTheDocument();
    expect(screen.queryByTestId('conversation-loading-composer')).not.toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('replaces a missing conversation with the clean new-task route', async () => {
    swrMock.mockReturnValue({
      data: null,
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    await expectGuidReplacement();
    expect(closePreviewMock).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('conversation-load-error')).not.toBeInTheDocument();
  });

  it('keeps a transient initial failure mounted and revalidates on backend reconnect', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    swrMock.mockReturnValue({
      data: undefined,
      error: new Error('provider failed with key sk-ant-api03-shouldNotLeak123456'),
      isLoading: false,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    expect(navigateMock).not.toHaveBeenCalled();
    expect(closePreviewMock).not.toHaveBeenCalled();
    expect(screen.queryByTestId('conversation-loading-surface')).not.toBeInTheDocument();
    expect(screen.getByTestId('conversation-load-error')).toHaveAttribute('role', 'alert');
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
    expect(screen.queryByText(/shouldNotLeak/)).not.toBeInTheDocument();
    expect(JSON.stringify(warn.mock.calls)).toContain('[REDACTED_KEY]');
    expect(JSON.stringify(warn.mock.calls)).not.toContain('shouldNotLeak');

    reconnectedListener?.();
    expect(mutateMock).toHaveBeenCalledTimes(1);
  });

  it('keeps cached conversation content visible when background revalidation fails', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    swrMock.mockReturnValue({
      data: { id: 'frame-cached', name: 'Cached session' },
      error: new Error('temporary network failure'),
      isLoading: false,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    expect(screen.getByText('Cached session')).toBeInTheDocument();
    expect(screen.queryByTestId('conversation-load-error')).not.toBeInTheDocument();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it.each([
    ['en-US', 'Failed to load session', 'Retry'],
    ['zh-CN', '会话加载失败', '重试'],
  ] as const)(
    'retries the same failed detail request in %s without leaving the route',
    async (language, title, retry) => {
      vi.spyOn(console, 'warn').mockImplementation(() => undefined);
      swrMock.mockReturnValue({
        data: undefined,
        error: new Error('temporary failure'),
        isLoading: true,
        isValidating: false,
        mutate: mutateMock,
      });
      await renderWithI18n(<ChatConversationIndex />, language);

      expect(screen.getByRole('alert')).toHaveTextContent(title);
      fireEvent.click(screen.getByRole('button', { name: retry }));

      expect(mutateMock).toHaveBeenCalledExactlyOnceWith();
      expect(navigateMock).not.toHaveBeenCalled();
      expect(closePreviewMock).not.toHaveBeenCalled();
    }
  );

  it('uses SWR validation state to prevent duplicate retries and restores the conversation on success', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const request = {
      data: undefined as TChatConversation | undefined,
      error: new Error('temporary failure') as Error | undefined,
      isValidating: true,
      mutate: mutateMock,
    };
    swrMock.mockImplementation(() => request);
    const view = await renderWithI18n(<ChatConversationIndex />, 'en-US');

    const retry = screen.getByRole('button', { name: 'Retry' });
    expect(retry).toBeDisabled();
    fireEvent.click(retry);
    expect(mutateMock).not.toHaveBeenCalled();

    request.isValidating = false;
    view.rerender(<ChatConversationIndex />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(mutateMock).toHaveBeenCalledTimes(1);

    request.data = { id: routeConversationId, name: 'Recovered session' } as TChatConversation;
    request.error = undefined;
    view.rerender(<ChatConversationIndex />);
    expect(screen.getByText('Recovered session')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps a failed manual retry actionable without leaking the rejected error', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    swrMock.mockReturnValue({
      data: undefined,
      error: new Error('temporary failure'),
      isValidating: false,
      mutate: mutateMock,
    });
    mutateMock.mockRejectedValueOnce(new Error('private provider detail'));
    await renderWithI18n(<ChatConversationIndex />, 'en-US');

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(mutateMock).toHaveBeenCalledTimes(1));
    expect(screen.getByRole('alert')).toHaveTextContent('Failed to load session');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    expect(screen.queryByText(/private provider/)).not.toBeInTheDocument();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it('preserves an owner route snapshot if a transient error has no SWR data', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    routeConversationId = 'frame-error-snapshot';
    rememberConversationRouteSnapshot('owner-test', {
      id: routeConversationId,
      name: 'Snapshot session',
    } as TChatConversation);
    swrMock.mockReturnValue({ data: undefined, error: new Error('temporary failure'), mutate: mutateMock });
    await renderWithI18n(<ChatConversationIndex />);

    expect(screen.getByText('Snapshot session')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByTestId('conversation-loading-surface')).not.toBeInTheDocument();
  });

  it('does not let a cached snapshot override authoritative not-found navigation', async () => {
    routeConversationId = 'frame-missing-snapshot';
    rememberConversationRouteSnapshot('owner-test', {
      id: routeConversationId,
      name: 'Deleted snapshot',
    } as TChatConversation);
    swrMock.mockReturnValue({ data: null, error: undefined, mutate: mutateMock });
    await renderWithI18n(<ChatConversationIndex />);

    await expectGuidReplacement();
    expect(screen.queryByText('Deleted snapshot')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('recovers through actual SWR and the shared detail reader after a controlled initial failure', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const actualSWR = await vi.importActual<typeof import('swr')>('swr');
    swrMock.mockImplementation(actualSWR.default);
    let resolveRetry!: (conversation: TChatConversation) => void;
    const retryResult = new Promise<TChatConversation>((resolve) => {
      resolveRetry = resolve;
    });
    conversationGetMock.mockRejectedValueOnce(new Error('controlled detail failure')).mockReturnValueOnce(retryResult);
    const provider = () => new Map();
    const Wrapper = ({ children }: { children: React.ReactNode }) => (
      <actualSWR.SWRConfig value={{ provider, shouldRetryOnError: false, dedupingInterval: 0 }}>
        {children}
      </actualSWR.SWRConfig>
    );
    await renderWithI18n(<ChatConversationIndex />, 'en-US', { wrapper: Wrapper });

    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load session');
    expect(conversationGetMock).toHaveBeenCalledExactlyOnceWith({ id: routeConversationId });
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(conversationGetMock).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();

    await act(async () => {
      resolveRetry({ id: routeConversationId, name: 'Authoritatively recovered session' } as TChatConversation);
    });
    expect(await screen.findByText('Authoritatively recovered session')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByTestId('conversation-loading-surface')).not.toBeInTheDocument();
    expect(conversationGetMock.mock.calls).toEqual([[{ id: routeConversationId }], [{ id: routeConversationId }]]);
    expect(navigateMock).not.toHaveBeenCalled();
    expect(closePreviewMock).not.toHaveBeenCalled();
  });

  it('keeps the route snapshot visible while SWR revalidates it', async () => {
    swrMock.mockReturnValue({
      data: { id: 'frame-prefetched', name: 'Prefetched session' },
      error: undefined,
      isLoading: true,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    expect(screen.getByText('Prefetched session')).toBeInTheDocument();
    expect(screen.queryByTestId('conversation-loading-surface')).not.toBeInTheDocument();
  });

  it('renders a normal conversation without unavailable-state chrome', async () => {
    swrMock.mockReturnValue({
      data: { id: 'frame-ready', name: 'Ready session' },
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    expect(screen.getByText('Ready session')).toBeInTheDocument();
    expect(screen.queryByTestId('conversation-load-error')).not.toBeInTheDocument();
  });

  it('scopes the conversation detail cache to the authenticated owner', async () => {
    routeConversationId = 'frame-owner-scoped';
    swrMock.mockReturnValue({
      data: { id: routeConversationId, name: 'Owner scoped session' },
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />);

    expect(swrMock).toHaveBeenCalledWith(
      'conversation/owner-test/frame-owner-scoped',
      expect.any(Function),
      expect.objectContaining({ revalidateOnMount: true })
    );
  });

  it('does not repeat the detail request after navigation just prefetched it', async () => {
    routeConversationId = 'frame-recent-detail';
    const conversation = { id: routeConversationId, name: 'Recently prefetched session' };
    rememberConversationRouteSnapshot('owner-test', conversation as TChatConversation, 'detail');
    swrMock.mockReturnValue({
      data: conversation,
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });

    await renderWithI18n(<ChatConversationIndex />);

    expect(swrMock).toHaveBeenCalledWith(
      'conversation/owner-test/frame-recent-detail',
      expect.any(Function),
      expect.objectContaining({ revalidateOnMount: false })
    );
  });

  it('does not revalidate an active X-Science detail for metadata updates', async () => {
    swrMock.mockReturnValue({
      data: {
        id: 'frame-running',
        name: 'Running chemistry task',
        extra: { backend: 'xscience' },
        runtime: { is_processing: true, has_task: true },
      },
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    routeConversationId = 'frame-running';
    await renderWithI18n(<ChatConversationIndex />);

    listChangedListener?.({ conversation_id: 'frame-running', action: 'updated' });

    expect(mutateMock).not.toHaveBeenCalled();
  });

  it('does not revalidate an active X-Science detail for creation notifications', async () => {
    swrMock.mockReturnValue({
      data: {
        id: 'frame-running',
        name: 'Running chemistry task',
        extra: { backend: 'xscience' },
        runtime: { is_processing: true, has_task: true },
      },
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    routeConversationId = 'frame-running';
    await renderWithI18n(<ChatConversationIndex />);

    listChangedListener?.({ conversation_id: 'frame-running', action: 'created' });

    expect(mutateMock).not.toHaveBeenCalled();
  });

  it('preserves an open preview across same-conversation remount work and closes only on a route-id change', async () => {
    swrMock.mockReturnValue({
      data: { id: 'frame-missing', name: 'Ready session' },
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    const view = await renderWithI18n(<ChatConversationIndex />);

    expect(closePreviewMock).not.toHaveBeenCalled();
    view.rerender(<ChatConversationIndex />);
    expect(closePreviewMock).not.toHaveBeenCalled();

    routeConversationId = 'frame-next';
    swrMock.mockReturnValue({
      data: { id: 'frame-next', name: 'Next session' },
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    view.rerender(<ChatConversationIndex />);
    expect(closePreviewMock).toHaveBeenCalledTimes(1);
  });

  it('redirects a missing conversation independently of locale', async () => {
    swrMock.mockReturnValue({
      data: null,
      error: undefined,
      isLoading: false,
      mutate: mutateMock,
    });
    await renderWithI18n(<ChatConversationIndex />, 'en-US');

    await expectGuidReplacement();
    expect(screen.queryByText('Session deleted')).not.toBeInTheDocument();
  });
});

async function expectGuidReplacement() {
  await waitFor(() =>
    expect(navigateMock).toHaveBeenCalledWith('/guid', {
      replace: true,
      state: { resetAssistant: true },
    })
  );
}
