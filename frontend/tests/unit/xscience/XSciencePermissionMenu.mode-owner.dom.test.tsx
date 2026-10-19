import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { AcpConfigOptionDto, SetConfigOptionResponse } from '@/common/types/platform/acpTypes';
import XSciencePermissionMenu from '@/renderer/components/xScience/runtime/XSciencePermissionMenu';
import { useAcpConfigOptions } from '@/renderer/hooks/xScience/runtime/useAcpConfigOptions';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';
import { sessionGrantIdentity, sessionGrantsSnapshot } from './sessionPermissionGrantsFixtures';

const { ensureRuntime, setConfigOption, grantRead, success } = vi.hoisted(() => ({
  ensureRuntime: vi.fn(),
  setConfigOption: vi.fn(),
  grantRead: vi.fn(),
  success: vi.fn(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      ensureRuntime: { invoke: ensureRuntime },
      listChanged: { on: () => () => {} },
    },
    acpConversation: {
      setConfigOption: { invoke: setConfigOption },
      responseStream: { on: () => () => {} },
    },
    realtime: { reconnected: { on: () => () => {} } },
  },
}));
vi.mock('@/common/adapter/httpBridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: grantRead,
}));
vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return { ...actual, Message: { ...actual.Message, success } };
});

const identityA = { ...sessionGrantIdentity, frameId: 'mode-owner-a', frameIncarnationId: 'mode-inc-a' };
const identityB = { ...sessionGrantIdentity, frameId: 'mode-owner-b', frameIncarnationId: 'mode-inc-b' };
function modeOptions(current: string): AcpConfigOptionDto[] {
  return [
    {
      id: 'mode',
      category: 'mode',
      option_type: 'select',
      current_value: current,
      options: [
        { value: 'default', name: 'Default' },
        { value: 'bypassPermissions', name: 'Full access' },
      ],
    },
  ];
}

/** Two real hook/SWR owners isolate the consumer race from mutations of an unrelated owner. */
function LiveOwners({ active }: { active: 'a' | 'b' }) {
  const first = useAcpConfigOptions({ conversation_id: identityA.frameId });
  const second = useAcpConfigOptions({ conversation_id: identityB.frameId });
  const runtime = active === 'a' ? first : second;
  return (
    <>
      <output data-testid='live-mode-owner'>{runtime.mode?.currentValue}</output>
      <XSciencePermissionMenu
        sessionIdentity={active === 'a' ? identityA : identityB}
        option={runtime.mode}
        setStatus={runtime.setStatus}
        setConfigOption={runtime.setConfigOption}
      />
    </>
  );
}

afterEach(() => setRendererAccountOwner(null));
beforeEach(() => {
  vi.clearAllMocks();
  setRendererAccountOwner(sessionGrantIdentity.ownerId);
  ensureRuntime.mockResolvedValue({ config_options: modeOptions('default') });
  grantRead.mockImplementation(async (_method: string, path: string) => {
    const identity = path.includes('mode-owner-a') ? identityA : identityB;
    const snapshot = sessionGrantsSnapshot([]);
    snapshot.scope.frame_id = identity.frameId;
    snapshot.scope.frame_incarnation_id = identity.frameIncarnationId;
    return snapshot;
  });
});

it('C6 desktop ignores a late observed ACK from the previous live mode owner after scope switching', async () => {
  let acknowledge!: (response: SetConfigOptionResponse) => void;
  const pending = new Promise<SetConfigOptionResponse>((resolve) => {
    acknowledge = resolve;
  });
  setConfigOption.mockReturnValue(pending);
  const view = await renderWithI18n(<LiveOwners active='a' />, 'en-US', {
    wrapper: ({ children }) => (
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>{children}</SWRConfig>
    ),
  });
  await waitFor(() => expect(screen.getByTestId('live-mode-owner')).toHaveTextContent('default'));
  fireEvent.click(screen.getByTestId('x-science-permission-selector'));
  fireEvent.click(await screen.findByTestId('x-science-permission-option-bypassPermissions'));
  fireEvent.click(await screen.findByRole('button', { name: 'Enable' }));
  await waitFor(() =>
    expect(setConfigOption).toHaveBeenCalledWith({
      conversation_id: identityA.frameId,
      option_id: 'mode',
      value: 'bypassPermissions',
    })
  );
  view.rerender(<LiveOwners active='b' />);
  await waitFor(() => expect(screen.getByTestId('live-mode-owner')).toHaveTextContent('default'));
  await act(async () => {
    acknowledge({ confirmation: 'observed', config_options: modeOptions('bypassPermissions') });
    await pending;
  });
  expect(screen.getByTestId('live-mode-owner')).toHaveTextContent('default');
  expect(screen.getByTestId('x-science-permission-selector')).toHaveAttribute('data-current-permission', 'default');
  expect(success).not.toHaveBeenCalled();
  expect(setConfigOption).toHaveBeenCalledTimes(1);
});

it('C6 desktop does not report a late live mode acknowledgement after the consumer unmounts', async () => {
  let acknowledge!: (response: SetConfigOptionResponse) => void;
  const pending = new Promise<SetConfigOptionResponse>((resolve) => {
    acknowledge = resolve;
  });
  setConfigOption.mockReturnValue(pending);
  const view = await renderWithI18n(<LiveOwners active='a' />, 'en-US', {
    wrapper: ({ children }) => (
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>{children}</SWRConfig>
    ),
  });
  await waitFor(() => expect(screen.getByTestId('live-mode-owner')).toHaveTextContent('default'));
  fireEvent.click(screen.getByTestId('x-science-permission-selector'));
  fireEvent.click(await screen.findByTestId('x-science-permission-option-bypassPermissions'));
  fireEvent.click(await screen.findByRole('button', { name: 'Enable' }));
  await waitFor(() => expect(setConfigOption).toHaveBeenCalledTimes(1));
  view.unmount();
  await act(async () => {
    acknowledge({ confirmation: 'observed', config_options: modeOptions('bypassPermissions') });
    await pending;
  });
  expect(screen.queryByTestId('x-science-permission-selector')).not.toBeInTheDocument();
  expect(success).not.toHaveBeenCalled();
  expect(setConfigOption).toHaveBeenCalledTimes(1);
});
