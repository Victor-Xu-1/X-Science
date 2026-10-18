import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  isActiveConversationSkillTask,
  useConversationSkillTask,
} from '@/renderer/pages/conversation/platforms/acp/useConversationSkillTask';
import { decodeMessageStreamPayload } from '@/common/adapter/messageStreamProtocol';
import { transformMessage, type TMessage } from '@/common/chat/chatLib';
import { decodeConversationRuntimeSummary } from '@/common/adapter/conversationRuntimeProtocol';
import {
  clearConversationRuntimeViewState,
  getConversationRuntimeViewSnapshot,
  hydrateSucceededConversationRuntimeView,
  localSendFailed,
  localSendStarted,
} from '@/renderer/pages/conversation/runtime/conversationRuntimeViewStore';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
function options() {
  return {
    authority: {},
    ownerId: 'owner',
    mountedRef: { current: true },
    disabled: false,
    isProcessing: false,
    startTask: vi.fn<(id: string) => Promise<void>>(),
    refreshCatalogs: vi.fn().mockResolvedValue([]),
  };
}

describe('conversation Skill task admission', () => {
  it('releases only the matching opaque runtime reservation across ABA and account-reset boundaries', () => {
    const conversationId = 'skill-admission-ticket';
    const oldTicket = {};
    const currentTicket = {};
    clearConversationRuntimeViewState();
    localSendStarted(conversationId, oldTicket);
    localSendFailed(conversationId, 'old-failed', oldTicket);
    localSendStarted(conversationId, currentTicket);
    expect(localSendFailed(conversationId, 'late-old-failure', oldTicket)).toEqual([]);
    expect(getConversationRuntimeViewSnapshot(conversationId).localSubmitting).toBe(true);
    localSendFailed(conversationId, 'current-cancelled', currentTicket);
    expect(getConversationRuntimeViewSnapshot(conversationId).localSubmitting).toBe(false);
    clearConversationRuntimeViewState();
    localSendStarted(conversationId, {});
    expect(localSendFailed(conversationId, 'old-account-cancelled', currentTicket)).toEqual([]);
    expect(getConversationRuntimeViewSnapshot(conversationId).localSubmitting).toBe(true);
    // Existing callers that do not supply a ticket retain their prior semantics.
    localSendFailed(conversationId, 'ordinary-send-failed');
    expect(getConversationRuntimeViewSnapshot(conversationId).localSubmitting).toBe(false);
    clearConversationRuntimeViewState();
  });
  it('validates and projects the canonical active input without reinterpreting the frame id', () => {
    const raw = {
      state: 'running',
      can_send_message: false,
      has_task: true,
      task_status: 'running',
      is_processing: true,
      pending_confirmations: 0,
      turn_id: 'frame',
      active_input_message_id: 'control-input',
    };
    const runtime = decodeConversationRuntimeSummary(raw);
    expect(runtime.active_input_message_id).toBe('control-input');
    expect(hydrateSucceededConversationRuntimeView(undefined, 'frame', runtime).view).toMatchObject({
      activeTurnId: 'frame',
      activeInputMessageId: 'control-input',
    });
    expect(() => decodeConversationRuntimeSummary({ ...raw, active_input_message_id: ' invalid ' })).toThrow();
    const legacy = { ...raw, active_input_message_id: undefined };
    expect(
      hydrateSucceededConversationRuntimeView(undefined, 'frame', decodeConversationRuntimeSummary(legacy)).view
        .activeInputMessageId
    ).toBeNull();
  });
  it('binds recovered control presentation to the exact active input, not the reused frame or prior Save', () => {
    const messages: TMessage[] = [
      {
        id: 'saved-control',
        msg_id: 'saved-control',
        conversation_id: 'frame',
        type: 'text',
        position: 'right',
        content: { content: 'Save as skill' },
        hidden: true,
        turn_intent: 'save-as-skill',
      },
      {
        id: 'ordinary-user',
        conversation_id: 'frame',
        type: 'text',
        position: 'right',
        content: { content: 'Continue normal research' },
      },
    ];
    expect(isActiveConversationSkillTask(messages, 'saved-control')).toBe(true);
    expect(isActiveConversationSkillTask(messages, 'ordinary-user')).toBe(false);
    expect(isActiveConversationSkillTask(messages, 'frame')).toBe(false);
    expect(isActiveConversationSkillTask(messages, undefined)).toBe(false);
  });

  it('preserves typed hidden control metadata without hiding ordinary user or assistant messages', () => {
    const payload = {
      type: 'message.stream',
      stream_type: 'text',
      position: 'right',
      conversation_id: 'frame',
      msg_id: 'control',
      turn_id: 'frame',
      data: { content: 'Save as skill' },
      hidden: true,
      turn_intent: 'save-as-skill',
    };
    const decoded = decodeMessageStreamPayload(payload);
    expect(decoded).toMatchObject({ hidden: true, turn_intent: 'save-as-skill' });
    if (!decoded) throw new Error('Expected a valid control publication');
    expect(transformMessage(decoded)).toMatchObject({ msg_id: 'control', hidden: true, turn_intent: 'save-as-skill' });
    expect(decodeMessageStreamPayload({ ...payload, turn_intent: 'unknown-control' })).toBeUndefined();
    expect(
      transformMessage({ type: 'user_content', conversation_id: 'frame', msg_id: 'ordinary', data: 'Save as skill' })
    ).not.toHaveProperty('hidden');
    expect(
      transformMessage({ type: 'text', conversation_id: 'frame', msg_id: 'answer', data: 'Created the package.' })
    ).not.toHaveProperty('hidden');
  });
  it('starts one real conversation command without a dialog and synchronously rejects duplicate clicks', async () => {
    const props = options();
    const accepted = deferred<void>();
    props.startTask.mockReturnValue(accepted.promise);
    const view = renderHook((p) => useConversationSkillTask(p), { initialProps: props });
    let first!: Promise<void>;
    await act(async () => {
      first = view.result.current.save();
      expect(view.result.current.isBlocking()).toBe(true);
      void view.result.current.save();
    });
    expect(props.startTask).toHaveBeenCalledOnce();
    expect(view.result.current.state.status).toBe('starting');
    expect(view.result.current.saveDisabled).toBe(true);
    view.rerender({ ...props, disabled: true, isProcessing: true });
    await act(async () => {
      accepted.resolve();
      await first;
    });
    expect(view.result.current.state.status).toBe('running');
    expect(props.refreshCatalogs).not.toHaveBeenCalled();
    view.rerender({ ...props, disabled: false, isProcessing: false });
    await waitFor(() => expect(view.result.current.state).toEqual({ status: 'finished', refresh: 'ready' }));
    expect(props.refreshCatalogs).toHaveBeenCalledOnce();
    expect(props.startTask).toHaveBeenCalledOnce();
  });

  it('refreshes catalogs after a fast terminal without claiming that the task saved successfully', async () => {
    const props = options();
    props.startTask.mockResolvedValue(undefined);
    const view = renderHook(() => useConversationSkillTask(props));
    await act(async () => {
      await view.result.current.save();
    });
    await waitFor(() => expect(view.result.current.state).toEqual({ status: 'finished', refresh: 'ready' }));
    expect(view.result.current.state).not.toHaveProperty('name');
    expect(props.startTask).toHaveBeenCalledOnce();
  });

  it('keeps an uncertain dispatch retry on the same mutation identity', async () => {
    const props = options();
    props.startTask.mockRejectedValueOnce(new Error('response lost')).mockResolvedValueOnce(undefined);
    const view = renderHook(() => useConversationSkillTask(props));
    await act(async () => {
      await view.result.current.save();
    });
    expect(view.result.current.state.status).toBe('error');
    expect(view.result.current.isBlocking()).toBe(false);
    const id = props.startTask.mock.calls[0][0];
    expect(props.refreshCatalogs).not.toHaveBeenCalled();
    await act(async () => {
      await view.result.current.save();
    });
    expect(props.startTask.mock.calls[1][0]).toBe(id);
    await waitFor(() => expect(view.result.current.state.status).toBe('finished'));
    await act(async () => {
      await view.result.current.save();
    });
    expect(props.startTask.mock.calls[2][0]).not.toBe(id);
  });

  it('retries only catalog reads after refresh failure, without repeating distillation', async () => {
    const props = options();
    props.startTask.mockResolvedValue(undefined);
    props.refreshCatalogs.mockRejectedValueOnce(new Error('list unavailable')).mockResolvedValueOnce([]);
    const view = renderHook(() => useConversationSkillTask(props));
    await act(async () => {
      await view.result.current.save();
    });
    await waitFor(() => expect(view.result.current.state).toEqual({ status: 'finished', refresh: 'error' }));
    await act(async () => {
      await view.result.current.retryRefresh();
    });
    expect(view.result.current.state).toEqual({ status: 'finished', refresh: 'ready' });
    expect(props.startTask).toHaveBeenCalledOnce();
    expect(props.refreshCatalogs).toHaveBeenCalledTimes(2);
  });

  it.each(['owner', 'conversation', 'unmount'] as const)(
    'does not expose late completion or refresh another %s scope',
    async (change) => {
      const props = options();
      const accepted = deferred<void>();
      props.startTask.mockReturnValue(accepted.promise);
      const view = renderHook((p) => useConversationSkillTask(p), { initialProps: props });
      let first!: Promise<void>;
      await act(async () => {
        first = view.result.current.save();
      });
      if (change === 'unmount') {
        props.mountedRef.current = false;
        view.unmount();
      } else view.rerender({ ...props, ...(change === 'owner' ? { ownerId: 'other' } : { authority: {} }) });
      await act(async () => {
        accepted.resolve();
        await first;
      });
      expect(props.refreshCatalogs).not.toHaveBeenCalled();
      if (change !== 'unmount') expect(view.result.current.state.status).toBe('idle');
    }
  );

  it('does not dispatch during another active task', async () => {
    const props = { ...options(), disabled: true, isProcessing: true };
    const view = renderHook(() => useConversationSkillTask(props));
    await act(async () => {
      await view.result.current.save();
    });
    expect(props.startTask).not.toHaveBeenCalled();
  });

  it('restores only an explicitly identified active control and refreshes when it ends', async () => {
    const props = { ...options(), activeControlTask: true, disabled: true, isProcessing: true };
    const view = renderHook((p) => useConversationSkillTask(p), { initialProps: props });
    expect(view.result.current.inFlight).toBe(true);
    expect(view.result.current.isBlocking()).toBe(true);
    await act(async () => {
      await view.result.current.save();
    });
    expect(props.startTask).not.toHaveBeenCalled();
    view.rerender({ ...props, isProcessing: false });
    expect(props.refreshCatalogs).not.toHaveBeenCalled();
    expect(view.result.current.inFlight).toBe(true);
    view.rerender({ ...props, activeControlTask: false, disabled: false, isProcessing: false });
    await waitFor(() => expect(view.result.current.state).toEqual({ status: 'finished', refresh: 'ready' }));
    expect(props.refreshCatalogs).toHaveBeenCalledOnce();
    expect(view.result.current.isBlocking()).toBe(false);
  });
});
