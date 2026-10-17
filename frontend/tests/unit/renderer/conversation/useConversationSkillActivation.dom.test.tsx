import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useConversationSkillActivation } from '@/renderer/pages/conversation/platforms/acp/useConversationSkillActivation';

const mocks = vi.hoisted(() => ({ detail: vi.fn(), library: vi.fn(), profiles: vi.fn(), attach: vi.fn() }));
vi.mock('@/common', () => ({
  ipcBridge: { assistants: { get: { invoke: (...args: unknown[]) => mocks.detail(...args) } } },
}));
vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: (...args: unknown[]) => mocks.library(...args),
}));
vi.mock('@/renderer/services/agents/xScienceExpertProfiles', () => ({
  loadXScienceExpertProfiles: (...args: unknown[]) => mocks.profiles(...args),
  updateXScienceExpertSkills: (...args: unknown[]) => mocks.attach(...args),
}));
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const makeOptions = () => ({
  authority: {},
  ownerId: 'owner-a',
  assistantId: 'assistant-fixture' as string | undefined,
  runtimeAgentName: undefined as string | undefined,
  locale: 'en-US',
  mountedRef: { current: true },
  disabled: false,
  refreshCatalogs: vi.fn().mockResolvedValue(['existing-workflow', 'new-workflow']),
  onSelectSkill: vi.fn(),
});
const detail = { id: 'assistant-fixture', engine: { agent_id: 'canonical-expert' } };
const profile = { name: 'canonical-expert', enabled: true, skillNames: ['existing-workflow'] };

describe('SkillActivation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.detail.mockResolvedValue(detail);
    mocks.library.mockResolvedValue([{ name: 'new-workflow', enabled: true }]);
    mocks.profiles.mockResolvedValue([profile]);
    mocks.attach.mockResolvedValue({ ...profile, skillNames: [...profile.skillNames, 'new-workflow'] });
  });

  it('requires explicit confirmation, attaches just one Skill and selects only after fresh admission', async () => {
    const options = makeOptions();
    const { result } = renderHook(() => useConversationSkillActivation(options));
    act(() => result.current.request('new-workflow'));
    expect(result.current.state?.phase).toBe('confirm');
    expect(mocks.detail).not.toHaveBeenCalled();
    expect(mocks.attach).not.toHaveBeenCalled();
    expect(options.onSelectSkill).not.toHaveBeenCalled();
    await act(async () => {
      await result.current.confirm();
    });
    expect(mocks.detail).toHaveBeenCalledExactlyOnceWith({ id: 'assistant-fixture', locale: 'en-US' });
    expect(mocks.attach).toHaveBeenCalledExactlyOnceWith('canonical-expert', [], ['new-workflow']);
    expect(options.refreshCatalogs).toHaveBeenCalledTimes(1);
    expect(options.onSelectSkill).toHaveBeenCalledExactlyOnceWith('new-workflow');
    expect(result.current.state).toBeNull();
  });

  it('deduplicates same-tick confirmation while the request is pending', async () => {
    const pending = deferred<Array<{ name: string; enabled: boolean }>>();
    mocks.library.mockReturnValue(pending.promise);
    const options = makeOptions();
    const { result } = renderHook(() => useConversationSkillActivation(options));
    act(() => result.current.request('new-workflow'));
    let first!: Promise<void>;
    let duplicate!: Promise<void>;
    act(() => {
      first = result.current.confirm();
      duplicate = result.current.confirm();
    });
    expect(mocks.library).toHaveBeenCalledTimes(1);
    expect(result.current.state?.pending).toBe(true);
    act(() => result.current.cancel());
    expect(result.current.state).not.toBeNull();
    await act(async () => {
      pending.resolve([{ name: 'new-workflow', enabled: true }]);
      await Promise.all([first, duplicate]);
    });
    expect(mocks.attach).toHaveBeenCalledTimes(1);
    expect(options.onSelectSkill).toHaveBeenCalledTimes(1);
  });

  it('retries only refresh after a committed binding, without another attachment', async () => {
    const options = makeOptions();
    options.refreshCatalogs.mockRejectedValueOnce(new Error('synthetic-private-failure'));
    const { result } = renderHook(() => useConversationSkillActivation(options));
    act(() => result.current.request('new-workflow'));
    await act(async () => {
      await result.current.confirm();
    });
    expect(result.current.state?.phase).toBe('refresh-error');
    expect(result.current.state?.attached).toBe(true);
    expect(options.onSelectSkill).not.toHaveBeenCalled();
    await act(async () => {
      await result.current.confirm();
    });
    expect(mocks.attach).toHaveBeenCalledTimes(1);
    expect(mocks.library).toHaveBeenCalledTimes(1);
    expect(options.refreshCatalogs).toHaveBeenCalledTimes(2);
    expect(options.onSelectSkill).toHaveBeenCalledTimes(1);
  });

  it.each(['conversation', 'account-aba', 'busy-aba', 'unmount'] as const)(
    'fences late metadata after %s',
    async (transition) => {
      const pending = deferred<typeof detail>();
      mocks.detail.mockReturnValue(pending.promise);
      const options = makeOptions();
      const view = renderHook(({ value }) => useConversationSkillActivation(value), {
        initialProps: { value: options },
      });
      act(() => view.result.current.request('new-workflow'));
      let work!: Promise<void>;
      act(() => {
        work = view.result.current.confirm();
      });
      await waitFor(() => expect(mocks.detail).toHaveBeenCalledTimes(1));
      if (transition === 'unmount') view.unmount();
      else if (transition === 'conversation') view.rerender({ value: { ...options, authority: {} } });
      else if (transition === 'account-aba') {
        view.rerender({ value: { ...options, ownerId: 'owner-b' } });
        view.rerender({ value: options });
      } else {
        view.rerender({ value: { ...options, disabled: true } });
        view.rerender({ value: options });
      }
      options.mountedRef.current = transition !== 'unmount';
      await act(async () => {
        pending.resolve(detail);
        await work;
      });
      expect(mocks.profiles).not.toHaveBeenCalled();
      expect(mocks.attach).not.toHaveBeenCalled();
      expect(options.onSelectSkill).not.toHaveBeenCalled();
      if (transition !== 'unmount') expect(view.result.current.state).toBeNull();
    }
  );

  it('does not select through an assistant exclusion even after a confirmed profile binding', async () => {
    const options = makeOptions();
    options.refreshCatalogs.mockResolvedValue(['existing-workflow']);
    const { result } = renderHook(() => useConversationSkillActivation(options));
    act(() => result.current.request('new-workflow'));
    await act(async () => {
      await result.current.confirm();
    });
    expect(result.current.state?.phase).toBe('refresh-error');
    expect(options.onSelectSkill).not.toHaveBeenCalled();
    await act(async () => {
      await result.current.confirm();
    });
    expect(mocks.attach).toHaveBeenCalledTimes(1);
    expect(options.onSelectSkill).not.toHaveBeenCalled();
    act(() => result.current.cancel());
    expect(result.current.state).toBeNull();
  });

  it.each(['disabled-library', 'wrong-assistant', 'unknown-expert', 'disabled-expert'] as const)(
    'rejects %s without binding or selection',
    async (failure) => {
      if (failure === 'disabled-library') mocks.library.mockResolvedValue([{ name: 'new-workflow', enabled: false }]);
      if (failure === 'wrong-assistant') mocks.detail.mockResolvedValue({ ...detail, id: 'another-assistant' });
      if (failure === 'unknown-expert') mocks.profiles.mockResolvedValue([{ ...profile, name: 'unrelated-expert' }]);
      if (failure === 'disabled-expert') mocks.profiles.mockResolvedValue([{ ...profile, enabled: false }]);
      const options = makeOptions();
      const { result } = renderHook(() => useConversationSkillActivation(options));
      act(() => result.current.request('new-workflow'));
      await act(async () => {
        await result.current.confirm();
      });
      expect(result.current.state?.phase).toBe('error');
      expect(mocks.attach).not.toHaveBeenCalled();
      expect(options.onSelectSkill).not.toHaveBeenCalled();
    }
  );

  it('uses exact server runtime identity for a legacy conversation, not a display-name guess', async () => {
    const options = { ...makeOptions(), assistantId: undefined, runtimeAgentName: 'canonical-expert' };
    const { result } = renderHook(() => useConversationSkillActivation(options));
    act(() => result.current.request('new-workflow'));
    await act(async () => {
      await result.current.confirm();
    });
    expect(mocks.detail).not.toHaveBeenCalled();
    expect(mocks.attach).toHaveBeenCalledExactlyOnceWith('canonical-expert', [], ['new-workflow']);
    expect(options.onSelectSkill).toHaveBeenCalledTimes(1);
  });

  it('skips an existing attachment but still requires current assistant admission', async () => {
    mocks.profiles.mockResolvedValue([{ ...profile, skillNames: ['existing-workflow', 'new-workflow'] }]);
    const options = makeOptions();
    const { result } = renderHook(() => useConversationSkillActivation(options));
    act(() => result.current.request('new-workflow'));
    await act(async () => {
      await result.current.confirm();
    });
    expect(mocks.attach).not.toHaveBeenCalled();
    expect(options.refreshCatalogs).toHaveBeenCalledTimes(1);
    expect(options.onSelectSkill).toHaveBeenCalledTimes(1);
  });

  it('does not open activation with missing identity or a busy task', () => {
    const options = { ...makeOptions(), assistantId: undefined };
    const view = renderHook(({ value }) => useConversationSkillActivation(value), { initialProps: { value: options } });
    expect(view.result.current.canActivate).toBe(false);
    act(() => view.result.current.request('new-workflow'));
    expect(view.result.current.state).toBeNull();
    view.rerender({ value: { ...options, runtimeAgentName: undefined, disabled: true } });
    act(() => view.result.current.request('new-workflow'));
    expect(mocks.library).not.toHaveBeenCalled();
  });
});
