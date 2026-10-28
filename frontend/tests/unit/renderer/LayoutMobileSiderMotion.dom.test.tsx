import React, { useLayoutEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}));

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

// Exercise the real LayoutContext owner without unrelated titlebar services.
vi.mock('@/renderer/components/layout/Titlebar', async () => {
  const { useLayoutContext } = await import('@renderer/hooks/context/LayoutContext');
  return {
    default: () => {
      const layout = useLayoutContext()!;
      return (
        <button
          type='button'
          aria-label={layout.siderCollapsed ? 'common.expandSidebar' : 'common.collapseSidebar'}
          aria-expanded={!layout.siderCollapsed}
          onClick={() => layout.setSiderCollapsed(!layout.siderCollapsed)}
        >
          Navigation
        </button>
      );
    },
  };
});

import Layout from '@renderer/components/layout/Layout';
import { SiderLibraryEntry } from '@renderer/components/layout/Sider/SiderNav/SiderLibraryEntry';
import { useLayoutContext } from '@renderer/hooks/context/LayoutContext';
import { useNavigationHistory } from '@renderer/hooks/context/NavigationHistoryContext';
import { getRendererAccountScopeToken } from '@renderer/services/rendererAccountScope';

const DraftSider = ({
  collapsed,
  onSessionClick,
  libraryNavigation,
}: {
  collapsed?: boolean;
  onSessionClick?: () => void;
  libraryNavigation?: React.ReactNode;
}) => {
  const [draft, setDraft] = useState('Original draft');
  return (
    <>
      <div hidden={libraryNavigation !== undefined}>
        <SiderLibraryEntry collapsed={collapsed} onNavigate={onSessionClick} />
        <span className='collapsed-hidden'>Retained conversation title</span>
        <input
          aria-label='Retained navigation draft'
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
        />
      </div>
      {libraryNavigation}
    </>
  );
};

const MainContent = () => {
  const location = useLocation();
  const rail = useLayoutContext()?.libraryRail;
  const history = useNavigationHistory();
  const owner = useMemo(() => ({}), []);
  const account = getRendererAccountScopeToken();
  // Model the real Page's rail registration instead of testing an unowned
  // /library route. Layout and NavigationHistory remain their actual owners.
  useLayoutEffect(() => rail?.register(owner, account), [rail?.register, owner, account]);
  return (
    <>
      <output data-testid='main-route'>{location.pathname}</output>
      {rail?.host &&
        rail.owner === owner &&
        createPortal(
          <button type='button' onClick={() => history?.returnFromLibrary()}>
            Return to workspace
          </button>,
          rail.host
        )}
    </>
  );
};

const renderLayout = () =>
  render(
    <MemoryRouter initialEntries={['/guid']}>
      <Routes>
        <Route element={<Layout sider={<DraftSider />} />}>
          <Route path='*' element={<MainContent />} />
        </Route>
      </Routes>
    </MemoryRouter>
  );

const resizeViewport = (width: number) => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: width });
  fireEvent(window, new Event('resize'));
};

describe('mobile sidebar presentation lifecycle', () => {
  const originalWidth = window.innerWidth;

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null as MediaQueryList['onchange'],
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  });

  afterEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalWidth });
    vi.clearAllMocks();
  });

  it('keeps full-width mobile contents mounted while toggles immediately update interaction boundaries', async () => {
    const user = userEvent.setup();
    const { container } = renderLayout();
    const sider = container.querySelector('.layout-sider') as HTMLElement;
    const draft = screen.getByLabelText('Retained navigation draft');
    const contents = draft.closest('[inert]')!;

    expect(sider).toHaveStyle({ width: '261px', transform: 'translateX(-100%)', pointerEvents: 'none' });
    expect(sider).toHaveClass('layout-sider--mobile');
    expect(screen.getByText('Retained conversation title').closest('.collapsed')).toBeNull();
    expect(sider.style.transition).toBe('');
    expect(contents).toHaveAttribute('aria-hidden', 'true');
    expect(container.querySelector('.workbench-modal-backdrop')).toBeNull();
    expect(screen.getByText('conversation.libraryWorkbench.title')).toBeInTheDocument();

    const toggle = screen.getByRole('button', { name: 'common.expandSidebar' });
    toggle.focus();
    await user.keyboard('{Enter}');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(sider).toHaveStyle({ width: '261px', transform: 'translateX(0)', pointerEvents: 'auto' });
    expect(contents).not.toHaveAttribute('inert');
    expect(contents).not.toHaveAttribute('aria-hidden');
    expect(container.querySelector('.workbench-modal-backdrop')).toBeInTheDocument();
    fireEvent.change(draft, { target: { value: 'Keep this draft' } });

    toggle.focus();
    await user.keyboard(' ');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(sider).toHaveStyle({ width: '261px', transform: 'translateX(-100%)', pointerEvents: 'none' });
    expect(contents).toHaveAttribute('inert');
    expect(contents).toHaveAttribute('aria-hidden', 'true');
    expect(container.querySelector('.workbench-modal-backdrop')).toBeNull();
    expect(screen.getByLabelText('Retained navigation draft')).toBe(draft);
    expect(draft).toHaveValue('Keep this draft');
    expect(screen.getByText('conversation.libraryWorkbench.title')).toBeInTheDocument();
    expect(screen.getByText('Retained conversation title').closest('.collapsed')).toBeNull();

    // No timer or transition-end event is needed to reverse the same owner.
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(contents).not.toHaveAttribute('inert');
  });

  it('retains navigation and the saved desktop width when a mobile drawer resizes or changes layout mode', async () => {
    localStorage.setItem('x-science:desktop-sider-width', '340');
    const user = userEvent.setup();
    const { container } = renderLayout();
    const sider = container.querySelector('.layout-sider') as HTMLElement;
    const draft = screen.getByLabelText('Retained navigation draft');

    await user.click(screen.getByRole('button', { name: 'common.expandSidebar' }));
    await user.click(screen.getByRole('link', { name: 'conversation.libraryWorkbench.title' }));
    expect(screen.getByTestId('main-route')).toHaveTextContent('/library');
    expect(sider).toHaveStyle({ width: '261px', pointerEvents: 'none' });
    expect(draft.closest('[inert]')).not.toBeNull();

    resizeViewport(700);
    expect(sider).toHaveStyle({ width: '420px', transform: 'translateX(-100%)' });
    expect(localStorage.getItem('x-science:desktop-sider-width')).toBe('340');
    expect(screen.getByText('conversation.libraryWorkbench.title')).toBeInTheDocument();

    resizeViewport(1440);
    expect(sider).not.toHaveClass('layout-sider--mobile');
    expect(sider).toHaveStyle({ width: '56px' });
    expect(screen.queryByTestId('sider-resize-handle')).toBeNull();
    expect(localStorage.getItem('x-science:desktop-sider-width')).toBe('340');
    await user.click(screen.getByRole('button', { name: 'Return to workspace' }));
    expect(screen.getByTestId('main-route')).toHaveTextContent('/guid');
    expect(sider).toHaveStyle({ width: '0px' });
    expect(screen.queryByText('conversation.libraryWorkbench.title')).toBeNull();
    expect(screen.getByTestId('sider-resize-handle')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'common.expandSidebar' }));
    expect(sider).toHaveStyle({ width: '340px' });
    expect(screen.getByLabelText('Retained navigation draft')).toBe(draft);
    expect(screen.getByText('conversation.libraryWorkbench.title')).toBeInTheDocument();
    expect(screen.getByTestId('main-route')).toHaveTextContent('/guid');
  });
});
