import React, { useEffect } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useLayoutContext } from '@/renderer/hooks/context/LayoutContext';
import type { LibraryRailSlot } from '@/renderer/hooks/context/LayoutContext';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { installCollectionHTTP, collection } from './library/libraryCollectionsFixtures';
import { installLiteratureLocks } from './conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../i18nTestUtils';
import Layout from '@/renderer/components/layout/Layout';
import LibraryPage from '@/renderer/pages/library/LibraryPage';

vi.mock('@/common', () => ({
  ipcBridge: {
    application: { openDevTools: { invoke: vi.fn() }, logStream: { on: () => () => {} } },
    task: { stopAll: { invoke: vi.fn() } },
  },
}));
vi.mock('@/renderer/components/layout/PwaPullToRefresh', () => ({ default: (): null => null }));
vi.mock('@/renderer/components/chat/ArchiveUndoNotice', () => ({ default: (): null => null }));
vi.mock('@renderer/hooks/system/useDeepLink', () => ({ useDeepLink: () => {} }));
vi.mock('@renderer/hooks/system/notification/useNotificationClick', () => ({ useNotificationClick: () => {} }));
vi.mock('@renderer/hooks/system/notification/useBrowserNotification', () => ({ useBrowserNotification: () => {} }));
vi.mock('@renderer/hooks/file/useDirectorySelection', () => ({
  useDirectorySelection: (): { contextHolder: null } => ({ contextHolder: null }),
}));
vi.mock('@renderer/utils/ui/siderTooltip', () => ({ cleanupSiderTooltips: () => {} }));
vi.mock('@renderer/hooks/ui/useConversationShortcuts', () => ({ useConversationShortcuts: () => {} }));
vi.mock('@renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
let slot: LibraryRailSlot | undefined;
vi.mock('@/renderer/components/layout/Titlebar', () => ({
  default: () => {
    const layout = useLayoutContext()!,
      navigate = useNavigate();
    slot = layout.libraryRail;
    return (
      <header data-testid='application-titlebar'>
        <button
          id={layout.siderToggleId}
          aria-controls={layout.siderId}
          aria-label={layout.siderCollapsed ? 'Open navigation' : 'Close navigation'}
          onClick={() => layout.setSiderCollapsed(!layout.siderCollapsed)}
        >
          Navigation
        </button>
        <button onClick={() => void navigate('/library')}>Library route</button>
        <button data-testid='global-notification'>Notification</button>
      </header>
    );
  },
}));
const mounted = vi.fn(),
  disposed = vi.fn();
function Sider({ libraryNavigation }: { libraryNavigation?: React.ReactNode }) {
  useEffect(() => {
    mounted();
    return disposed;
  }, []);
  return (
    <>
      <div>{libraryNavigation ?? <span>Workspace navigation</span>}</div>
      <button>Account footer</button>
    </>
  );
}
function Location() {
  const path = useLocation();
  return <output aria-label='route'>{path.pathname + path.search + path.hash}</output>;
}
function Tree({ initial = '/conversation/retained?q=draft#anchor' }: { initial?: string }) {
  return (
    <MemoryRouter initialEntries={[initial]}>
      <Routes>
        <Route element={<Layout sider={<Sider />} />}>
          <Route
            path='/library'
            element={
              <>
                <LibraryPage />
                <Location />
              </>
            }
          />
          <Route
            path='*'
            element={
              <>
                <button data-testid='workspace-content'>Workspace</button>
                <Location />
              </>
            }
          />
        </Route>
      </Routes>
    </MemoryRouter>
  );
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('rail-owner');
  localStorage.clear();
  sessionStorage.clear();
  mounted.mockClear();
  disposed.mockClear();
  installLiteratureLocks();
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1280 });
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: (media: string) => ({
      matches: false,
      media,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent() {
        return false;
      },
    }),
  });
  installCollectionHTTP({ collections: () => [collection('methods', 'Methods')] });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('hosts one real Page rail at 240/56 and retains workspace preference, chrome and the precise return route', async () => {
  localStorage.setItem('x-science:desktop-sider-width', '330');
  const { container } = await renderWithI18n(<Tree />, 'en-US');
  fireEvent.click(screen.getByRole('button', { name: 'Close navigation' }));
  fireEvent.click(screen.getByRole('button', { name: 'Library route' }));
  await screen.findByRole('button', { name: 'Methods' });
  expect(container.querySelectorAll('.layout-sider')).toHaveLength(1);
  expect(container.querySelector('.library-workbench__navigation')).toBeNull();
  const physical = container.querySelector('.layout-sider') as HTMLElement;
  expect(physical.style.width).toBe('240px');
  expect(screen.getByTestId('application-titlebar')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Account footer' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' }));
  expect(physical.style.width).toBe('56px');
  fireEvent.keyDown(window, { key: 'b', ctrlKey: true });
  expect(physical.style.width).toBe('240px');
  const modal = document.createElement('div');
  modal.setAttribute('role', 'dialog');
  document.body.append(modal);
  fireEvent.keyDown(window, { key: 'b', ctrlKey: true });
  expect(physical.style.width).toBe('240px');
  modal.remove();
  fireEvent.click(screen.getByRole('button', { name: 'Back to Chat' }));
  await waitFor(() =>
    expect(screen.getByLabelText('route')).toHaveTextContent('/conversation/retained?q=draft#anchor')
  );
  expect(screen.getByRole('button', { name: 'Open navigation' })).toBeInTheDocument();
  expect(localStorage.getItem('x-science:desktop-sider-width')).toBe('330');
  expect(mounted).toHaveBeenCalledTimes(1);
  expect(disposed).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Library route' }));
  await screen.findByRole('button', { name: 'Methods' });
  expect(physical.style.width).toBe('240px');
});

it('keeps one mobile focus scope and rejects stale slot cleanup after a newer owner claim', async () => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
  const user = userEvent.setup();
  await renderWithI18n(<Tree initial='/library' />, 'en-US');
  const toggle = await screen.findByRole('button', { name: 'Open navigation' });
  await user.click(toggle);
  expect(screen.getByRole('dialog', { name: 'Workspace navigation' })).toHaveAttribute('aria-modal', 'true');
  expect(screen.getByTestId('global-notification').closest('[inert]')).not.toBeNull();
  await user.tab();
  expect(
    document.getElementById(toggle.getAttribute('aria-controls')!)?.contains(document.activeElement) ||
      document.activeElement === toggle
  ).toBe(true);
  fireEvent.keyDown(document.activeElement ?? window, { key: 'Escape' });
  await waitFor(() => expect(toggle).toHaveFocus());
  expect(screen.getByRole('button', { name: 'Open navigation' })).toBeInTheDocument();
  let release!: () => void;
  const next = {};
  act(() => {
    release = slot!.register({}, getRendererAccountScopeToken());
  });
  act(() => {
    slot!.register(next, getRendererAccountScopeToken());
  });
  act(release);
  expect(slot!.owner).toBe(next);
  act(() => setRendererAccountOwner('next-account'));
  act(release);
  expect(slot!.owner).not.toBe(next);
});

it('restores desktop group focus to the single rail toggle when the shortcut collapses its heading', async () => {
  const { container } = await renderWithI18n(<Tree initial='/library' />, 'en-US');
  await screen.findByRole('button', { name: 'Methods' });
  const physical = container.querySelector('.layout-sider') as HTMLElement;
  screen.getByRole('button', { name: 'Collections' }).focus();
  fireEvent.keyDown(window, { key: 'b', ctrlKey: true });
  expect(physical.style.width).toBe('56px');
  expect(screen.getByRole('button', { name: 'Expand sidebar' })).toHaveFocus();
  const prevented = new KeyboardEvent('keydown', { key: 'b', ctrlKey: true, cancelable: true });
  prevented.preventDefault();
  window.dispatchEvent(prevented);
  expect(physical.style.width).toBe('56px');
  fireEvent.keyDown(window, { key: 'b', ctrlKey: true });
  expect(physical.style.width).toBe('240px');
});

it('keeps mobile rail group interactions out of the background content close action', async () => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
  const user = userEvent.setup();
  await renderWithI18n(<Tree initial='/library' />, 'en-US');
  await user.click(await screen.findByRole('button', { name: 'Open navigation' }));
  await screen.findByRole('button', { name: 'Methods' });
  await user.click(screen.getByRole('button', { name: 'Collections' }));
  expect(screen.getByRole('dialog', { name: 'Workspace navigation' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Collections' })).toHaveAttribute('aria-expanded', 'false');
});
