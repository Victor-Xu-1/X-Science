import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { TChatConversation } from '@/common/config/storage';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { renderWithI18n } from '../i18nTestUtils';
import {
  beginConversationDetailHandoff,
  clearConversationReadCaches,
  getConversationOrNullShared,
  readConversationRouteSnapshot,
  rememberConversationRouteSnapshot,
} from '@/renderer/pages/conversation/utils/conversationCache';
import { getRendererAccountScopeToken, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { emitter } from '@/renderer/utils/emitter';
import { useXScienceConversationSync } from '@/renderer/pages/conversation/platforms/acp/useXScienceConversationSync';
import ChatConversationIndex from '@/renderer/pages/conversation';

type ListChange = { conversation_id: string; action: 'created' | 'updated' | 'deleted' };
const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  closePreview: vi.fn(),
  syncIssue: vi.fn(),
  listeners: new Set<(event: ListChange) => void>(),
  reconnect: undefined as (() => void) | undefined,
  wake: undefined as ((signal: { kind: 'reconcile' }) => void) | undefined,
  nestedSync: false,
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      get: { invoke: mocks.get },
      listChanged: {
        on: (listener: (event: ListChange) => void) => {
          mocks.listeners.add(listener);
          return () => mocks.listeners.delete(listener);
        },
      },
    },
  },
}));
vi.mock('@/renderer/hooks/context/AuthContext', () => ({ useAuth: () => ({ user: { id: 'owner-test' } }) }));
vi.mock('@/renderer/hooks/context/RealtimeContext', () => ({
  useRealtime: () => ({
    runtime: {
      subscribeReconnected: (listener: () => void) => {
        mocks.reconnect = listener;
        return () => {
          mocks.reconnect = undefined;
        };
      },
    },
  }),
}));
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => ({ closePreview: mocks.closePreview }),
}));
vi.mock('@/renderer/hooks/chat/useAutoTitle', () => ({ useAutoTitle: () => ({ syncTitleFromHistory: vi.fn() }) }));
vi.mock('@/renderer/pages/conversation/components/ChatConversation', () => ({
  default: ({ conversation }: { conversation: TChatConversation }) => (
    <div>
      <span>{conversation.name}</span>
      <button>Composer fixture</button>
      {mocks.nestedSync && <NestedSync conversation={conversation} />}
    </div>
  ),
}));

function NestedSync({ conversation }: { conversation: TChatConversation }) {
  useXScienceConversationSync({
    conversationId: conversation.id,
    initialConversation: conversation,
    initialRuntime: conversation.runtime,
    backend: 'xscience',
    enabled: true,
    refreshMessages: async () => [],
    onSettled: () => undefined,
    onIssue: mocks.syncIssue,
    subscribeWake: subscribeNestedWake,
  });
  return null;
}

const subscribeNestedWake: NonNullable<Parameters<typeof useXScienceConversationSync>[0]['subscribeWake']> = (
  _id,
  wake
) => {
  mocks.wake = wake;
  return () => {
    mocks.wake = undefined;
  };
};

const conversation = (id = 'frame-current') =>
  ({
    id,
    name: `Loaded ${id}`,
    type: 'acp',
    status: 'running',
    extra: { backend: 'xscience' },
    runtime: {
      state: 'running',
      can_send_message: false,
      has_task: true,
      task_status: 'running',
      is_processing: true,
      pending_confirmations: 0,
      turn_id: id,
    },
  }) as TChatConversation;

const failure = (status: number) =>
  new BackendHttpError({
    method: 'GET',
    path: '/api/conversations/frame-current',
    status,
    body: { message: 'unavailable' },
  });
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
const emitDeleted = async (id = 'frame-current') =>
  act(async () => {
    for (const listener of mocks.listeners) listener({ conversation_id: id, action: 'deleted' });
  });

function Navigation() {
  const navigate = useNavigate();
  return (
    <button
      onClick={() => {
        void navigate('/conversation/frame-next');
      }}
    >
      Next conversation
    </button>
  );
}
async function mount() {
  return renderWithI18n(
    <SWRConfig value={{ provider: () => new Map(), errorRetryCount: 0, revalidateOnFocus: false }}>
      <MemoryRouter initialEntries={['/conversation/frame-current']}>
        <Navigation />
        <Routes>
          <Route path='/conversation/:id' element={<ChatConversationIndex />} />
          <Route path='/guid' element={<div>New task fixture</div>} />
        </Routes>
      </MemoryRouter>
    </SWRConfig>,
    'en-US'
  );
}

describe('authoritative deletion of an already-loaded route', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listeners.clear();
    mocks.nestedSync = false;
    mocks.reconnect = undefined;
    mocks.wake = undefined;
    clearConversationReadCaches();
    setRendererAccountOwner('owner-test');
    mocks.get.mockReset().mockResolvedValue(conversation());
  });
  afterEach(() => vi.restoreAllMocks());

  it('reconciles a cross-tab deleted event through fresh authority despite active runtime and a recent handoff', async () => {
    beginConversationDetailHandoff('frame-current');
    const detail = await getConversationOrNullShared('frame-current');
    rememberConversationRouteSnapshot('owner-test', detail!, 'detail');
    await mount();
    expect(screen.getByRole('button', { name: 'Composer fixture' })).toBeInTheDocument();
    mocks.get.mockRejectedValueOnce(failure(404));

    await emitDeleted();

    await waitFor(() => expect(screen.getByText('New task fixture')).toBeInTheDocument());
    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(mocks.closePreview).toHaveBeenCalledTimes(1);
    expect(readConversationRouteSnapshot('owner-test', 'frame-current')).toBeUndefined();
  });

  it('joins a nested sync authoritative404 to the same route instead of stranding its composer', async () => {
    mocks.nestedSync = true;
    await mount();
    await screen.findByRole('button', { name: 'Composer fixture' });
    await waitFor(() => expect(mocks.wake).toBeTypeOf('function'));
    mocks.get.mockRejectedValueOnce(failure(404));

    await act(async () => {
      mocks.wake?.({ kind: 'reconcile' });
    });

    await screen.findByText('New task fixture');
    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(mocks.closePreview).toHaveBeenCalledTimes(1);
  });

  it('keeps loaded content on temporary errors and later reconciles canonical absence on reconnect', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    await mount();
    await screen.findByRole('button', { name: 'Composer fixture' });
    mocks.get.mockRejectedValueOnce(failure(500));
    await emitDeleted();
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
    expect(screen.getByText('Loaded frame-current')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Composer fixture' })).toBeInTheDocument();
    expect(screen.queryByText('New task fixture')).not.toBeInTheDocument();
    expect(mocks.closePreview).not.toHaveBeenCalled();

    mocks.get.mockRejectedValueOnce(failure(404));
    await act(async () => {
      mocks.reconnect?.();
    });
    await screen.findByText('New task fixture');
  });

  it('ignores another conversation deletion and a late missing result after route change', async () => {
    await mount();
    await screen.findByText('Loaded frame-current');
    await emitDeleted('frame-other');
    expect(mocks.get).toHaveBeenCalledTimes(1);
    const oldRead = deferred<TChatConversation>();
    mocks.get.mockReturnValueOnce(oldRead.promise).mockResolvedValueOnce(conversation('frame-next'));
    await emitDeleted();
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole('button', { name: 'Next conversation' }));
    await screen.findByText('Loaded frame-next');
    await act(async () => {
      oldRead.reject(failure(404));
    });
    expect(screen.getByText('Loaded frame-next')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Composer fixture' })).toBeInTheDocument();
    expect(screen.queryByText('New task fixture')).not.toBeInTheDocument();
  });

  it('rejects a previous account scope missing notification without emitting a queue cleanup', async () => {
    const previousScope = getRendererAccountScopeToken();
    setRendererAccountOwner('owner-intermediate');
    setRendererAccountOwner('owner-test');
    const queueCleanup = vi.fn();
    emitter.on('conversation.deleted', queueCleanup);
    try {
      await mount();
      await screen.findByRole('button', { name: 'Composer fixture' });

      await act(async () => {
        emitter.emit('conversation.detail.missing', previousScope, 'frame-current');
      });

      expect(screen.getByText('Loaded frame-current')).toBeInTheDocument();
      expect(screen.queryByText('New task fixture')).not.toBeInTheDocument();
      expect(queueCleanup).not.toHaveBeenCalled();
      expect(mocks.get).toHaveBeenCalledTimes(1);

      // The old route binding must not consume the next owner's same-ID result
      // during the interval before its React subtree is replaced.
      setRendererAccountOwner('owner-replaced');
      await act(async () => {
        emitter.emit('conversation.detail.missing', getRendererAccountScopeToken(), 'frame-current');
      });
      expect(screen.getByText('Loaded frame-current')).toBeInTheDocument();
      expect(screen.queryByText('New task fixture')).not.toBeInTheDocument();
      expect(queueCleanup).not.toHaveBeenCalled();
    } finally {
      emitter.off('conversation.deleted', queueCleanup);
    }
  });

  it('does not let a superseded nested404 hide the refreshed same-route composer', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    mocks.nestedSync = true;
    await mount();
    await screen.findByRole('button', { name: 'Composer fixture' });
    const oldRead = deferred<TChatConversation>();
    mocks.get.mockReturnValueOnce(oldRead.promise);
    await act(async () => {
      mocks.wake?.({ kind: 'reconcile' });
    });
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(2));
    mocks.get.mockResolvedValueOnce({ ...conversation(), name: 'Refreshed same route' });
    await emitDeleted();
    await screen.findByText('Refreshed same route');

    await act(async () => {
      oldRead.reject(failure(404));
    });

    expect(mocks.syncIssue).not.toHaveBeenCalledWith({ kind: 'missing', retryDelayMs: null });
    expect(screen.getByText('Refreshed same route')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Composer fixture' })).toBeInTheDocument();
    expect(screen.queryByText('New task fixture')).not.toBeInTheDocument();
  });
});
