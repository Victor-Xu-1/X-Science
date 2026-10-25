import { act, cleanup, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import React, { useRef, useState } from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { loadXScienceMemoryEnabled } from '@/renderer/services/xScienceMemory';
import { createXScienceSessionDefaults } from '@/renderer/services/xScienceSessionOptions';
import type { XScienceSessionMemoryControl } from '@/renderer/services/xScienceSessionOptions';
import {
  useXScienceMemoryAvailability,
  sessionMemoryPresentation,
  type XScienceMemoryAvailability,
} from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { useAcpSessionOptionControls } from '@/renderer/pages/conversation/platforms/acp/useAcpSessionOptionControls';
import { useAcpMobileActionSheetController } from '@/renderer/pages/conversation/platforms/acp/useAcpMobileActionSheetController';
import { useSessionComputeControls } from '@/renderer/hooks/xScience/runtime/useSessionComputeControls';
import MobileActionSheet from '@/renderer/components/chat/MobileActionSheet';
import { useTranslation } from 'react-i18next';
import { useXScienceSessionOptionsState } from '@/renderer/pages/conversation/platforms/acp/useXScienceSessionOptionsState';
import { clearXScienceFrameReads } from '@/renderer/services/xScienceFrameReads';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({ persist: vi.fn(), refresh: vi.fn() }));
vi.mock('@/renderer/services/xScienceCatalog', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceCatalog')>()),
  loadXScienceAssistants: async () => [],
}));
vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: async () => [],
  loadXScienceSessionComputeProviders: async () => [],
  setXScienceSessionComputeProvider: vi.fn(),
}));

function MenuHarness({ availability }: { availability: XScienceMemoryAvailability }) {
  const [value, setValue] = useState({ ...createXScienceSessionDefaults(), memory: true });
  return (
    <MemoryRouter>
      <XScienceSessionOptionsMenu
        value={value}
        onChange={setValue}
        memoryControl={{
          availability,
          refresh: mocks.refresh,
          current: () => true,
          canUpdate: () => availability.status === 'ready' && availability.enabled,
        }}
        onPersistentOptionChange={async (key, checked) => {
          await mocks.persist(key, checked);
          setValue((current) => ({ ...current, [key]: checked }));
        }}
      />
      <output data-testid='preference'>{String(value.memory)}</output>
    </MemoryRouter>
  );
}

function SharedComposerHarness({
  sessionMemory,
  sessionReview,
  legacyReady = true,
}: {
  sessionMemory?: XScienceSessionMemoryControl;
  sessionReview?: XScienceSessionMemoryControl;
  legacyReady?: boolean;
} = {}) {
  const { t, i18n } = useTranslation();
  const [value, setValue] = useState({ ...createXScienceSessionDefaults(), memory: true });
  const [mobileOpen, setMobileOpen] = useState(false);
  const computeControl = useSessionComputeControls({ rootFrameId: null });
  const mounted = useRef(true);
  const owner = useRef({ conversationId: 'synthetic-frame', ownerId: 'synthetic-owner' });
  const { memoryControl, updatePersistentSessionOption, openMobileControls } = useAcpSessionOptionControls(
    'synthetic-frame',
    undefined,
    owner,
    mounted,
    setValue,
    setMobileOpen,
    sessionMemory,
    undefined,
    sessionReview
  );
  const entries = useAcpMobileActionSheetController({
    isMobile: true,
    runtimeMode: null,
    runtimeThoughtLevel: null,
    runtimeConfig: { setStatus: { state: 'idle' } },
    canSwitchModel: false,
    model_info: null,
    isModelListLoading: false,
    modelListError: null,
    retryModelList: async () => undefined,
    selectModel: () => undefined,
    xscienceSessionOptions: value,
    sessionOptionsReady: legacyReady,
    updatePersistentSessionOption,
    memoryControl,
    autoReviewControl: sessionReview,
    computeControl,
    handleSheetModeChange: async () => undefined,
    handleThoughtLevelSetOption: async () => true,
    attachEntries: [],
    loadedSkills: [],
    loadedMcpStatuses: [],
    selectedSkillNames: [],
    selectedMcpServerIds: [],
    onSelectSkill: () => undefined,
    onSelectMcpServer: () => undefined,
    t,
    i18n,
  });
  return (
    <MemoryRouter>
      <button onClick={openMobileControls}>Mobile controls</button>
      {!mobileOpen && (
        <XScienceSessionOptionsMenu
          value={value}
          onChange={setValue}
          memoryControl={memoryControl}
          autoReviewControl={sessionReview}
          autoReviewDisabled={!sessionReview && !legacyReady}
          computeControl={computeControl}
          disabled={!legacyReady}
          memoryDisabled={false}
          onPersistentOptionChange={updatePersistentSessionOption}
        />
      )}
      <MobileActionSheet
        open={mobileOpen}
        onClose={() => setMobileOpen(false)}
        entries={entries}
        title='Mobile controls'
      />
      <output data-testid='shared-preference'>{String(value.memory)}</output>
      <output data-testid='capability'>{JSON.stringify(memoryControl.availability)}</output>
    </MemoryRouter>
  );
}

describe('session Memory availability', () => {
  it('AutoReview has the same unknown checkbox and GET-only Retry action in desktop and mobile', async () => {
    const preference: XScienceSessionMemoryControl = {
      status: 'unknown',
      pending: false,
      current: () => true,
      capture: () => null,
      refresh: vi.fn(),
      update: vi.fn(),
    };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => Response.json({ enabled: true }))
    );
    await renderWithI18n(<SharedComposerHarness sessionReview={preference} legacyReady={false} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
    const desktop = await screen.findByTestId('session-config-row-autoReview');
    expect(desktop).toHaveAttribute('aria-checked', 'mixed');
    expect(desktop).toBeDisabled();
    const retry = screen.getByRole('menuitem', { name: 'Refresh automatic review preference' });
    expect(retry).not.toHaveAttribute('aria-checked');
    fireEvent.click(retry);
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Conversation options' }), { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Mobile controls' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-session-options'));
    const mobile = await screen.findByTestId('mobile-action-sheet-option-autoReview');
    expect(mobile).toHaveAttribute('role', 'menuitemcheckbox');
    expect(mobile).toHaveAttribute('aria-checked', 'mixed');
    expect(mobile).toBeDisabled();
    const mobileRetry = screen.getByTestId('mobile-action-sheet-option-autoReview-retry');
    expect(mobileRetry).toHaveAttribute('role', 'menuitem');
    expect(mobileRetry).not.toHaveAttribute('aria-checked');
    const before = vi.mocked(preference.refresh).mock.calls.length;
    fireEvent.click(mobileRetry);
    expect(preference.refresh).toHaveBeenCalledTimes(before + 1);
    expect(preference.update).not.toHaveBeenCalled();
  });

  it.each(['incarnation', 'account'])(
    'AutoReview ignores the old %s ACK and finally while a new owner write is pending',
    async (boundary) => {
      setRendererAccountOwner('review-account-a');
      const writes: Array<(response: Response) => void> = [];
      vi.stubGlobal(
        'fetch',
        vi.fn<typeof fetch>(async (_url, init) => {
          if (init?.method === 'POST') return new Promise<Response>((done) => writes.push(done));
          return Response.json({
            id: 'root',
            root_frame_id: 'root',
            parent_frame_id: null,
            project_id: 'project',
            input_data: { verifier_mode: 'off', memory_mode: 'on' },
          });
        })
      );
      const hook = renderHook(
        ({ incarnation }) =>
          useXScienceSessionOptionsState('root', undefined, { ownerId: 'owner', frameIncarnationId: incarnation }),
        { initialProps: { incarnation: 'old' } }
      );
      await waitFor(() => expect(hook.result.current.autoReview.status).toBe('ready'));
      let old!: Promise<void>;
      act(() => {
        old = hook.result.current.autoReview.update(true);
      });
      if (boundary === 'account')
        act(() => {
          setRendererAccountOwner('review-account-b');
        });
      hook.rerender({ incarnation: boundary === 'incarnation' ? 'new' : 'old' });
      await waitFor(() => expect(hook.result.current.autoReview.status).toBe('ready'));
      let fresh!: Promise<void>;
      act(() => {
        fresh = hook.result.current.autoReview.update(true);
      });
      await act(async () => {
        writes[0](Response.json({ status: 'ok' }));
        await old;
      });
      expect(hook.result.current.autoReview.status).toBe('writing');
      expect(hook.result.current.autoReview.capture()).toBeNull();
      await act(async () => {
        writes[1](Response.json({ status: 'ok' }));
        await fresh;
      });
      expect(hook.result.current.autoReview.status).toBe('ready');
      expect(hook.result.current.autoReview.capture()).toBe(false);
      expect(writes).toHaveLength(2);
      hook.unmount();
    }
  );

  it.each(['preference', 'retry'])('Memory406 R2 gives the mobile %s its truthful ARIA semantics', async (row) => {
    const preference: XScienceSessionMemoryControl = {
      status: 'unknown',
      pending: false,
      current: () => true,
      capture: () => null,
      refresh: vi.fn(),
      update: vi.fn(),
    };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => Response.json({ enabled: true }))
    );
    await renderWithI18n(<SharedComposerHarness sessionMemory={preference} legacyReady={false} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Mobile controls' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-session-options'));
    const target = await screen.findByTestId(
      `mobile-action-sheet-option-${row === 'preference' ? 'memory' : 'memory-retry'}`
    );
    if (row === 'preference') {
      expect(target).toHaveAttribute('role', 'menuitemcheckbox');
      expect(target).toHaveAttribute('aria-checked', 'mixed');
      expect(target).toBeDisabled();
    } else {
      expect(target).toHaveAttribute('role', 'menuitem');
      expect(target).not.toHaveAttribute('aria-checked');
      expect(target.querySelector('[aria-hidden]')).toBeNull();
      const before = preference.refresh;
      fireEvent.click(target);
      expect(before).toHaveBeenCalledTimes(2);
      expect(preference.update).not.toHaveBeenCalled();
    }
  });
  it('Memory406 treats an unknown session readback as unknown despite globally available Memory', () => {
    const control = {
      availability: { status: 'ready', enabled: true },
      refresh: vi.fn(),
      current: () => true,
      canUpdate: () => true,
      preference: { status: 'unknown', pending: false, refresh: vi.fn(), current: () => true },
    } as unknown as Parameters<typeof sessionMemoryPresentation>[0];
    expect(sessionMemoryPresentation(control, true)).toMatchObject({ checked: undefined, disabled: true });
  });
  beforeEach(() => {
    mocks.persist.mockResolvedValue(undefined);
  });
  afterEach(() => {
    clearXScienceFrameReads();
    cleanup();
    vi.clearAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    resetRendererAccountScopeForTest();
  });

  it('Memory406 leaves a lost-ACK write unknown and retries only the fresh read', async () => {
    let mode = 'on';
    const started = vi.fn();
    const transport = vi.fn<typeof fetch>(async (_url, init) => {
      if (init?.method === 'POST') {
        mode = 'off';
        throw new TypeError('synthetic ACK lost');
      }
      return Response.json({
        id: 'root',
        root_frame_id: 'root',
        parent_frame_id: null,
        project_id: 'project',
        input_data: { memory_mode: mode },
      });
    });
    vi.stubGlobal('fetch', transport);
    const hook = renderHook(() =>
      useXScienceSessionOptionsState('root', undefined, {
        ownerId: 'owner',
        frameIncarnationId: 'inc',
        onMutationStarted: started,
      })
    );
    await waitFor(() => expect(hook.result.current.memory.status).toBe('ready'));
    await act(async () => {
      await expect(hook.result.current.memory.update(false)).rejects.toThrow('ACK lost');
    });
    expect(started).toHaveBeenCalledTimes(1);
    expect(hook.result.current.memory.status).toBe('unknown');
    expect(hook.result.current.options.memory).toBe(true);
    expect(hook.result.current.memory.capture()).toBe(null);
    expect(hook.result.current.memory.pending).toBe(false);
    act(() => hook.result.current.memory.refresh());
    await waitFor(() => expect(hook.result.current.memory.status).toBe('ready'));
    expect(hook.result.current.memory.capture()).toBe(false);
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    hook.unmount();
  });

  it.each(['incarnation', 'account'])(
    'Memory406 ignores old-%s ACK/finally while a new owner write is pending',
    async (boundary) => {
      setRendererAccountOwner('account-a');
      const writes: Array<(response: Response) => void> = [];
      const transport = vi.fn<typeof fetch>(async (_url, init) => {
        if (init?.method === 'POST') return new Promise<Response>((done) => writes.push(done));
        return Response.json({
          id: 'root',
          root_frame_id: 'root',
          parent_frame_id: null,
          project_id: 'project',
          input_data: { memory_mode: 'on' },
        });
      });
      vi.stubGlobal('fetch', transport);
      const hook = renderHook(
        ({ incarnation }) =>
          useXScienceSessionOptionsState('root', undefined, { ownerId: 'owner', frameIncarnationId: incarnation }),
        { initialProps: { incarnation: 'old' } }
      );
      await waitFor(() => expect(hook.result.current.memory.status).toBe('ready'));
      let old!: Promise<void>;
      act(() => {
        old = hook.result.current.memory.update(false);
      });
      if (boundary === 'account')
        act(() => {
          setRendererAccountOwner('account-b');
        });
      hook.rerender({ incarnation: boundary === 'incarnation' ? 'new' : 'old' });
      await waitFor(() => expect(hook.result.current.memory.status).toBe('ready'));
      let fresh!: Promise<void>;
      act(() => {
        fresh = hook.result.current.memory.update(false);
      });
      expect(hook.result.current.memory.pending).toBe(true);
      await act(async () => {
        writes[0](Response.json({ status: 'ok' }));
        await old;
      });
      expect(hook.result.current.memory.status).toBe('writing');
      expect(hook.result.current.memory.pending).toBe(true);
      await act(async () => {
        writes[1](Response.json({ status: 'ok' }));
        await fresh;
      });
      expect(hook.result.current.memory.status).toBe('ready');
      hook.unmount();
    }
  );

  it('Memory406 does not let the legacy session loader overwrite a newer fresh Memory observation', async () => {
    let finishLegacy!: (response: Response) => void;
    let reads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => {
        if (++reads === 3)
          return new Promise<Response>((done) => {
            finishLegacy = done;
          });
        return Response.json({
          id: 'root',
          root_frame_id: 'root',
          parent_frame_id: null,
          project_id: 'project',
          input_data: { memory_mode: 'off', verifier_mode: 'on' },
        });
      })
    );
    const hook = renderHook(() => useXScienceSessionOptionsState('root', undefined, { ownerId: 'owner' }));
    await waitFor(() => expect(hook.result.current.memory.capture()).toBe(false));
    await act(async () => {
      finishLegacy(Response.json({ id: 'root', input_data: { memory_mode: 'on', verifier_mode: 'on' } }));
    });
    await waitFor(() => expect(hook.result.current.loadState.status).toBe('ready'));
    expect(hook.result.current.options.autoReview).toBe(true);
    expect(hook.result.current.memory.capture()).toBe(false);
    hook.unmount();
  });

  it('Memory406 shows the same unknown and read-only Retry in desktop and the real mobile sheet', async () => {
    const refresh = vi.fn(),
      update = vi.fn();
    const preference: XScienceSessionMemoryControl = {
      status: 'unknown',
      pending: false,
      current: () => true,
      capture: () => null,
      refresh,
      update,
    };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async () => Response.json({ enabled: true }))
    );
    await renderWithI18n(<SharedComposerHarness sessionMemory={preference} legacyReady={false} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
    const desktop = await screen.findByTestId('session-config-row-memory');
    await waitFor(() => expect(desktop).toHaveAttribute('aria-checked', 'mixed'));
    expect(desktop).toBeDisabled();
    expect(screen.getByTestId('session-config-row-autoReview')).toBeDisabled();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Refresh Memory preference' }));
    const beforeMobile = refresh.mock.calls.length;
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Conversation options' }), { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Mobile controls' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-session-options'));
    const mobile = await screen.findByTestId('mobile-action-sheet-option-memory');
    expect(mobile).toBeDisabled();
    expect(screen.getByTestId('mobile-action-sheet-option-autoReview')).toBeDisabled();
    expect(mobile).toHaveTextContent('Memory preference is unconfirmed');
    fireEvent.click(screen.getByTestId('mobile-action-sheet-option-memory-retry'));
    expect(refresh.mock.calls.length).toBeGreaterThan(beforeMobile);
    expect(update).not.toHaveBeenCalled();
  });

  it('disables globally unavailable Memory without rewriting the retained session preference', async () => {
    await renderWithI18n(<MenuHarness availability={{ status: 'ready', enabled: false }} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
    const memory = await screen.findByRole('menuitemcheckbox', { name: 'Memory' });
    expect(memory).not.toBeChecked();
    expect(memory).toBeDisabled();
    expect(memory).toHaveAttribute(
      'title',
      'Memory is off in Settings. Turn it on to use Memory in this conversation.'
    );
    fireEvent.click(memory);
    expect(mocks.persist).not.toHaveBeenCalled();
    expect(screen.getByTestId('preference')).toHaveTextContent('true');
    fireEvent.click(screen.getByTestId('session-config-row-autoReview'));
    await waitFor(() => expect(mocks.persist).toHaveBeenCalledWith('autoReview', true));
  });

  it.each(['loading', 'unavailable'] as const)(
    'keeps %s distinct from an authoritative global-off response',
    async (status) => {
      await renderWithI18n(<MenuHarness availability={{ status }} />, 'en-US');
      fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
      const memory = await screen.findByRole('menuitemcheckbox', { name: 'Memory' });
      expect(memory).toHaveAttribute('aria-checked', 'mixed');
      expect(memory).toBeDisabled();
      expect(memory).not.toHaveAttribute(
        'title',
        'Memory is off in Settings. Turn it on to use Memory in this conversation.'
      );
      fireEvent.click(memory);
      expect(mocks.persist).not.toHaveBeenCalled();
      expect(screen.getByTestId('preference')).toHaveTextContent('true');
    }
  );

  it.each([{}, { enabled: 'false' }, { enabled: null }])(
    'rejects an unknown memory-setting payload %j instead of inventing global-off',
    async (payload) => {
      const fetchImpl = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => Response.json(payload));
      await expect(loadXScienceMemoryEnabled({ fetchImpl })).rejects.toThrow('memory enabled response');
      expect(fetchImpl).toHaveBeenCalledTimes(1);
      expect(fetchImpl.mock.calls[0]?.[0]).toBe('/api/memory/enabled');
    }
  );

  it('preserves the server authentication failure rather than returning global-off', async () => {
    await expect(
      loadXScienceMemoryEnabled({ fetchImpl: async () => Response.json({ error: 'unauthorized' }, { status: 401 }) })
    ).rejects.toThrow('401');
  });

  it('fails closed when a component consumer omits capability wiring', async () => {
    await renderWithI18n(
      <MemoryRouter>
        <XScienceSessionOptionsMenu
          value={{ ...createXScienceSessionDefaults(), memory: true }}
          onChange={mocks.persist}
        />
      </MemoryRouter>,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
    const memory = await screen.findByTestId('session-config-row-memory');
    expect(memory).toBeDisabled();
    expect(memory).toHaveAttribute('aria-checked', 'mixed');
  });

  it('loads the real GET and can retry a network failure without inventing global-off', async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError('offline'))
      .mockResolvedValueOnce(Response.json({ enabled: true }));
    vi.stubGlobal('fetch', fetchImpl);
    const { result } = renderHook(() => useXScienceMemoryAvailability('owner-a:conversation-a'));
    await waitFor(() => expect(result.current.availability).toEqual({ status: 'unavailable' }));
    expect(result.current.canUpdate()).toBe(false);
    act(() => result.current.refresh());
    await waitFor(() => expect(result.current.availability).toEqual({ status: 'ready', enabled: true }));
    expect(result.current.canUpdate()).toBe(true);
    expect(fetchImpl.mock.calls.every(([url, init]) => url === '/api/memory/enabled' && !init?.method)).toBe(true);
  });

  it('invalidates old conversation/account reads and ignores a late result before the next operation', async () => {
    let resolveOld!: (response: Response) => void;
    const first = new Promise<Response>((resolve) => {
      resolveOld = resolve;
    });
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockReturnValueOnce(first)
      .mockImplementation(async () => Response.json({ enabled: false }));
    vi.stubGlobal('fetch', fetchImpl);
    setRendererAccountOwner('account-a');
    const { result, rerender } = renderHook(({ owner }) => useXScienceMemoryAvailability(owner), {
      initialProps: { owner: 'account-a:conversation-a' },
    });
    const previous = result.current;
    // Exercise a genuinely dispatched old response arriving after the owner changes.
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(1));
    act(() => setRendererAccountOwner('account-b'));
    rerender({ owner: 'account-b:conversation-b' });
    await waitFor(() => expect(result.current.availability).toEqual({ status: 'ready', enabled: false }));
    expect(previous.current()).toBe(false);
    expect(fetchImpl.mock.calls[0]?.[1]?.signal?.aborted).toBe(true);
    await act(async () => resolveOld(Response.json({ enabled: true })));
    expect(result.current.availability).toEqual({ status: 'ready', enabled: false });
    fetchImpl.mockResolvedValueOnce(Response.json({ enabled: true }));
    act(() => result.current.refresh());
    await waitFor(() => expect(result.current.canUpdate()).toBe(true));
  });

  it('keeps a timed-out or unmounted request from publishing a late ready capability', async () => {
    vi.useFakeTimers();
    let resolveRequest!: (response: Response) => void;
    const delayed = new Promise<Response>((resolve) => {
      resolveRequest = resolve;
    });
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(delayed));
    const { result, unmount } = renderHook(() => useXScienceMemoryAvailability('timeout-owner'));
    await act(async () => vi.advanceTimersByTimeAsync(8_001));
    expect(result.current.availability).toEqual({ status: 'unavailable' });
    await act(async () => resolveRequest(Response.json({ enabled: true })));
    expect(result.current.availability).toEqual({ status: 'unavailable' });
    const control = result.current;
    unmount();
    expect(control.current()).toBe(false);
    expect(control.canUpdate()).toBe(false);
  });

  it('refreshes a previously unavailable capability on a new menu opening including keyboard opening', async () => {
    await renderWithI18n(<MenuHarness availability={{ status: 'unavailable' }} />, 'en-US');
    const trigger = screen.getByRole('button', { name: 'Conversation options' });
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Conversation options' }), { key: 'Escape' });
    fireEvent.click(trigger);
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(2));
  });

  it('uses one observed capability for the desktop and real mobile sheet without changing global settings or a disabled preference', async () => {
    let globalEnabled = false;
    const writes: Array<{ url: string; body: unknown }> = [];
    const fetchImpl = vi.fn<typeof fetch>(async (input, init) => {
      const url = String(input);
      if (url === '/api/memory/enabled' && !init?.method) return Response.json({ enabled: globalEnabled });
      if (url === '/api/frames/synthetic-frame/session-config' && init?.method === 'POST') {
        writes.push({ url, body: JSON.parse(String(init.body)) });
        return Response.json({ status: 'ok' });
      }
      throw new Error(`Unexpected synthetic request: ${init?.method ?? 'GET'} ${url}`);
    });
    vi.stubGlobal('fetch', fetchImpl);
    await renderWithI18n(<SharedComposerHarness />, 'en-US');
    await waitFor(() => expect(screen.getByTestId('capability')).toHaveTextContent('"enabled":false'));
    fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
    const desktopMemory = await screen.findByTestId('session-config-row-memory');
    await waitFor(() => expect(desktopMemory).toHaveAttribute('aria-checked', 'false'));
    expect(desktopMemory).toBeDisabled();
    fireEvent.click(desktopMemory);
    expect(writes).toEqual([]);
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Conversation options' }), { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Mobile controls' }));
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-session-options'));
    const mobileMemory = await screen.findByTestId('mobile-action-sheet-option-memory');
    await waitFor(() => expect(screen.getByTestId('capability')).toHaveTextContent('"enabled":false'));
    expect(mobileMemory).toBeDisabled();
    expect(mobileMemory).toHaveTextContent('Memory is off in Settings.');
    fireEvent.click(mobileMemory);
    expect(writes).toEqual([]);
    expect(screen.getByTestId('shared-preference')).toHaveTextContent('true');
    globalEnabled = true;
    fireEvent(window, new Event('focus'));
    await waitFor(() => expect(mobileMemory).not.toBeDisabled());
    fireEvent.click(mobileMemory);
    await waitFor(() =>
      expect(writes).toEqual([{ url: '/api/frames/synthetic-frame/session-config', body: { memory_mode: 'off' } }])
    );
    expect(screen.getByTestId('shared-preference')).toHaveTextContent('false');
    expect(
      fetchImpl.mock.calls.some(([url, init]) => String(url) === '/api/memory/enabled' && init?.method === 'PUT')
    ).toBe(false);
  });
});
