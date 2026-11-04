import { act, waitFor } from '@testing-library/react';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthProvider } from '@/renderer/hooks/context/AuthContext';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';

const state = vi.hoisted(() => ({
  prefetchConversationRoute: vi.fn(),
  bindRealtimeRuntime: vi.fn(),
  createRendererRealtimeRuntime: vi.fn(),
  readNotificationPreference: vi.fn(),
  writeNotificationPreference: vi.fn(),
  owner: 'workspace-owner',
}));

vi.mock('@/renderer/hooks/context/AuthContext', async () => {
  const { createContext, useContext } = await import('react');
  const authenticated = { status: 'authenticated' as const, user: { id: state.owner } };
  const FixtureAuthContext = createContext<typeof authenticated | null>(null);
  return {
    AuthProvider: ({ children }: React.PropsWithChildren) => (
      <FixtureAuthContext.Provider value={authenticated}>{children}</FixtureAuthContext.Provider>
    ),
    useAuth: () => {
      const auth = useContext(FixtureAuthContext);
      if (!auth) throw new Error('Workspace fixture must mount inside its AuthProvider');
      return auth;
    },
  };
});
vi.mock('@/renderer/services/clientBusinessSettings', () => ({
  getClientBusinessSetting: state.readNotificationPreference,
  setClientBusinessSetting: state.writeNotificationPreference,
}));

vi.mock('@/renderer/pages/conversation/conversationRoute', () => ({
  prefetchConversationRoute: state.prefetchConversationRoute,
}));
vi.mock('@/common/adapter/httpBridge', () => ({ bindRealtimeRuntime: state.bindRealtimeRuntime }));
vi.mock('@/common/adapter/realtimeRenderer', () => ({
  createRendererRealtimeRuntime: state.createRendererRealtimeRuntime,
}));
vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  RealtimeProvider: ({
    children,
    runtime,
  }: React.PropsWithChildren<{
    runtime: {
      getSnapshot: () => unknown;
      subscribeStatus: (listener: () => void) => () => void;
    };
  }>) => {
    React.useSyncExternalStore(runtime.subscribeStatus, runtime.getSnapshot, runtime.getSnapshot);
    return <>{children}</>;
  },
}));
vi.mock('@/renderer/hooks/context/ConversationHistoryContext', () => ({
  ConversationHistoryProvider: ({ children }: React.PropsWithChildren) => <>{children}</>,
}));
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  PreviewProvider: ({ children }: React.PropsWithChildren) => <>{children}</>,
}));
vi.mock('@/renderer/components/layout/Layout', () => ({ default: () => null }));
vi.mock('@/renderer/components/layout/Sider', () => ({ default: () => null }));

import AuthenticatedWorkspace, {
  InitialConversationPrefetchAuthority,
} from '@/renderer/components/layout/AuthenticatedWorkspace';

describe('InitialConversationPrefetchAuthority', () => {
  beforeEach(() => {
    state.prefetchConversationRoute.mockReset().mockResolvedValue(undefined);
    state.bindRealtimeRuntime.mockReset();
    state.createRendererRealtimeRuntime.mockReset();
    state.readNotificationPreference.mockReset();
    state.writeNotificationPreference.mockReset();
  });

  afterEach(resetRendererAccountScopeForTest);

  it('prefetches the conversation module once across route rerenders', async () => {
    const view = await renderWithI18n(
      <MemoryRouter initialEntries={['/conversation/conversation-a']}>
        <InitialConversationPrefetchAuthority />
      </MemoryRouter>,
      'en-US'
    );

    await waitFor(() => expect(state.prefetchConversationRoute).toHaveBeenCalledTimes(1));
    await act(async () => {
      view.rerender(
        <MemoryRouter initialEntries={['/conversation/conversation-a']}>
          <InitialConversationPrefetchAuthority />
        </MemoryRouter>
      );
    });
    expect(state.prefetchConversationRoute).toHaveBeenCalledTimes(1);

    await act(async () => {
      view.rerender(
        <MemoryRouter initialEntries={['/conversation/conversation-a']}>
          <InitialConversationPrefetchAuthority />
        </MemoryRouter>
      );
    });
    expect(state.prefetchConversationRoute).toHaveBeenCalledTimes(1);
  });

  it('keeps realtime binding out of render and releases the committed binding exactly once', async () => {
    const runtimes: Array<{ dispose: ReturnType<typeof vi.fn> }> = [];
    const releases: Array<ReturnType<typeof vi.fn>> = [];
    state.createRendererRealtimeRuntime.mockImplementation(() => {
      const snapshot = Object.freeze({ identity: 'checking' as const, status: 'idle' as const });
      const runtime = {
        getSnapshot: () => snapshot,
        subscribeStatus: () => () => {},
        dispose: vi.fn(),
      };
      runtimes.push(runtime);
      return runtime;
    });
    state.bindRealtimeRuntime.mockImplementation(() => {
      const release = vi.fn();
      releases.push(release);
      return release;
    });

    const abortPreferenceRead = vi.fn();
    state.readNotificationPreference.mockImplementation((_key: string, { signal }: { signal: AbortSignal }) => {
      signal.addEventListener('abort', abortPreferenceRead, { once: true });
      return new Promise<boolean>(() => {});
    });
    setRendererAccountOwner(state.owner);
    const workspace = (
      <AuthProvider>
        <MemoryRouter initialEntries={['/guid']}>
          <AuthenticatedWorkspace />
        </MemoryRouter>
      </AuthProvider>
    );

    renderToString(workspace);
    expect(state.createRendererRealtimeRuntime).not.toHaveBeenCalled();
    expect(state.bindRealtimeRuntime).not.toHaveBeenCalled();
    expect(state.readNotificationPreference).not.toHaveBeenCalled();

    const view = await renderWithI18n(workspace, 'en-US');

    await waitFor(() => expect(state.createRendererRealtimeRuntime).toHaveBeenCalledTimes(1));
    expect(state.bindRealtimeRuntime).toHaveBeenCalledTimes(1);
    expect(runtimes).toHaveLength(1);
    expect(releases).toHaveLength(1);
    expect(releases[0]).not.toHaveBeenCalled();
    expect(runtimes[0].dispose).not.toHaveBeenCalled();
    expect(state.readNotificationPreference).toHaveBeenCalledTimes(1);
    expect(state.readNotificationPreference).toHaveBeenCalledWith('system.notificationEnabled', {
      signal: expect.any(AbortSignal),
    });
    expect(abortPreferenceRead).not.toHaveBeenCalled();

    view.unmount();
    expect(abortPreferenceRead).toHaveBeenCalledTimes(1);
    expect(state.writeNotificationPreference).not.toHaveBeenCalled();
    expect(releases[0]).toHaveBeenCalledTimes(1);
    expect(runtimes[0].dispose).toHaveBeenCalledTimes(1);
  });
});
