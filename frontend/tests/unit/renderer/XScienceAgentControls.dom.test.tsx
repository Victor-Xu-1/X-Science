import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { useSessionPermissionGrantsController } from '@/renderer/hooks/xScience/runtime/useSessionPermissionGrants';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { createXScienceSessionDefaults } from '@/renderer/services/xScienceSessionOptions';
import { renderWithI18n } from '../i18nTestUtils';
import {
  sessionGrantIdentity,
  sessionGrantRevokeBody,
  sessionGrantRevokeResponse,
  sessionGrantsSnapshot,
} from '../xscience/sessionPermissionGrantsFixtures';

const mocks = vi.hoisted(() => ({
  httpRequest: vi.fn(),
  setMode: vi.fn(),
  error: vi.fn(),
  success: vi.fn(),
  setCompute: vi.fn(),
  changeOptions: vi.fn(),
  clearGoal: vi.fn(),
}));
vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: async () => [
    { name: 'ssh:fixture', displayName: 'Fixture host', family: 'ssh', checked: true },
  ],
  loadXScienceSessionComputeProviders: async () => ['ssh:fixture'],
  setXScienceSessionComputeProvider: mocks.setCompute,
}));
vi.mock('@/common/adapter/httpBridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.httpRequest,
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: { listChanged: { on: () => () => {} } },
    realtime: { reconnected: { on: () => () => {} } },
  },
}));
vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return { ...actual, Message: { ...actual.Message, error: mocks.error, success: mocks.success } };
});

const option = {
  id: 'mode',
  category: 'mode',
  currentValue: 'default',
  options: [
    { value: 'default', label: 'Default' },
    { value: 'smart', label: 'Smart' },
    { value: 'bypassPermissions', label: 'Full access' },
  ],
};

function Harness({
  withGrants = false,
  owner = 'a',
  readOnly = false,
  modeAvailable = true,
  modeOption = option,
}: {
  withGrants?: boolean;
  owner?: string;
  readOnly?: boolean;
  modeAvailable?: boolean;
  modeOption?: typeof option;
}) {
  const controller = useSessionPermissionGrantsController(withGrants ? sessionGrantIdentity : null);
  return (
    <MemoryRouter>
      <XScienceSessionOptionsMenu
        rootFrameId={`frame-${owner}`}
        value={{ ...createXScienceSessionDefaults(), goalText: readOnly ? 'Fixture goal' : null }}
        onChange={mocks.changeOptions}
        onClearGoal={mocks.clearGoal}
        disabled={readOnly}
        permissionControl={{
          option: modeAvailable ? modeOption : null,
          setStatus: { state: 'idle' },
          setConfigOption: mocks.setMode,
          grantsController: controller,
          contextKey: owner,
        }}
      />
    </MemoryRouter>
  );
}

async function openPermission() {
  fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
  fireEvent.click(await screen.findByTestId('session-config-row-permission'));
}

describe('unified composer Agent controls', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setRendererAccountOwner(sessionGrantIdentity.ownerId);
    mocks.setMode.mockResolvedValue(undefined);
    mocks.httpRequest.mockImplementation(async (method: string) =>
      method === 'GET' ? sessionGrantsSnapshot() : sessionGrantRevokeResponse()
    );
  });
  afterEach(() => {
    cleanup();
    setRendererAccountOwner(null);
  });

  it('normalizes non-text runtime permission descriptions before presenting menu content', async () => {
    const runtimeOption = {
      ...option,
      options: [{ value: 'fixture-custom', label: 'Fixture permission', description: { unexpected: true } }],
    };
    await renderWithI18n(<Harness modeOption={runtimeOption} />, 'en-US');
    await openPermission();
    const choice = await screen.findByRole('menuitemradio', {
      name: 'Fixture permission: Ask before every tool operation that requires authorization',
    });
    expect(choice).toHaveAttribute('title', 'Ask before every tool operation that requires authorization');
    expect(mocks.setMode).not.toHaveBeenCalled();
  });

  it('exposes permissions and the observed grants through one slider without a second shield trigger', async () => {
    await renderWithI18n(<Harness withGrants />, 'en-US');
    expect(await screen.findByLabelText('1 allowed this session')).toBeInTheDocument();
    expect(screen.queryByTestId('x-science-permission-selector')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('x-science-session-options-trigger'));
    expect(await screen.findByTestId('session-config-row-permission')).toHaveTextContent('Request approval');
    expect(await screen.findByRole('button', { name: 'Clear all session grants' })).toBeEnabled();
    expect(mocks.httpRequest.mock.calls.filter(([method]) => method === 'GET')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Clear all session grants' }));
    await waitFor(() =>
      expect(mocks.httpRequest).toHaveBeenCalledWith(
        'POST',
        '/api/conversations/grant-child/permission-grants/revoke',
        sessionGrantRevokeBody()
      )
    );
    expect(mocks.setMode).not.toHaveBeenCalled();
  });

  it('requires explicit Full access confirmation; cancel does not write and reopening confirms once', async () => {
    await renderWithI18n(<Harness />, 'en-US');
    await openPermission();
    fireEvent.click(await screen.findByTestId('x-science-permission-option-bypassPermissions'));
    expect(await screen.findByRole('dialog', { name: 'Enable Full access?' })).toBeInTheDocument();
    expect(mocks.setMode).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByTestId('x-science-session-options-trigger')).toHaveFocus());
    await openPermission();
    fireEvent.click(await screen.findByTestId('x-science-permission-option-bypassPermissions'));
    fireEvent.click(await screen.findByRole('button', { name: 'Enable' }));
    await waitFor(() => expect(mocks.setMode).toHaveBeenCalledTimes(1));
    expect(mocks.setMode).toHaveBeenCalledWith('mode', 'bypassPermissions');
  });

  it('keeps optional session options read-only without blocking existing permission and grant controls', async () => {
    await renderWithI18n(<Harness withGrants readOnly />, 'en-US');
    const trigger = screen.getByTestId('x-science-session-options-trigger');
    expect(trigger).toBeEnabled();
    fireEvent.click(trigger);
    const autoReview = await screen.findByRole('menuitemcheckbox', { name: 'Auto review' });
    expect(autoReview).toBeDisabled();
    fireEvent.click(autoReview);
    expect(screen.getByRole('menuitem', { name: 'Clear goal' })).toBeDisabled();
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-specialist'));
    expect(screen.getByTestId('session-config-row-specialist')).toHaveTextContent('Not confirmed');
    const specialist = await screen.findByRole('menuitemradio', { name: 'Select expert None' });
    expect(specialist).toBeDisabled();
    expect(screen.queryByRole('menuitemradio', { name: 'Select expert Fixture expert' })).not.toBeInTheDocument();
    fireEvent.click(specialist);
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-compute'));
    const local = await screen.findByRole('menuitemradio', { name: 'Local' });
    expect(local).toBeDisabled();
    const provider = screen.getByRole('menuitemcheckbox', { name: 'Compute Fixture host' });
    expect(provider).toBeDisabled();
    fireEvent.click(provider);
    expect(mocks.changeOptions).not.toHaveBeenCalled();
    expect(mocks.clearGoal).not.toHaveBeenCalled();
    expect(mocks.setCompute).not.toHaveBeenCalled();
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-permission'));
    fireEvent.click(await screen.findByTestId('x-science-permission-option-smart'));
    await waitFor(() => expect(mocks.setMode).toHaveBeenCalledWith('mode', 'smart'));
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole('button', { name: 'Clear all session grants' }));
    await waitFor(() =>
      expect(mocks.httpRequest).toHaveBeenCalledWith(
        'POST',
        '/api/conversations/grant-child/permission-grants/revoke',
        sessionGrantRevokeBody()
      )
    );
  });

  it('discards a confirmation after its conversation owner changes', async () => {
    const view = await renderWithI18n(<Harness owner='a' />, 'en-US');
    await openPermission();
    fireEvent.click(await screen.findByTestId('x-science-permission-option-bypassPermissions'));
    await screen.findByRole('dialog');
    view.rerender(<Harness owner='b' />);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(mocks.setMode).not.toHaveBeenCalled();
  });

  it('ignores late mode errors after owner change and remains usable for the next request', async () => {
    let reject!: (reason: Error) => void;
    const pending = new Promise<void>((_resolve, fail) => {
      reject = fail;
    });
    mocks.setMode.mockReturnValueOnce(pending);
    const view = await renderWithI18n(<Harness owner='a' />, 'en-US');
    await openPermission();
    fireEvent.click(await screen.findByTestId('x-science-permission-option-smart'));
    await waitFor(() => expect(mocks.setMode).toHaveBeenCalledTimes(1));
    view.rerender(<Harness owner='b' />);
    await act(async () => {
      reject(new Error('old mode response'));
      await pending.catch(() => {});
    });
    expect(mocks.error).not.toHaveBeenCalled();
    await openPermission();
    fireEvent.click(await screen.findByTestId('x-science-permission-option-smart'));
    await waitFor(() => expect(mocks.setMode).toHaveBeenCalledTimes(2));
  });

  it('navigates the unified permission submenu by keyboard and restores the slider for the next opening', async () => {
    await renderWithI18n(<Harness />, 'en-US');
    const trigger = screen.getByTestId('x-science-session-options-trigger');
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    const permission = await screen.findByTestId('session-config-row-permission');
    await waitFor(() => expect(permission).toHaveFocus());
    expect(permission).toHaveClass('text-13px', 'h-32px');
    fireEvent.keyDown(permission, { key: 'ArrowRight' });
    const choices = await screen.findByRole('menu', { name: 'Permission Mode' });
    await waitFor(() => expect(screen.getByTestId('x-science-permission-option-default')).toHaveFocus());
    fireEvent.keyDown(choices, { key: 'Escape' });
    expect(permission).toHaveFocus();
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Conversation options' }), { key: 'Escape' });
    await waitFor(() => expect(trigger).toHaveFocus());
    fireEvent.click(trigger);
    expect(await screen.findByTestId('session-config-row-permission')).toBeInTheDocument();
    expect(mocks.setMode).not.toHaveBeenCalled();
  });

  it('does not offer an empty permission submenu when only existing grants are available', async () => {
    await renderWithI18n(<Harness withGrants readOnly modeAvailable={false} />, 'en-US');
    expect(await screen.findByLabelText('1 allowed this session')).toBeInTheDocument();
    const trigger = screen.getByTestId('x-science-session-options-trigger');
    expect(trigger).toBeEnabled();
    fireEvent.click(trigger);
    await screen.findByRole('menu', { name: 'Conversation options' });
    expect(screen.queryByTestId('session-config-row-permission')).not.toBeInTheDocument();
    expect(screen.queryByRole('menu', { name: 'Permission Mode' })).not.toBeInTheDocument();
    expect(screen.getByRole('menuitemcheckbox', { name: 'Auto review' })).toBeDisabled();
    fireEvent.click(await screen.findByRole('button', { name: 'Clear all session grants' }));
    await waitFor(() =>
      expect(mocks.httpRequest).toHaveBeenCalledWith(
        'POST',
        '/api/conversations/grant-child/permission-grants/revoke',
        sessionGrantRevokeBody()
      )
    );
    expect(mocks.setMode).not.toHaveBeenCalled();
    expect(mocks.changeOptions).not.toHaveBeenCalled();
  });

  it('removes an invalid permission submenu and confirmation without losing grants or resurrecting a stale confirmation', async () => {
    const view = await renderWithI18n(<Harness withGrants />, 'en-US');
    const trigger = screen.getByTestId('x-science-session-options-trigger');
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    const permission = await screen.findByTestId('session-config-row-permission');
    fireEvent.keyDown(permission, { key: 'ArrowRight' });
    await waitFor(() => expect(screen.getByTestId('x-science-permission-option-default')).toHaveFocus());
    view.rerender(<Harness withGrants modeAvailable={false} />);
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Permission Mode' })).not.toBeInTheDocument());
    expect(screen.queryByTestId('session-config-row-permission')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox', { name: 'Auto review' })).toHaveFocus());
    expect(screen.getByRole('button', { name: 'Clear all session grants' })).toBeEnabled();
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Conversation options' }), { key: 'Escape' });
    await waitFor(() => expect(trigger).toHaveFocus());

    view.rerender(<Harness withGrants />);
    await openPermission();
    fireEvent.click(await screen.findByTestId('x-science-permission-option-bypassPermissions'));
    await screen.findByRole('dialog', { name: 'Enable Full access?' });
    view.rerender(<Harness withGrants modeAvailable={false} />);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    view.rerender(<Harness withGrants />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(mocks.setMode).not.toHaveBeenCalled();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole('button', { name: 'Clear all session grants' }));
    await waitFor(() =>
      expect(mocks.httpRequest).toHaveBeenCalledWith(
        'POST',
        '/api/conversations/grant-child/permission-grants/revoke',
        sessionGrantRevokeBody()
      )
    );
    expect(mocks.setMode).not.toHaveBeenCalled();
  });
});
