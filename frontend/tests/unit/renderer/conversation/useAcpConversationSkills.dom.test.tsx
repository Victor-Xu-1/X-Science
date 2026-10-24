import { bindRealtimeRuntime } from '@/common/adapter/httpBridge';
import { parseRealtimeMessage } from '@/common/adapter/realtimeProtocol';
import { createRealtimeRuntime, type RealtimeRuntimeClientObservers } from '@/common/adapter/realtimeRuntime';
import React, { type PropsWithChildren } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import useSWR, { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAcpConversationSkills } from '@/renderer/pages/conversation/platforms/acp/useAcpConversationSkills';
import type { XScienceComposerCapabilities } from '@/renderer/services/xScienceGateway';
import type { XScienceSkill } from '@/renderer/services/xScienceCapabilities';

const mocks = vi.hoisted(() => ({ library: vi.fn(), capabilities: vi.fn() }));
vi.mock('@/common', () => ({ ipcBridge: {} }));
vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: (...args: unknown[]) => mocks.library(...args),
}));
vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceComposerCapabilities: (...args: unknown[]) => mocks.capabilities(...args),
}));
vi.mock('@/renderer/services/agents/xScienceExpertProfiles', () => ({
  loadXScienceExpertProfiles: vi.fn(),
  updateXScienceExpertSkills: vi.fn(),
}));

type Options = Omit<Parameters<typeof useAcpConversationSkills>[0], 'mutateComposerCapabilities'>;
const makeOptions = (): Options => ({
  conversationId: 'conversation-a',
  ownerId: 'owner-a',
  authority: {},
  mountedRef: { current: true },
  disabled: false,
  isXScience: true,
  isMobile: true,
  assistantId: 'assistant-a',
  locale: 'en-US',
  loadedSkills: [],
  fetchSlashCommands: vi.fn().mockResolvedValue(undefined),
  onSelectSkill: vi.fn(),
});
const skill = (name: string): XScienceSkill => ({
  name,
  displayName: name,
  description: 'Synthetic personal workflow',
  source: 'personal',
  category: null,
  license: null,
  skillId: name,
  updatedAt: null,
  attachedAgents: [],
  enabled: true,
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const useSubject = (options: Options) => {
  const { mutate } = useSWR<XScienceComposerCapabilities>(
    ['fixture-capabilities', options.ownerId, options.conversationId],
    null,
    { fallbackData: { skills: [], mcpStatuses: [] } }
  );
  return useAcpConversationSkills({ ...options, mutateComposerCapabilities: mutate });
};
const wrapper = () => {
  const cache = new Map();
  return function IsolatedSWR({ children }: PropsWithChildren) {
    return (
      <SWRConfig value={{ provider: () => cache, dedupingInterval: 0, shouldRetryOnError: false }}>
        {children}
      </SWRConfig>
    );
  };
};

let releaseCatalogRuntime: (() => void) | undefined;

async function connectCatalogRuntime() {
  let observers!: RealtimeRuntimeClientObservers;
  const runtime = createRealtimeRuntime({
    createClient: (next) => {
      observers = next;
      return { setIdentity: async () => undefined, dispose: () => undefined };
    },
  });
  const unbind = bindRealtimeRuntime(runtime);
  releaseCatalogRuntime = () => {
    unbind();
    runtime.dispose();
    releaseCatalogRuntime = undefined;
  };
  await runtime.setIdentity('catalog-owner');
  let sequence = 0;
  return () => {
    sequence += 1;
    const parsed = parseRealtimeMessage(
      JSON.stringify({
        type: 'skills.catalog.changed',
        _event_id: `catalog-${sequence}`,
        _sequence: sequence,
        _kind: 'invalidate',
      })
    );
    if (!parsed.ok) throw new Error('Controlled catalog event must satisfy the real protocol');
    observers.onMessage(parsed.message);
  };
}

afterEach(() => releaseCatalogRuntime?.());

describe('conversation Skill catalog readback barrier', () => {
  it('refreshes the current conversation and mobile library after a remote catalog invalidation', async () => {
    const invalidate = await connectCatalogRuntime();
    const options = makeOptions();
    const view = renderHook(() => useSubject(options), { wrapper: wrapper() });
    await waitFor(() => expect(mocks.library).toHaveBeenCalledTimes(1));
    mocks.library.mockResolvedValue([skill('remote-workflow')]);
    await act(async () => invalidate());
    await waitFor(() => expect(view.result.current.mobileLibraryNames).toEqual(['remote-workflow']));
    expect(mocks.capabilities).toHaveBeenCalledExactlyOnceWith('conversation-a');
    expect(options.fetchSlashCommands).toHaveBeenCalledExactlyOnceWith({ fresh: true });
    expect(options.onSelectSkill).not.toHaveBeenCalled();
    view.unmount();
    await act(async () => invalidate());
    expect(mocks.capabilities).toHaveBeenCalledTimes(1);
  });

  it('coalesces remote catalog invalidations behind the existing conversation readback barrier', async () => {
    const invalidate = await connectCatalogRuntime();
    const options = makeOptions();
    const view = renderHook(() => useSubject(options), { wrapper: wrapper() });
    await waitFor(() => expect(mocks.library).toHaveBeenCalledTimes(1));
    const required = deferred<XScienceComposerCapabilities>();
    mocks.capabilities.mockReturnValueOnce(required.promise);
    mocks.library.mockResolvedValue([skill('remote-workflow')]);
    let settled!: Promise<string[]>;
    await act(async () => {
      settled = view.result.current.refreshCatalogs(() => true);
      await Promise.resolve();
    });
    expect(mocks.capabilities).toHaveBeenCalledTimes(1);
    await act(async () => {
      invalidate();
      invalidate();
      await Promise.resolve();
    });
    expect(mocks.capabilities).toHaveBeenCalledTimes(1);
    await act(async () => {
      required.resolve({ skills: ['new-workflow'], mcpStatuses: [] });
      await expect(settled).resolves.toEqual(['new-workflow']);
    });
    await waitFor(() => expect(mocks.capabilities).toHaveBeenCalledTimes(2));
    expect(mocks.library).toHaveBeenCalledTimes(3);
    expect(options.fetchSlashCommands).toHaveBeenCalledTimes(2);
    expect(options.onSelectSkill).not.toHaveBeenCalled();
  });

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.library.mockResolvedValue([]);
    mocks.capabilities.mockResolvedValue({ skills: ['new-workflow'], mcpStatuses: [] });
  });

  it('updates the mounted mobile personal catalog before the existing refresh barrier completes', async () => {
    const options = makeOptions();
    const { result } = renderHook(() => useSubject(options), { wrapper: wrapper() });
    await waitFor(() => expect(mocks.library).toHaveBeenCalledTimes(1));
    expect(result.current.mobileLibraryNames).toEqual([]);
    mocks.library.mockResolvedValue([skill('new-workflow')]);
    let admitted: string[] | undefined;
    await act(async () => {
      admitted = await result.current.refreshCatalogs(() => true);
    });
    expect(admitted).toEqual(['new-workflow']);
    expect(result.current.mobileLibraryNames).toEqual(['new-workflow']);
    expect(mocks.library).toHaveBeenCalledTimes(2);
    expect(options.fetchSlashCommands).toHaveBeenCalledExactlyOnceWith({ fresh: true });
    expect(options.onSelectSkill).not.toHaveBeenCalled();
  });

  it('rejects a failed mobile read and retries only the existing readback barrier', async () => {
    const options = makeOptions();
    const { result } = renderHook(() => useSubject(options), { wrapper: wrapper() });
    await waitFor(() => expect(mocks.library).toHaveBeenCalledTimes(1));
    mocks.library.mockRejectedValueOnce(new Error('synthetic read failure'));
    await act(async () => {
      await expect(result.current.refreshCatalogs(() => true)).rejects.toThrow('Skill catalog refresh failed');
    });
    expect(result.current.mobileLibraryNames).toEqual([]);
    mocks.library.mockResolvedValue([skill('new-workflow')]);
    await act(async () => {
      await result.current.refreshCatalogs(() => true);
    });
    expect(result.current.mobileLibraryNames).toEqual(['new-workflow']);
    expect(mocks.library).toHaveBeenCalledTimes(3);
    expect(options.fetchSlashCommands).toHaveBeenCalledTimes(2);
    expect(options.onSelectSkill).not.toHaveBeenCalled();
  });

  it.each(['owner', 'conversation'] as const)('fences the old mobile response after a %s switch', async (change) => {
    const invalidate = await connectCatalogRuntime();
    const options = makeOptions();
    const pending = deferred<XScienceSkill[]>();
    let initial = true;
    let switched = false;
    let current = true;
    mocks.library.mockImplementation(() => {
      if (switched) return Promise.resolve([skill('new-scope-workflow')]);
      if (initial) {
        initial = false;
        return Promise.resolve([]);
      }
      return pending.promise;
    });
    const view = renderHook(({ value }) => useSubject(value), {
      initialProps: { value: options },
      wrapper: wrapper(),
    });
    await waitFor(() => expect(mocks.library).toHaveBeenCalledTimes(1));
    let settled!: Promise<unknown>;
    await act(async () => {
      settled = view.result.current
        .refreshCatalogs(() => current)
        .then(
          (): null => null,
          (error: unknown) => error
        );
      await Promise.resolve();
    });
    await act(async () => invalidate());
    current = false;
    switched = true;
    view.rerender({
      value: {
        ...options,
        authority: {},
        ...(change === 'owner' ? { ownerId: 'owner-b' } : { conversationId: 'conversation-b' }),
      },
    });
    await waitFor(() => expect(view.result.current.mobileLibraryNames).toEqual(['new-scope-workflow']));
    let outcome: unknown;
    await act(async () => {
      pending.resolve([skill('old-scope-workflow')]);
      outcome = await settled;
    });
    expect(outcome).toBeInstanceOf(Error);
    expect(view.result.current.mobileLibraryNames).toEqual(['new-scope-workflow']);
    expect(mocks.capabilities).toHaveBeenCalledTimes(1);
    expect(options.onSelectSkill).not.toHaveBeenCalled();
  });

  it('does not load an inactive mobile catalog or start any stale refresh IO', async () => {
    const inactiveOptions: Partial<Options>[] = [
      { isMobile: false },
      { disabled: true },
      { isXScience: false },
      { assistantId: undefined },
    ];
    for (const inactive of inactiveOptions) {
      const options = { ...makeOptions(), ...inactive };
      const view = renderHook(() => useSubject(options), { wrapper: wrapper() });
      await act(async () => {
        await view.result.current.refreshCatalogs(() => true);
      });
      expect(mocks.library).not.toHaveBeenCalled();
      view.unmount();
    }
    const options = makeOptions();
    const view = renderHook(() => useSubject(options), { wrapper: wrapper() });
    await waitFor(() => expect(mocks.library).toHaveBeenCalledTimes(1));
    vi.clearAllMocks();
    await act(async () => {
      await expect(view.result.current.refreshCatalogs(() => false)).rejects.toThrow();
    });
    expect(mocks.library).not.toHaveBeenCalled();
    expect(mocks.capabilities).not.toHaveBeenCalled();
    expect(options.fetchSlashCommands).not.toHaveBeenCalled();
  });
});
