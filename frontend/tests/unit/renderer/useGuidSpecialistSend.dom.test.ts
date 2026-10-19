import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { useGuidSend, type GuidSendDeps } from '@/renderer/pages/guid/hooks/useGuidSend';
import { specialistSnapshot } from '../xscience/sessionSpecialistFixtures';
import type { SessionSpecialistCommand } from '@/renderer/services/runtime/sessionSpecialistTypes';
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
function snapshot(revision = 0, name = 'AGENT_A', id = 'draft-frame') {
  return specialistSnapshot(revision, name, {
    scope: { frame_id: id, frame_incarnation_id: `${id}-inc`, root_frame_id: id, root_incarnation_id: `${id}-inc` },
    branch: { id: '', generation: 0 },
    prepared_branch: { id: '', generation: 0 },
  });
}
function mutation(command: SessionSpecialistCommand, id = 'draft-frame') {
  const revision = command.expected_revision + 1,
    name = command.desired_agent || 'DEFAULT_LOGICAL';
  const current = snapshot(revision, name, id);
  return {
    schema: 'x-science.session-specialist-mutation.v1',
    replayed: false,
    receipt: {
      command,
      accepted_revision: revision,
      snapshot_id: current.desired.snapshot_id,
      accepted_at: '2026-10-19T00:00:00Z',
    },
    snapshot: { ...current, desired: { ...current.desired, is_default: command.use_default === true } },
  };
}
const posts = () => request.mock.calls.filter(([method]) => method === 'POST');
function deferred() {
  let resolve!: (v: unknown) => void;
  const promise = new Promise<unknown>((done) => {
    resolve = done;
  });
  return { resolve, promise };
}
function deps(): GuidSendDeps {
  return {
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
    sessionOptions: {
      delegation: false,
      memory: true,
      autoReview: false,
      targetAgent: 'OLD_LOCAL_TARGET',
      goalText: null,
      asRoutine: false,
    },
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
    specialistIntent: { id: 'choice-b', name: 'AGENT_B' },
    draftGeneration: 0,
  };
}
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
  request.mockImplementation(async (method, _path, command) => (method === 'GET' ? snapshot() : mutation(command)));
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.restoreAllMocks();
});
it('confirms explicit None before handing off one first message with the exact prepared reference', async () => {
  const pending = deferred(),
    value = { ...deps(), specialistIntent: { id: 'none', name: null } };
  request.mockImplementation((method) => (method === 'GET' ? Promise.resolve(snapshot()) : pending.promise));
  const { result } = renderHook(() => useGuidSend(value));
  let completion!: Promise<boolean>;
  act(() => {
    completion = result.current.sendMessageHandler();
  });
  await vi.waitFor(() => expect(posts()).toHaveLength(1));
  expect(posts()[0][2]).toMatchObject({ desired_agent: '', use_default: true });
  expect(sessionStorage.length).toBe(0);
  expect(navigate).not.toHaveBeenCalled();
  await act(async () => {
    pending.resolve(mutation(posts()[0][2]));
    expect(await completion).toBe(true);
  });
  const initial = JSON.parse(sessionStorage.getItem('acp_initial_message_draft-frame')!);
  expect(initial.session_options).toMatchObject({
    target_agent: 'DEFAULT_LOGICAL',
    specialist_binding: { scope: { frame_id: 'draft-frame' }, branch: { id: '', generation: 0 }, revision: 1 },
  });
  expect(create).toHaveBeenCalledTimes(1);
});
it('reuses the materialized conversation and original command after a lost specialist ACK', async () => {
  let lost = true;
  request.mockImplementation(async (method, _path, command) => {
    if (method === 'GET') return snapshot();
    if (lost) {
      lost = false;
      throw new TypeError('lost ACK');
    }
    return mutation(command);
  });
  const { result } = renderHook(() => useGuidSend(deps()));
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(false));
  const command = posts()[0][2];
  expect(sessionStorage.length).toBe(0);
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(true));
  expect(posts()[1][2]).toBe(command);
  expect(create).toHaveBeenCalledTimes(1);
});
it('does not rebase a known conflict until the user makes a new explicit selection', async () => {
  let conflict = true;
  request.mockImplementation(async (method, _path, command) => {
    if (method === 'GET') return conflict ? snapshot() : snapshot(2, 'AGENT_C');
    if (conflict) {
      conflict = false;
      throw new BackendHttpError({
        method: 'POST',
        path: '/',
        status: 409,
        body: { code: 'SESSION_SPECIALIST_REVISION_CONFLICT' },
      });
    }
    return mutation(command);
  });
  const value = deps(),
    { result, rerender } = renderHook((input) => useGuidSend(input), { initialProps: value });
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(false));
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(false));
  expect(posts()).toHaveLength(1);
  rerender({ ...value, specialistIntent: { id: 'explicit-reselect', name: 'AGENT_B' } });
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(true));
  expect(posts()).toHaveLength(2);
  expect(posts()[1][2].expected_revision).toBe(2);
  expect(create).toHaveBeenCalledTimes(1);
});
it('does not create another task after navigation lost its result and retains the original message nonce', async () => {
  navigate.mockRejectedValueOnce(new Error('navigation result lost')).mockResolvedValue(undefined);
  const { result } = renderHook(() => useGuidSend(deps()));
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(false));
  const original = sessionStorage.getItem('acp_initial_message_draft-frame');
  expect(original).not.toBeNull();
  const requestsBeforeRetry = request.mock.calls.length;
  request.mockResolvedValue(snapshot(2, 'AGENT_C'));
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(true));
  expect(sessionStorage.getItem('acp_initial_message_draft-frame')).toBe(original);
  expect(create).toHaveBeenCalledTimes(1);
  expect(posts()).toHaveLength(1);
  expect(request).toHaveBeenCalledTimes(requestsBeforeRetry);
});
it('keeps an edited draft usable after storage failed before any handoff', async () => {
  const original = Storage.prototype.setItem;
  let fail = true;
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
    if (key.startsWith('acp_initial_message_') && fail) {
      fail = false;
      throw new Error('quota');
    }
    return original.call(this, key, value);
  });
  const value = deps(),
    { result, rerender } = renderHook((input) => useGuidSend(input), { initialProps: value });
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(false));
  request.mockResolvedValue(snapshot(1, 'AGENT_B'));
  rerender({ ...value, input: 'edited synthetic input' });
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(true));
  expect(JSON.parse(sessionStorage.getItem('acp_initial_message_draft-frame')!).input).toBe('edited synthetic input');
  expect(create).toHaveBeenCalledTimes(1);
  expect(posts()).toHaveLength(1);
});
it('takes a new draft owner while an old specialist GET is pending and ignores its late result', async () => {
  const pending = deferred();
  create.mockResolvedValueOnce(frame('old')).mockResolvedValueOnce(frame('new'));
  request.mockImplementation(async (method, path, command) => {
    const id = path.includes('/old/') ? 'old' : 'new';
    if (method === 'GET') return id === 'old' ? pending.promise : snapshot(0, 'AGENT_A', id);
    return mutation(command, id);
  });
  const value = deps(),
    { result, rerender } = renderHook((input) => useGuidSend(input), { initialProps: value });
  let old!: Promise<boolean>;
  act(() => {
    old = result.current.sendMessageHandler();
  });
  await vi.waitFor(() => expect(request).toHaveBeenCalled());
  rerender({
    ...value,
    draftGeneration: 1,
    input: 'new synthetic draft',
    specialistIntent: { id: 'new-choice', name: 'AGENT_C' },
  });
  await act(async () => expect(await result.current.sendMessageHandler()).toBe(true));
  await act(async () => {
    pending.resolve(snapshot(0, 'AGENT_A', 'old'));
    expect(await old).toBe(false);
  });
  expect(navigate).toHaveBeenCalledTimes(1);
  expect(navigate).toHaveBeenCalledWith('/conversation/new');
  expect(sessionStorage.getItem('acp_initial_message_old')).toBeNull();
  expect(create).toHaveBeenCalledTimes(2);
});
