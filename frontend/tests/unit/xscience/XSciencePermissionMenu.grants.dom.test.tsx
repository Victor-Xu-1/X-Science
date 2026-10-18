import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XSciencePermissionMenu from '@/renderer/components/xScience/runtime/XSciencePermissionMenu';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';
import {
  sessionGrantA,
  sessionGrantB,
  sessionGrantC,
  sessionGrantIdentity,
  sessionGrantInvalidation,
  sessionGrantRevokeBody,
  sessionGrantRevokeResponse,
  sessionGrantsSnapshot,
} from './sessionPermissionGrantsFixtures';

const { httpRequest, subscribeInvalidation } = vi.hoisted(() => ({
  httpRequest: vi.fn(),
  subscribeInvalidation: vi.fn((_source: string, _callback: (event?: unknown) => void) => vi.fn()),
}));
vi.mock('@/common/adapter/httpBridge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest,
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      listChanged: {
        on: (listener: (event: unknown) => void) =>
          subscribeInvalidation('listChanged', (event) => listener(event ?? sessionGrantInvalidation)),
      },
    },
    realtime: {
      reconnected: {
        on: (listener: () => void) => subscribeInvalidation('reconnected', () => listener()),
      },
    },
  },
}));

const mode = {
  id: 'mode',
  category: 'mode',
  currentValue: 'default',
  options: [{ value: 'default', label: 'Default', description: null }],
};
const listPath = '/api/conversations/grant-child/permission-grants';
const sessionProps = { sessionIdentity: sessionGrantIdentity };

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('XSciencePermissionMenu session grants', () => {
  beforeEach(() => {
    setRendererAccountOwner(null);
    setRendererAccountOwner(sessionGrantIdentity.ownerId);
    httpRequest.mockReset();
    subscribeInvalidation.mockClear();
  });
  afterEach(() => setRendererAccountOwner(null));

  it('shows the authoritative badge before the menu has ever opened and observes closed canonical updates', async () => {
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot());
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    expect(await screen.findByLabelText('1 allowed this session')).toHaveTextContent('1');
    expect(screen.queryByRole('menu', { name: 'Permission Mode' })).not.toBeInTheDocument();
    expect(httpRequest).toHaveBeenCalledTimes(1);
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantA, sessionGrantB]));
    await act(async () => subscribeInvalidation.mock.calls[0][1]());
    expect(await screen.findByLabelText('2 allowed this session')).toHaveTextContent('2');
    expect(httpRequest.mock.calls[1][1]).toContain('expected_frame_incarnation_id=grant-child-incarnation');
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    expect(await screen.findByText(sessionGrantB.label)).toBeInTheDocument();
    expect(httpRequest).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Permission Mode' })).not.toBeInTheDocument());
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot([sessionGrantC]));
    await act(async () => subscribeInvalidation.mock.calls[0][1]());
    expect(await screen.findByLabelText('1 allowed this session')).toHaveTextContent('1');
    expect(screen.queryByLabelText('2 allowed this session')).not.toBeInTheDocument();
    expect(subscribeInvalidation).toHaveBeenCalledTimes(2);
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
  });
  it('drops a closed-menu late badge read and destroys its observer on account change', async () => {
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot());
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    expect(await screen.findByLabelText('1 allowed this session')).toBeInTheDocument();
    const late = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(late.promise);
    await act(async () => subscribeInvalidation.mock.calls[0][1]());
    await act(async () => setRendererAccountOwner('new-owner'));
    await act(async () => late.resolve(sessionGrantsSnapshot([sessionGrantA, sessionGrantB])));
    expect(screen.queryByLabelText('1 allowed this session')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('2 allowed this session')).not.toBeInTheDocument();
    expect(subscribeInvalidation.mock.results.every((result) => result.value.mock.calls.length === 1)).toBe(true);
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
  });
  it('keeps grant revocation independently usable while mode is setting and leaves the menu open', async () => {
    httpRequest.mockImplementation(async (method: string) =>
      method === 'GET' ? sessionGrantsSnapshot() : sessionGrantRevokeResponse()
    );
    const setConfigOption = vi.fn();
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'setting', optionId: 'mode', requestedValue: 'default' }}
        setConfigOption={setConfigOption}
      />,
      'en-US'
    );
    expect(screen.getByTestId('x-science-permission-selector')).toBeEnabled();
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    const revoke = await screen.findByRole('button', { name: 'Revoke session grant for Python execution' });
    expect(screen.getByRole('menuitemradio')).toBeDisabled();
    fireEvent.click(revoke);
    await waitFor(() =>
      expect(httpRequest).toHaveBeenCalledWith('POST', `${listPath}/revoke`, sessionGrantRevokeBody())
    );
    expect(screen.getByRole('menu', { name: 'Permission Mode' })).toBeInTheDocument();
    expect(setConfigOption).not.toHaveBeenCalled();
    expect(httpRequest.mock.calls.every(([, path]) => String(path).startsWith(listPath))).toBe(true);
  });

  it('clears only the rendered generation set and retains a later grant from authoritative readback', async () => {
    const mutation = deferred<ReturnType<typeof sessionGrantRevokeResponse>>();
    httpRequest.mockImplementation((method: string) =>
      method === 'GET' ? Promise.resolve(sessionGrantsSnapshot([sessionGrantA, sessionGrantB])) : mutation.promise
    );
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    fireEvent.click(await screen.findByRole('button', { name: 'Clear all session grants' }));
    await waitFor(() =>
      expect(httpRequest).toHaveBeenCalledWith(
        'POST',
        `${listPath}/revoke`,
        sessionGrantRevokeBody([sessionGrantA, sessionGrantB])
      )
    );
    await act(async () =>
      mutation.resolve(sessionGrantRevokeResponse([sessionGrantA, sessionGrantB], [sessionGrantC]))
    );
    expect(await screen.findByText(sessionGrantC.label)).toBeInTheDocument();
    expect(screen.queryByText(sessionGrantA.label)).not.toBeInTheDocument();
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(1);
    expect(screen.getByRole('menu', { name: 'Permission Mode' })).toBeInTheDocument();
  });

  it('does not offer a full clear when the current scoped list is incomplete', async () => {
    httpRequest.mockResolvedValue({
      ...sessionGrantsSnapshot(),
      legacy_unbound_count: 2,
      stale_grant_count: 1,
      incomplete: true,
      incomplete_reason: 'limit',
    });
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    expect(await screen.findByRole('button', { name: 'Clear all session grants' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Revoke session grant for Python execution' })).toBeEnabled();
    expect(screen.getByText(/Some session grants could not be listed/)).toBeInTheDocument();
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
  });
  it('hides a successfully emptied grant section but keeps a later real read failure visible and recoverable', async () => {
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot());
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    const clear = await screen.findByRole('button', { name: 'Clear all session grants' });
    const section = screen.getByTestId('session-permission-grants');
    expect(document.getElementById(section.getAttribute('aria-describedby')!)).toHaveClass('sr-only');
    httpRequest.mockResolvedValueOnce(sessionGrantRevokeResponse());
    fireEvent.click(clear);
    await waitFor(() => expect(screen.queryByTestId('session-permission-grants')).not.toBeInTheDocument());
    expect(screen.getByRole('menu', { name: 'Permission Mode' })).toBeInTheDocument();
    expect(httpRequest).toHaveBeenCalledWith('POST', `${listPath}/revoke`, sessionGrantRevokeBody());
    httpRequest.mockRejectedValueOnce(new Error('storage unavailable'));
    await act(async () => subscribeInvalidation.mock.calls[0][1]());
    expect(await screen.findByRole('alert')).toHaveTextContent('previous list is retained');
    expect(screen.getByTestId('session-permission-grants')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Refresh grants' })).toBeEnabled();
    expect(screen.queryByText('No active session grants.')).not.toBeInTheDocument();
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(1);
  });
  it('retains a failed read and retries only the original mutation after uncertain acknowledgement', async () => {
    httpRequest.mockResolvedValueOnce(sessionGrantsSnapshot());
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    const revoke = await screen.findByRole('button', { name: 'Revoke session grant for Python execution' });
    httpRequest.mockRejectedValueOnce(new Error('storage unavailable'));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh grants' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('previous list is retained');
    expect(screen.getByText(sessionGrantA.label)).toBeInTheDocument();
    httpRequest.mockRejectedValueOnce(new Error('ack lost'));
    fireEvent.click(revoke);
    const retry = await screen.findByRole('button', { name: 'Retry original revocation' });
    expect(screen.getByRole('button', { name: 'Revoke session grant for Python execution' })).toBeDisabled();
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(1);
    const renewed = { ...sessionGrantA, grant_id: 'renewed-after-ack-loss' };
    const response = sessionGrantRevokeResponse([sessionGrantA], [renewed]);
    response.results[0].status = 'changed';
    httpRequest.mockResolvedValueOnce(response);
    fireEvent.click(retry);
    expect(await screen.findByText(/Some grants were renewed/)).toBeInTheDocument();
    const writes = httpRequest.mock.calls.filter(([method]) => method === 'POST');
    expect(writes).toHaveLength(2);
    expect(writes[1][2]).toBe(writes[0][2]);
    expect(screen.queryByRole('button', { name: 'Retry original revocation' })).not.toBeInTheDocument();
    expect(screen.getByText(sessionGrantA.label)).toBeInTheDocument();
  });
  it('closes the actual old portal on account reset and ignores its late bootstrap', async () => {
    const pending = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    await waitFor(() => expect(httpRequest).toHaveBeenCalledTimes(1));
    const oldPortal = screen.getByRole('menu', { name: 'Permission Mode' });
    await act(async () => setRendererAccountOwner('other-owner'));
    await waitFor(() => expect(oldPortal).not.toBeInTheDocument());
    await act(async () => pending.resolve(sessionGrantsSnapshot()));
    expect(screen.queryByText(sessionGrantA.label)).not.toBeInTheDocument();
    expect(httpRequest.mock.calls.filter(([method]) => method === 'POST')).toHaveLength(0);
  });
  it('does not reuse a same-frame old incarnation or its late response after rerender', async () => {
    const pending = deferred<ReturnType<typeof sessionGrantsSnapshot>>();
    httpRequest.mockReturnValueOnce(pending.promise);
    const view = await renderWithI18n(
      <XSciencePermissionMenu
        {...sessionProps}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    await waitFor(() => expect(httpRequest).toHaveBeenCalledTimes(1));
    const oldPortal = screen.getByRole('menu', { name: 'Permission Mode' });
    const identity = { ...sessionGrantIdentity, frameIncarnationId: 'replacement-frame-incarnation' };
    const next = sessionGrantsSnapshot([sessionGrantC]);
    next.scope.frame_incarnation_id = identity.frameIncarnationId;
    httpRequest.mockResolvedValueOnce(next);
    view.rerender(
      <XSciencePermissionMenu
        sessionIdentity={identity}
        option={mode}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn()}
      />
    );
    await waitFor(() => expect(oldPortal).not.toBeInTheDocument());
    await act(async () => pending.resolve(sessionGrantsSnapshot()));
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    expect(await screen.findByText(sessionGrantC.label)).toBeInTheDocument();
    expect(screen.queryByText(sessionGrantA.label)).not.toBeInTheDocument();
    expect(httpRequest.mock.calls[1][1]).toBe(listPath);
  });
  it('reopens with exact scope and does not consume the first outside Send click', async () => {
    httpRequest.mockResolvedValue(sessionGrantsSnapshot());
    const send = vi.fn();
    await renderWithI18n(
      <>
        <XSciencePermissionMenu
          {...sessionProps}
          option={mode}
          setStatus={{ state: 'idle' }}
          setConfigOption={vi.fn()}
        />
        <button type='button' onClick={send}>
          Send message
        </button>
      </>,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    expect(await screen.findByText(sessionGrantA.label)).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole('button', { name: 'Send message' }));
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
    expect(send).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Permission Mode' })).not.toBeInTheDocument());
    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    await waitFor(() => expect(httpRequest).toHaveBeenCalledTimes(2));
    expect(httpRequest.mock.calls[1][1]).toContain('expected_root_frame_incarnation_id=grant-root-incarnation');
  });
});
