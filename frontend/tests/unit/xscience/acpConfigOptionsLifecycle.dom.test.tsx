import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SWRConfig } from 'swr';
import type { AcpConfigOptionDto, SetConfigOptionResponse } from '@/common/types/platform/acpTypes';
import { useAcpConfigOptions } from '@/renderer/hooks/xScience/runtime/useAcpConfigOptions';
import { useAcpModelInfo } from '@/renderer/hooks/xScience/runtime/useAcpModelInfo';
import { getRendererAccountOwnerId, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const { ensureRuntime, setConfigOption } = vi.hoisted(() => ({
  ensureRuntime: vi.fn(),
  setConfigOption: vi.fn(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: { ensureRuntime: { invoke: ensureRuntime } },
    acpConversation: { setConfigOption: { invoke: setConfigOption }, responseStream: { on: () => () => {} } },
  },
}));

function options(current = 'default', label = 'Default'): AcpConfigOptionDto[] {
  return [
    {
      id: 'mode',
      category: 'mode',
      option_type: 'select',
      current_value: current,
      options: [
        { value: 'default', name: label },
        { value: 'bypassPermissions', name: 'Full access' },
      ],
    },
  ];
}
function deferredResponse() {
  let acknowledge!: (value: SetConfigOptionResponse) => void;
  let fail!: (reason: unknown) => void;
  const promise = new Promise<SetConfigOptionResponse>((resolve, reject) => {
    acknowledge = resolve;
    fail = reject;
  });
  setConfigOption.mockReturnValue(promise);
  return { promise, acknowledge, fail };
}
function Wrapper({ children }: { children: React.ReactNode }) {
  return <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>{children}</SWRConfig>;
}
beforeEach(() => {
  vi.clearAllMocks();
  setRendererAccountOwner('config-owner-a');
  ensureRuntime.mockResolvedValue({ config_options: options() });
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
});

it('C6 config lifecycle consumes a late observed response after unmount without touching a destroyed SWR owner', async () => {
  const response = deferredResponse();
  const view = renderHook(() => useAcpConfigOptions({ conversation_id: 'config-lifecycle' }), { wrapper: Wrapper });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  view.unmount();
  const observed = options('bypassPermissions');
  await act(async () => {
    response.acknowledge({ confirmation: 'observed', config_options: observed });
    await expect(operation).resolves.toEqual(observed);
  });
  expect(setConfigOption).toHaveBeenCalledTimes(1);
});

it('C6 config lifecycle fences one hook when the conversation changes before an old observed response', async () => {
  const response = deferredResponse();
  const view = renderHook(({ id }) => useAcpConfigOptions({ conversation_id: id }), {
    initialProps: { id: 'config-a' },
    wrapper: Wrapper,
  });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  view.rerender({ id: 'config-b' });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  await act(async () => {
    response.acknowledge({ confirmation: 'observed', config_options: options('bypassPermissions') });
    await operation;
  });
  expect(view.result.current.mode?.currentValue).toBe('default');
});

it('C6 config lifecycle isolates account cache and pending status even with the same conversation and SWR provider', async () => {
  const response = deferredResponse();
  ensureRuntime.mockImplementation(async () => ({ config_options: options('default', getRendererAccountOwnerId()) }));
  const view = renderHook(
    ({ revision }) => {
      void revision;
      return useAcpConfigOptions({ conversation_id: 'same-id' });
    },
    { initialProps: { revision: 0 }, wrapper: Wrapper }
  );
  await waitFor(() => expect(view.result.current.mode?.options[0].label).toBe('config-owner-a'));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  act(() => {
    setRendererAccountOwner('config-owner-b');
  });
  view.rerender({ revision: 1 });
  await waitFor(() => expect(view.result.current.mode?.options[0].label).toBe('config-owner-b'));
  expect(view.result.current.setStatus.state).toBe('idle');
  await act(async () => {
    response.acknowledge({ confirmation: 'observed', config_options: options('bypassPermissions', 'config-owner-a') });
    await operation;
  });
  expect(view.result.current.mode?.currentValue).toBe('default');
  expect(view.result.current.mode?.options[0].label).toBe('config-owner-b');
  expect(view.result.current.setStatus.state).toBe('idle');
});

it('C6 config lifecycle fences an old incarnation before the same frame receives its late response', async () => {
  const response = deferredResponse();
  const view = renderHook(
    ({ incarnation }) => useAcpConfigOptions({ conversation_id: 'same-frame', frameIncarnationId: incarnation }),
    {
      initialProps: { incarnation: 'incarnation-a' },
      wrapper: Wrapper,
    }
  );
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  view.rerender({ incarnation: 'incarnation-b' });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  await act(async () => {
    response.acknowledge({ confirmation: 'observed', config_options: options('bypassPermissions') });
    await operation;
  });
  expect(view.result.current.mode?.currentValue).toBe('default');
});

it('C6 config lifecycle still rejects a real runtime write failure and releases its current pending status', async () => {
  const failure = new Error('actual runtime rejected the operation');
  setConfigOption.mockRejectedValue(failure);
  const view = renderHook(() => useAcpConfigOptions({ conversation_id: 'config-failure' }), { wrapper: Wrapper });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  await act(async () => {
    await expect(view.result.current.setConfigOption('mode', 'bypassPermissions')).rejects.toBe(failure);
  });
  await waitFor(() => expect(view.result.current.setStatus.state).toBe('idle'));
  expect(view.result.current.mode?.currentValue).toBe('default');
  expect(setConfigOption).toHaveBeenCalledTimes(1);
});

it('C6 config lifecycle does not start an old runtime read after preparation outlives its owner', async () => {
  let finishPreparation!: () => void;
  const preparation = new Promise<void>((resolve) => {
    finishPreparation = resolve;
  });
  const view = renderHook(
    () =>
      useAcpConfigOptions({
        conversation_id: 'preparation-owner',
        prepareRuntime: () => preparation,
      }),
    { wrapper: Wrapper }
  );
  expect(ensureRuntime).not.toHaveBeenCalled();
  view.unmount();
  await act(async () => {
    finishPreparation();
    await preparation;
  });
  expect(ensureRuntime).not.toHaveBeenCalled();
});

it('C6 config lifecycle rereads authority after A to B to A while the original A write settles', async () => {
  const response = deferredResponse();
  const view = renderHook(({ id }) => useAcpConfigOptions({ conversation_id: id }), {
    initialProps: { id: 'aba-a' },
    wrapper: Wrapper,
  });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  view.rerender({ id: 'aba-b' });
  await waitFor(() => expect(view.result.current.mode?.currentValue).toBe('default'));
  view.rerender({ id: 'aba-a' });
  await waitFor(() => expect(view.result.current.setStatus.state).toBe('setting'));
  ensureRuntime.mockResolvedValue({ config_options: options('bypassPermissions', 'fresh authority') });
  await act(async () => {
    response.acknowledge({
      confirmation: 'observed',
      config_options: options('bypassPermissions', 'old write response'),
    });
    await operation;
  });
  await waitFor(() => expect(view.result.current.mode?.options[0].label).toBe('fresh authority'));
  expect(view.result.current.mode?.currentValue).toBe('bypassPermissions');
  expect(setConfigOption).toHaveBeenCalledTimes(1);
});

it('C6 config lifecycle gives model and mode consumers one incarnation snapshot and writer lock', async () => {
  const response = deferredResponse();
  const view = renderHook(
    () => {
      const scope = { conversation_id: 'shared-runtime', frameIncarnationId: 'shared-incarnation' };
      return { config: useAcpConfigOptions(scope), model: useAcpModelInfo(scope) };
    },
    { wrapper: Wrapper }
  );
  await waitFor(() => expect(view.result.current.config.mode?.currentValue).toBe('default'));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.config.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(view.result.current.model.setStatus.state).toBe('setting'));
  await expect(view.result.current.model.setConfigOption('mode', 'default')).rejects.toThrow(
    'config_update_in_progress'
  );
  expect(setConfigOption).toHaveBeenCalledTimes(1);
  await act(async () => {
    response.acknowledge({ confirmation: 'observed', config_options: options('bypassPermissions') });
    await operation;
  });
  await waitFor(() => expect(view.result.current.model.setStatus.state).toBe('idle'));
});

it('C6 config lifecycle never reuses an in-flight read from an earlier incarnation of the same frame', async () => {
  let finishOldRead!: (value: { config_options: AcpConfigOptionDto[] }) => void;
  const oldRead = new Promise<{ config_options: AcpConfigOptionDto[] }>((resolve) => {
    finishOldRead = resolve;
  });
  ensureRuntime
    .mockReturnValueOnce(oldRead)
    .mockResolvedValue({ config_options: options('default', 'new incarnation') });
  const view = renderHook(
    ({ incarnation }) =>
      useAcpConfigOptions({
        conversation_id: 'recreated-frame',
        frameIncarnationId: incarnation,
      }),
    { initialProps: { incarnation: 'old-incarnation' }, wrapper: Wrapper }
  );
  await waitFor(() => expect(ensureRuntime).toHaveBeenCalledTimes(1));
  view.rerender({ incarnation: 'new-incarnation' });
  await waitFor(() => expect(view.result.current.mode?.options[0].label).toBe('new incarnation'));
  await act(async () => {
    finishOldRead({ config_options: options('bypassPermissions', 'old incarnation') });
    await oldRead;
  });
  expect(view.result.current.mode?.options[0].label).toBe('new incarnation');
  expect(ensureRuntime).toHaveBeenCalledTimes(2);
});

it('C6 config lifecycle rejects a stale passive read from another consumer after the shared write is observed', async () => {
  const response = deferredResponse();
  const view = renderHook(
    () => {
      const scope = { conversation_id: 'shared-read-write', frameIncarnationId: 'same-incarnation' };
      return { config: useAcpConfigOptions(scope), observer: useAcpConfigOptions(scope) };
    },
    { wrapper: Wrapper }
  );
  await waitFor(() => expect(view.result.current.config.mode?.currentValue).toBe('default'));
  let finishOldRead!: (value: { config_options: AcpConfigOptionDto[] }) => void;
  const staleRead = new Promise<{ config_options: AcpConfigOptionDto[] }>((resolve) => {
    finishOldRead = resolve;
  });
  ensureRuntime.mockReturnValueOnce(staleRead);
  let passiveRead!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    passiveRead = view.result.current.observer.reload();
  });
  await waitFor(() => expect(ensureRuntime).toHaveBeenCalledTimes(2));
  let operation!: Promise<AcpConfigOptionDto[]>;
  act(() => {
    operation = view.result.current.config.setConfigOption('mode', 'bypassPermissions');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  await act(async () => {
    response.acknowledge({ confirmation: 'observed', config_options: options('bypassPermissions') });
    await operation;
  });
  await waitFor(() => expect(view.result.current.observer.mode?.currentValue).toBe('bypassPermissions'));
  ensureRuntime.mockResolvedValue({ config_options: options('bypassPermissions', 'current authority') });
  await act(async () => {
    await view.result.current.config.reload();
  });
  expect(ensureRuntime).toHaveBeenCalledTimes(3);
  expect(view.result.current.config.mode?.options[0].label).toBe('current authority');
  await act(async () => {
    finishOldRead({ config_options: options('default', 'old passive read') });
    await passiveRead;
  });
  expect(view.result.current.config.mode?.currentValue).toBe('bypassPermissions');
  expect(view.result.current.observer.mode?.currentValue).toBe('bypassPermissions');
});

it.each(['unmount', 'conversation', 'account', 'incarnation'])(
  'C6 model callback never reports an old observed response after %s',
  async (retire) => {
    const response = deferredResponse();
    const onSuccess = vi.fn();
    const onFailure = vi.fn();
    const runtimeOptions = (): AcpConfigOptionDto[] => [
      ...options(),
      {
        id: 'model',
        category: 'model',
        option_type: 'select',
        current_value: 'first',
        options: [
          { value: 'first', label: 'First' },
          { value: 'second', label: 'Second' },
        ],
      },
    ];
    ensureRuntime.mockResolvedValue({ config_options: runtimeOptions() });
    const view = renderHook(
      ({ id, incarnation }) =>
        useAcpModelInfo({
          conversation_id: id,
          frameIncarnationId: incarnation,
          onSelectModelSuccess: onSuccess,
          onSelectModelFailed: onFailure,
        }),
      { initialProps: { id: 'model-callback', incarnation: 'original-incarnation' }, wrapper: Wrapper }
    );
    await waitFor(() => expect(view.result.current.model_info?.current_model_id).toBe('first'));
    act(() => {
      view.result.current.selectModel('second');
    });
    await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
    if (retire === 'unmount') view.unmount();
    else if (retire === 'account') {
      act(() => {
        setRendererAccountOwner('new-model-owner');
      });
      view.rerender({ id: 'model-callback', incarnation: 'original-incarnation' });
    } else
      view.rerender({
        id: retire === 'conversation' ? 'new-conversation' : 'model-callback',
        incarnation: retire === 'incarnation' ? 'new-incarnation' : 'original-incarnation',
      });
    await act(async () => {
      const observed = runtimeOptions();
      observed[1].current_value = 'second';
      response.acknowledge({ confirmation: 'observed', config_options: observed });
      await response.promise;
    });
    expect(onSuccess).not.toHaveBeenCalled();
    expect(onFailure).not.toHaveBeenCalled();
    expect(setConfigOption).toHaveBeenCalledTimes(1);
  }
);

it('C6 model callback never reports an old runtime rejection after its owner unmounts', async () => {
  const response = deferredResponse();
  const onFailure = vi.fn();
  ensureRuntime.mockResolvedValue({
    config_options: [
      {
        id: 'model',
        category: 'model',
        option_type: 'select',
        current_value: 'first',
        options: [
          { value: 'first', label: 'First' },
          { value: 'second', label: 'Second' },
        ],
      },
    ],
  });
  const view = renderHook(
    () => useAcpModelInfo({ conversation_id: 'old-model-failure', onSelectModelFailed: onFailure }),
    { wrapper: Wrapper }
  );
  await waitFor(() => expect(view.result.current.canSwitch).toBe(true));
  act(() => {
    view.result.current.selectModel('second');
  });
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  view.unmount();
  await act(async () => {
    response.fail(new Error('runtime rejected old model'));
    await Promise.resolve();
  });
  expect(onFailure).not.toHaveBeenCalled();
});
