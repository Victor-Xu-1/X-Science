import { act, cleanup, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import React, { useRef, useState } from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { loadXScienceMemoryEnabled } from '@/renderer/services/xScienceMemory';
import { createXScienceSessionDefaults } from '@/renderer/services/xScienceSessionOptions';
import {
  useXScienceMemoryAvailability,
  type XScienceMemoryAvailability,
} from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { useAcpSessionOptionControls } from '@/renderer/pages/conversation/platforms/acp/useAcpSessionOptionControls';
import { useAcpMobileActionSheetController } from '@/renderer/pages/conversation/platforms/acp/useAcpMobileActionSheetController';
import MobileActionSheet from '@/renderer/components/chat/MobileActionSheet';
import { useTranslation } from 'react-i18next';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({ persist: vi.fn(), refresh: vi.fn() }));
vi.mock('@/renderer/services/xScienceCatalog', () => ({ loadXScienceAssistants: async () => [] }));
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

function SharedComposerHarness() {
  const { t, i18n } = useTranslation();
  const [value, setValue] = useState({ ...createXScienceSessionDefaults(), memory: true });
  const [mobileOpen, setMobileOpen] = useState(false);
  const mounted = useRef(true);
  const owner = useRef({ conversationId: 'synthetic-frame', ownerId: 'synthetic-owner' });
  const { memoryControl, updatePersistentSessionOption, openMobileControls } = useAcpSessionOptionControls(
    'synthetic-frame',
    'synthetic-owner:synthetic-frame',
    owner,
    mounted,
    setValue,
    setMobileOpen
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
    sessionOptionsReady: true,
    setXScienceSessionOptions: setValue,
    updatePersistentSessionOption,
    memoryControl,
    mobileSpecialists: [],
    mobileComputeProviders: [],
    mobileEnabledCompute: [],
    handleSessionComputeChange: async () => undefined,
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
  beforeEach(() => {
    mocks.persist.mockResolvedValue(undefined);
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    resetRendererAccountScopeForTest();
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
