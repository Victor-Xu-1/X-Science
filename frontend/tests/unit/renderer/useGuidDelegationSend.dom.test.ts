import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { useGuidSend, type GuidSendDeps } from '@/renderer/pages/guid/hooks/useGuidSend';
import { useGuidSessionOptions } from '@/renderer/pages/guid/hooks/useGuidSessionOptions';

const { create, request, navigate, messageError } = vi.hoisted(() => ({
  create: vi.fn(),
  request: vi.fn(),
  navigate: vi.fn(),
  messageError: vi.fn(),
}));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: request,
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: { create: { invoke: create }, listChanged: { on: () => () => {} } },
    realtime: { reconnected: { on: () => () => {} } },
  },
}));
vi.mock('swr', () => ({ useSWRConfig: () => ({ mutate: vi.fn().mockResolvedValue(undefined) }) }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { emit: vi.fn() } }));
vi.mock('@/renderer/pages/conversation/conversationRoute', () => ({
  prefetchConversationRoute: () => Promise.resolve(),
}));
vi.mock('@/renderer/utils/workspace/workspaceHistory', () => ({ updateWorkspaceTime: vi.fn() }));
vi.mock('@arco-design/web-react', () => ({ Message: { warning: vi.fn(), error: messageError } }));
const frame = (id = 'draft-frame') => ({
  id,
  type: 'acp',
  extra: { frame_incarnation_id: `${id}-inc`, root_frame_id: id },
});
const snapshot = (revision = 0, policy = 'allow', id = 'draft-frame') => ({
  schema: 'x-science.session-delegation-policy.v1',
  scope: { root_frame_id: id, root_incarnation_id: `${id}-inc` },
  revision,
  policy,
  has_live_delegated_attempts: false,
});
function mutation(command: Record<string, unknown>) {
  return {
    schema: 'x-science.session-delegation-mutation.v1',
    receipt: {
      schema: 'x-science.session-delegation-receipt.v1',
      command,
      applied_revision: Number(command.expected_revision) + 1,
      applied_policy: command.desired_policy,
      applied_at: '2026-10-19T00:00:00Z',
    },
    snapshot: {
      ...snapshot(Number(command.expected_revision) + 1, String(command.desired_policy)),
      scope: command.scope,
    },
    replayed: true,
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { resolve, promise };
}
const posts = () => request.mock.calls.filter(([method]) => method === 'POST');
const deps = (): GuidSendDeps => ({
  input: 'inspect a synthetic dataset',
  setInput: vi.fn(),
  files: [],
  setFiles: vi.fn(),
  localFiles: [],
  setLocalFiles: vi.fn(),
  contextItems: [],
  setContextItems: vi.fn(),
  dir: '',
  setDir: vi.fn(),
  loading: false,
  setLoading: vi.fn(),
  selectedAssistantId: 'assistant-one',
  selectedAssistantBackend: 'xscience',
  selectedMode: 'bypassPermissions',
  selectedAcpModel: null,
  currentAcpCachedModelInfo: null,
  guidDisabledBuiltinSkills: undefined,
  guidEnabledSkills: undefined,
  availableMcpServers: [],
  selectedMcpServerIds: undefined,
  sessionOptions: { delegation: false, memory: true, autoReview: false, targetAgent: 'OPERON' },
  sessionDefaults: {},
  sessionComputeProviders: [],
  setMentionOpen: vi.fn(),
  setMentionQuery: vi.fn(),
  setMentionSelectorOpen: vi.fn(),
  setMentionActiveIndex: vi.fn(),
  ownerId: 'owner',
  navigate: navigate as never,
  t: ((key: string) => key) as never,
  localeKey: 'en-US',
  delegationPolicyOverride: 'deny',
  draftGeneration: 0,
});
describe('Guid durable delegation handoff', () => {
  beforeEach(() => {
    sessionStorage.clear();
    create.mockReset();
    request.mockReset();
    navigate.mockReset();
    messageError.mockReset();
    setRendererAccountOwner(null);
    setRendererAccountOwner('owner');
    create.mockResolvedValue(frame());
    navigate.mockResolvedValue(undefined);
    request.mockImplementation((method: string, _path: string, command: Record<string, unknown>) =>
      Promise.resolve(method === 'GET' ? snapshot() : mutation(command))
    );
  });
  afterEach(() => {
    setRendererAccountOwner(null);
  });
  it('confirms explicit deny before initial-message handoff and never sends the scientific task itself', async () => {
    const pending = deferred<unknown>();
    request.mockImplementation((method: string) => (method === 'GET' ? Promise.resolve(snapshot()) : pending.promise));
    const { result } = renderHook(() => useGuidSend(deps()));
    let completion!: Promise<boolean>;
    act(() => {
      completion = result.current.sendMessageHandler();
    });
    await vi.waitFor(() => expect(posts()).toHaveLength(1));
    expect(navigate).not.toHaveBeenCalled();
    expect(sessionStorage.getItem('acp_initial_message_draft-frame')).toBeNull();
    await act(async () => {
      pending.resolve(mutation(posts()[0][2]));
      await expect(completion).resolves.toBe(true);
    });
    expect(create).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith('/conversation/draft-frame');
    expect(JSON.parse(sessionStorage.getItem('acp_initial_message_draft-frame')!)).toMatchObject({
      input: 'inspect a synthetic dataset',
      session_options: { ultra_mode: false },
    });
  });
  it('retains a materialized conversation and the identical command after a lost policy acknowledgement', async () => {
    let lost = true;
    request.mockImplementation((method: string, _path: string, command: Record<string, unknown>) => {
      if (method === 'GET') return Promise.resolve(snapshot());
      if (lost) {
        lost = false;
        return Promise.reject(new TypeError('acknowledgement lost'));
      }
      return Promise.resolve(mutation(command));
    });
    const { result } = renderHook(() => useGuidSend(deps()));
    await act(async () => {
      await expect(result.current.sendMessageHandler()).resolves.toBe(false);
    });
    const command = posts()[0][2];
    expect(navigate).not.toHaveBeenCalled();
    expect(sessionStorage.length).toBe(0);
    await act(async () => {
      await expect(result.current.sendMessageHandler()).resolves.toBe(true);
    });
    expect(create).toHaveBeenCalledTimes(1);
    expect(posts()).toHaveLength(2);
    expect(posts()[1][2]).toBe(command);
    expect(navigate).toHaveBeenCalledTimes(1);
  });
  it('does not automatically rebase a rejected revision into another policy command', async () => {
    request.mockImplementation((method: string) =>
      method === 'GET'
        ? Promise.resolve(snapshot())
        : Promise.reject(
            new BackendHttpError({
              method: 'POST',
              path: '/',
              status: 409,
              body: { code: 'SESSION_DELEGATION_REVISION_CONFLICT', snapshot: snapshot(2) },
            })
          )
    );
    const { result } = renderHook(() => useGuidSend(deps()));
    await act(async () => {
      await expect(result.current.sendMessageHandler()).resolves.toBe(false);
    });
    expect(posts()).toHaveLength(1);
    expect(navigate).not.toHaveBeenCalled();
    expect(sessionStorage.length).toBe(0);
  });
  it('reconciles an old unknown deny then applies a newly explicit allow without requiring another click', async () => {
    let lost = true;
    request.mockImplementation((method: string, _path: string, command: Record<string, unknown>) => {
      if (method === 'GET') return Promise.resolve(snapshot());
      if (lost) {
        lost = false;
        return Promise.reject(new TypeError('acknowledgement lost'));
      }
      return Promise.resolve(mutation(command));
    });
    const initial = deps();
    const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
    await act(async () => {
      await expect(hook.result.current.sendMessageHandler()).resolves.toBe(false);
    });
    const original = posts()[0][2];
    hook.rerender({ ...initial, delegationPolicyOverride: 'allow' });
    await act(async () => {
      await expect(hook.result.current.sendMessageHandler()).resolves.toBe(true);
    });
    expect(create).toHaveBeenCalledTimes(1);
    expect(posts()).toHaveLength(3);
    expect(posts()[1][2]).toBe(original);
    expect(posts()[2][2]).toMatchObject({ expected_revision: 1, desired_policy: 'allow' });
    expect(posts()[2][2].command_id).not.toBe(original.command_id);
  });
  it('uses the backend default allow without an unnecessary GET or POST', async () => {
    const { result } = renderHook(() => useGuidSend({ ...deps(), delegationPolicyOverride: undefined }));
    await act(async () => {
      await expect(result.current.sendMessageHandler()).resolves.toBe(true);
    });
    expect(request).not.toHaveBeenCalled();
    expect(create).toHaveBeenCalledTimes(1);
    expect(JSON.parse(sessionStorage.getItem('acp_initial_message_draft-frame')!)).toMatchObject({
      session_options: { ultra_mode: true },
    });
  });
  it('preserves the same initial message identity when navigation fails and the user retries', async () => {
    navigate.mockRejectedValueOnce(new Error('route unavailable'));
    const { result } = renderHook(() => useGuidSend({ ...deps(), delegationPolicyOverride: undefined }));
    await act(async () => {
      await expect(result.current.sendMessageHandler()).resolves.toBe(false);
    });
    const original = sessionStorage.getItem('acp_initial_message_draft-frame');
    await act(async () => {
      await expect(result.current.sendMessageHandler()).resolves.toBe(true);
    });
    expect(create).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem('acp_initial_message_draft-frame')).toBe(original);
  });
  it('allows an edited draft after sessionStorage proves that no initial-message handoff occurred', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    setItem.mockImplementationOnce(() => {
      throw new Error('storage quota');
    });
    const initial = { ...deps(), delegationPolicyOverride: undefined };
    const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
    await act(async () => {
      await expect(hook.result.current.sendMessageHandler()).resolves.toBe(false);
    });
    expect(sessionStorage.length).toBe(0);
    expect(navigate).not.toHaveBeenCalled();
    hook.rerender({ ...initial, input: 'a corrected unsent question' });
    await act(async () => {
      await expect(hook.result.current.sendMessageHandler()).resolves.toBe(true);
    });
    expect(create).toHaveBeenCalledTimes(1);
    expect(JSON.parse(sessionStorage.getItem('acp_initial_message_draft-frame')!).input).toBe(
      'a corrected unsent question'
    );
    setItem.mockRestore();
  });
  it('does not cancel a current send for an unrelated unselected MCP catalog refresh', async () => {
    const pending = deferred<unknown>();
    create.mockReturnValueOnce(pending.promise);
    const initial = { ...deps(), delegationPolicyOverride: undefined };
    const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
    let completion!: Promise<boolean>;
    act(() => {
      completion = hook.result.current.sendMessageHandler();
    });
    await vi.waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    hook.rerender({
      ...initial,
      availableMcpServers: [
        { id: 'unselected-server', name: 'New catalog entry', enabled: true },
      ] as GuidSendDeps['availableMcpServers'],
    });
    await act(async () => {
      pending.resolve(frame());
      await expect(completion).resolves.toBe(true);
    });
    expect(navigate).toHaveBeenCalledTimes(1);
  });
  it.each(['account', 'draft', 'assistant', 'workspace', 'input'] as const)(
    'fences a late create after %s changes without clearing the current input',
    async (change) => {
      const pending = deferred<unknown>();
      create.mockReturnValueOnce(pending.promise);
      const initial = deps();
      const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
      let completion!: Promise<boolean>;
      act(() => {
        completion = hook.result.current.sendMessageHandler();
      });
      await vi.waitFor(() => expect(create).toHaveBeenCalledTimes(1));
      const next = { ...initial };
      if (change === 'account') {
        setRendererAccountOwner('other');
        next.ownerId = 'other';
      }
      if (change === 'draft') next.draftGeneration = 1;
      if (change === 'assistant') next.selectedAssistantId = 'assistant-two';
      if (change === 'workspace') next.dir = 'x-science://project/another';
      if (change === 'input') next.input = 'a new unsent question';
      hook.rerender(next);
      await act(async () => {
        pending.resolve(frame());
        await expect(completion).resolves.toBe(false);
      });
      expect(request).not.toHaveBeenCalled();
      expect(navigate).not.toHaveBeenCalled();
      expect(initial.setInput).not.toHaveBeenCalled();
      expect(sessionStorage.length).toBe(0);
    }
  );
  it('does not reuse an empty conversation created with a different assistant or project', async () => {
    request.mockRejectedValueOnce(new TypeError('read unavailable'));
    const initial = deps();
    const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
    await act(async () => {
      await expect(hook.result.current.sendMessageHandler()).resolves.toBe(false);
    });
    create.mockResolvedValueOnce(frame('new-frame'));
    request.mockImplementation((method: string, _path: string, command: Record<string, unknown>) =>
      Promise.resolve(method === 'GET' ? snapshot(0, 'allow', 'new-frame') : mutation(command))
    );
    hook.rerender({ ...initial, selectedAssistantId: 'assistant-two', dir: 'x-science://project/another' });
    await act(async () => {
      await expect(hook.result.current.sendMessageHandler()).resolves.toBe(true);
    });
    expect(create).toHaveBeenCalledTimes(2);
    expect(navigate).toHaveBeenCalledWith('/conversation/new-frame');
    expect(create.mock.calls[1][0]).toMatchObject({
      assistant: { id: 'assistant-two' },
      extra: { project_id: 'another' },
    });
  });
  it('releases same-draft loading after an input fence without clearing the edited input', async () => {
    const pending = deferred<unknown>();
    create.mockReturnValueOnce(pending.promise);
    const initial = { ...deps(), delegationPolicyOverride: undefined };
    const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
    let completion!: Promise<boolean>;
    act(() => {
      completion = hook.result.current.sendMessageHandler();
    });
    await vi.waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    hook.rerender({ ...initial, input: 'new unsent input' });
    await act(async () => {
      pending.resolve(frame());
      await completion;
    });
    expect(initial.setInput).not.toHaveBeenCalled();
    expect(initial.setLoading).toHaveBeenLastCalledWith(false);
    expect(navigate).not.toHaveBeenCalled();
  });
  it('starts a new draft independently and never lets the old request release its new in-flight token', async () => {
    const oldCreate = deferred<unknown>(),
      newCreate = deferred<unknown>();
    create.mockReturnValueOnce(oldCreate.promise).mockReturnValueOnce(newCreate.promise);
    const initial = { ...deps(), delegationPolicyOverride: undefined };
    const hook = renderHook((value: GuidSendDeps) => useGuidSend(value), { initialProps: initial });
    let oldCompletion!: Promise<boolean>, newCompletion!: Promise<boolean>;
    act(() => {
      oldCompletion = hook.result.current.sendMessageHandler();
    });
    await vi.waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    hook.rerender({ ...initial, draftGeneration: 1, input: 'new draft task' });
    act(() => {
      newCompletion = hook.result.current.sendMessageHandler();
    });
    const callsBeforeOldEnd = create.mock.calls.length;
    await act(async () => {
      oldCreate.resolve(frame('old-frame'));
      await oldCompletion;
    });
    expect(initial.setLoading).toHaveBeenLastCalledWith(true);
    let duplicate!: Promise<boolean>;
    act(() => {
      duplicate = hook.result.current.sendMessageHandler();
    });
    let duplicateResult!: boolean;
    await act(async () => {
      newCreate.resolve(frame('new-frame'));
      await newCompletion;
      duplicateResult = await duplicate;
    });
    expect(callsBeforeOldEnd).toBe(2);
    expect(duplicateResult).toBe(false);
    expect(create).toHaveBeenCalledTimes(2);
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith('/conversation/new-frame');
    expect(sessionStorage.getItem('acp_initial_message_old-frame')).toBeNull();
  });
  it('keeps a draft deny through one-shot navigation cleanup but resets it for an actual new draft', () => {
    const initial = { baseAgentName: 'OPERON', ownerId: 'owner', navigationKey: 'new-draft', resetRequested: true };
    const hook = renderHook((value) => useGuidSessionOptions(value), { initialProps: initial });
    const generation = hook.result.current.draftGeneration;
    act(() => {
      hook.result.current.delegationControl.change(false);
    });
    expect(hook.result.current.delegationPolicyOverride).toBe('deny');
    hook.rerender({ ...initial, navigationKey: 'cleanup-replace', resetRequested: false });
    expect(hook.result.current.draftGeneration).toBe(generation);
    expect(hook.result.current.delegationPolicyOverride).toBe('deny');
    const staleControl = hook.result.current.delegationControl;
    hook.rerender({ ...initial, navigationKey: 'next-new-draft' });
    expect(hook.result.current.delegationPolicyOverride).toBeUndefined();
    act(() => {
      staleControl.change(false);
    });
    expect(hook.result.current.delegationPolicyOverride).toBeUndefined();
    expect(hook.result.current.delegationControl.enabled).toBe(true);
  });
});
