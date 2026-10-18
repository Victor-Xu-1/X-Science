import { act, renderHook } from '@testing-library/react';
import type { TChatConversation } from '@/common/config/storage';
import { beforeEach, describe, expect, it, vi } from 'vitest';
const mock = vi.hoisted(() => ({ setPin: vi.fn(), scope: 'a', error: vi.fn(), emit: vi.fn() }));
vi.mock('@/renderer/services/runtime/conversationPresentation', () => ({ setConversationPinned: mock.setPin }));
vi.mock('@/renderer/services/rendererAccountScope', () => ({ getRendererAccountScopeToken: () => mock.scope }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { emit: mock.emit } }));
vi.mock('@arco-design/web-react', () => ({ Message: { error: mock.error } }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import { useConversationPin } from '@/renderer/pages/conversation/GroupedHistory/hooks/useConversationPin';
const row: TChatConversation = {
  id: 'frame',
  name: 'Frame',
  type: 'acp',
  created_at: 1,
  modified_at: 1,
  extra: { backend: 'xscience', pinned: false },
};

describe('pin action ownership', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mock.setPin.mockReset();
    mock.scope = 'a';
  });
  it('locks repeat clicks and refreshes only after the accepted request', async () => {
    let resolve!: () => void;
    mock.setPin.mockImplementation(
      () =>
        new Promise<void>((done) => {
          resolve = done;
        })
    );
    const { result } = renderHook(useConversationPin);
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.togglePin(row);
      void result.current.togglePin(row);
    });
    expect(mock.setPin).toHaveBeenCalledTimes(1);
    expect(result.current.isPinPending(row.id)).toBe(true);
    expect(mock.emit).not.toHaveBeenCalled();
    await act(async () => {
      resolve();
      await pending;
    });
    expect(result.current.isPinPending(row.id)).toBe(false);
    expect(mock.emit).toHaveBeenCalledWith('chat.history.refresh');
  });
  it('reports failure without optimistic pin changes and permits a new explicit action', async () => {
    mock.setPin.mockRejectedValueOnce(new Error('stale')).mockResolvedValueOnce(undefined);
    const { result } = renderHook(useConversationPin);
    await act(async () => {
      await result.current.togglePin(row);
    });
    expect(mock.error).toHaveBeenCalledWith('conversation.history.pinFailed');
    expect(row.extra.pinned).toBe(false);
    await act(async () => {
      await result.current.togglePin(row);
    });
    expect(mock.setPin).toHaveBeenCalledTimes(2);
  });
  it.each(['account', 'unmount'])('ignores a late %s result', async (kind) => {
    let reject!: (error: Error) => void;
    mock.setPin.mockImplementation(
      () =>
        new Promise<void>((_, fail) => {
          reject = fail;
        })
    );
    const { result, unmount } = renderHook(useConversationPin);
    let pending!: Promise<void>;
    act(() => {
      pending = result.current.togglePin(row);
    });
    if (kind === 'account') mock.scope = 'b';
    else unmount();
    await act(async () => {
      reject(new Error('late'));
      await pending;
    });
    expect(mock.error).not.toHaveBeenCalled();
    expect(mock.emit).not.toHaveBeenCalled();
  });
});
