import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BrowserNotificationPreferenceProvider,
  useBrowserNotificationPreference,
} from '@/renderer/hooks/system/notification/BrowserNotificationPreference';
import NotificationsSettings from '@/renderer/pages/settings/NotificationsSettings';
import SettingsRoute from '@/renderer/pages/settings/SettingsRoute';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const stubs = vi.hoisted(() => ({
  owner: 'owner-1',
  get: vi.fn<(key: string, options: { signal: AbortSignal }) => Promise<boolean | undefined>>(),
  put: vi.fn<(key: string, value: boolean, options: { signal: AbortSignal }) => Promise<void>>(),
}));
vi.mock('@/renderer/hooks/context/AuthContext', () => ({
  useAuth: () => ({ status: 'authenticated', user: { id: stubs.owner } }),
}));
vi.mock('@/renderer/services/clientBusinessSettings', () => ({
  getClientBusinessSetting: stubs.get,
  setClientBusinessSetting: stubs.put,
}));
vi.mock('@/renderer/pages/settings/components/SettingsPageWrapper', () => ({
  default: ({ children }: React.PropsWithChildren) => children,
}));
vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

function DeliveryObserver() {
  const { canDeliver } = useBrowserNotificationPreference();
  return <output data-testid='delivery-enabled'>{String(canDeliver())}</output>;
}

function fixture(showSettings = true) {
  return (
    <BrowserNotificationPreferenceProvider>
      {showSettings && <NotificationsSettings />}
      <DeliveryObserver />
    </BrowserNotificationPreferenceProvider>
  );
}

function toggle() {
  return screen.getByRole('switch', { name: 'settings.notificationsPage.taskNotifications' });
}

function signalChange(owner: string) {
  fireEvent(
    window,
    new StorageEvent('storage', {
      key: `x-science:notification-preference-changed:${owner}`,
      newValue: 'changed:other-tab-nonce',
    })
  );
}

beforeEach(() => {
  resetRendererAccountScopeForTest();
  stubs.owner = 'owner-1';
  setRendererAccountOwner(stubs.owner);
  stubs.get.mockReset();
  stubs.put.mockReset();
});

afterEach(cleanup);

describe('browser notification settings', () => {
  it('renders the real lazy route directly from its settings URL', async () => {
    stubs.get.mockResolvedValue(false);
    render(
      <BrowserNotificationPreferenceProvider>
        <MemoryRouter initialEntries={['/settings/notifications']}>
          <Routes>
            <Route path='/settings/:section' element={<SettingsRoute />} />
          </Routes>
        </MemoryRouter>
      </BrowserNotificationPreferenceProvider>
    );
    expect(await screen.findByRole('heading', { name: 'settings.notificationsPage.title' })).toBeInTheDocument();
    await waitFor(() => expect(toggle()).toBeEnabled());
  });

  it('reads an existing preference, saves once and shares the committed value with delivery', async () => {
    stubs.get.mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    let finishSave: (() => void) | undefined;
    stubs.put.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finishSave = resolve;
        })
    );
    const view = render(fixture());

    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');
    fireEvent.click(toggle());
    expect(stubs.put).toHaveBeenCalledWith('system.notificationEnabled', true, { signal: expect.any(AbortSignal) });
    expect(toggle()).toBeDisabled();
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');

    view.rerender(fixture(false));
    view.rerender(fixture());
    fireEvent.click(toggle());
    expect(stubs.put).toHaveBeenCalledTimes(1);

    await act(async () => finishSave?.());
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('true');
    view.unmount();
    render(fixture());
    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'true'));
    expect(stubs.get).toHaveBeenCalledTimes(2);
  });

  it('refreshes a second mounted surface after another tab disables task notifications', async () => {
    let persisted = true;
    stubs.get.mockImplementation(async () => persisted);
    stubs.put.mockImplementation(async (_key, enabled) => {
      persisted = enabled;
    });
    const signalKey = 'x-science:notification-preference-changed:owner-1';
    const signal = vi.spyOn(Storage.prototype, 'setItem');
    try {
      const settingsTab = render(fixture());
      const deliveryTab = render(fixture(false));
      await waitFor(() => expect(deliveryTab.container.querySelector('output')).toHaveTextContent('true'));
      fireEvent.click(toggle());
      await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'false'));
      // jsdom uses one document for these independent React owners. Deliver
      // the real sender's storage hint to the second document explicitly.
      const marker = signal.mock.calls.find(([key]) => key === signalKey)?.[1];
      if (marker) fireEvent(window, new StorageEvent('storage', { key: signalKey, newValue: marker }));

      await waitFor(() => expect(deliveryTab.container.querySelector('output')).toHaveTextContent('false'));
      expect(settingsTab.container.querySelector('output')).toHaveTextContent('false');
      expect(marker).toMatch(/^changed:[a-z0-9.-]+$/i);
      fireEvent.click(toggle());
      await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'true'));
      const markers = signal.mock.calls.filter(([key]) => key === signalKey).map(([, value]) => value);
      expect(markers).toHaveLength(2);
      expect(markers[1]).not.toBe(marker);
      expect(window.localStorage.getItem(signalKey)).toBeNull();
      expect(stubs.put).toHaveBeenCalledTimes(2);
    } finally {
      signal.mockRestore();
    }
  });

  it('coalesces invalidations during a read and ignores another account', async () => {
    let finishOldRead: ((enabled: boolean) => void) | undefined;
    stubs.get
      .mockResolvedValueOnce(true)
      .mockImplementationOnce(
        () =>
          new Promise<boolean>((resolve) => {
            finishOldRead = resolve;
          })
      )
      .mockResolvedValueOnce(false);
    render(fixture());
    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'true'));

    signalChange('owner-2');
    expect(stubs.get).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('true');
    signalChange('owner-1');
    expect(toggle()).toBeDisabled();
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');
    signalChange('owner-1');
    signalChange('owner-1');
    expect(stubs.get).toHaveBeenCalledTimes(2);

    await act(async () => finishOldRead?.(true));
    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(stubs.get).toHaveBeenCalledTimes(3);
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');
  });

  it('refreshes after an in-flight write invalidation without aborting or replaying the PUT', async () => {
    let finishSave: (() => void) | undefined;
    stubs.get.mockResolvedValue(false);
    stubs.put.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finishSave = resolve;
        })
    );
    render(fixture());
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());

    const writeSignal = stubs.put.mock.calls[0][2].signal;
    signalChange('owner-1');
    expect(stubs.get).toHaveBeenCalledTimes(1);
    expect(writeSignal.aborted).toBe(false);
    expect(toggle()).toBeDisabled();
    await act(async () => finishSave?.());

    await waitFor(() => expect(toggle()).toBeEnabled());
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');
    expect(stubs.get).toHaveBeenCalledTimes(2);
    expect(stubs.put).toHaveBeenCalledTimes(1);
  });

  it('keeps delivery off while the value is unknown and retries the read only', async () => {
    stubs.get.mockRejectedValueOnce(new Error('read failed')).mockResolvedValueOnce(undefined);
    render(fixture());
    expect(toggle()).toBeDisabled();
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');
    expect(await screen.findByRole('alert')).toHaveTextContent('settings.notificationsPage.readFailed');

    fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'true'));
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('true');
    expect(stubs.put).not.toHaveBeenCalled();
  });

  it('rereads after a failed PUT without replaying the write', async () => {
    stubs.get.mockResolvedValueOnce(true).mockResolvedValueOnce(true);
    stubs.put.mockRejectedValueOnce(new Error('lost response'));
    render(fixture());
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());

    expect(await screen.findByRole('alert')).toHaveTextContent('settings.notificationsPage.saveFailed');
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    expect(stubs.get).toHaveBeenCalledTimes(2);
    expect(stubs.put).toHaveBeenCalledTimes(1);
  });

  it('rereads after the same owner starts a new authentication generation', async () => {
    stubs.get.mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    const view = render(fixture());
    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'true'));

    setRendererAccountOwner(null);
    setRendererAccountOwner(stubs.owner);
    view.rerender(fixture());

    await waitFor(() => expect(toggle()).toHaveAttribute('aria-checked', 'false'));
    expect(stubs.get).toHaveBeenCalledTimes(2);
    expect(stubs.put).not.toHaveBeenCalled();
  });

  it('discards a pending save when the authenticated owner changes', async () => {
    stubs.get.mockResolvedValue(false);
    let finishSave: (() => void) | undefined;
    stubs.put.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finishSave = resolve;
        })
    );
    const view = render(fixture());
    await waitFor(() => expect(toggle()).toBeEnabled());
    fireEvent.click(toggle());
    const oldSignal = stubs.put.mock.calls[0][2].signal;

    stubs.owner = 'owner-2';
    setRendererAccountOwner(stubs.owner);
    view.rerender(fixture());
    expect(oldSignal.aborted).toBe(true);
    await waitFor(() => expect(toggle()).toBeEnabled());
    await act(async () => finishSave?.());

    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByTestId('delivery-enabled')).toHaveTextContent('false');
    expect(stubs.get).toHaveBeenCalledTimes(2);
    expect(stubs.put).toHaveBeenCalledTimes(1);
  });
});
