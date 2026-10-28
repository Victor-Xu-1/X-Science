import React, { useLayoutEffect, useRef, useState } from 'react';
// Vitest loads Arco's CJS entry, so bootstrap its matching React 19 adapter.
import '@arco-design/web-react/lib/_util/react-19-adapter';
import { Message } from '@arco-design/web-react';
import type { TChatConversation } from '@/common/config/storage';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  projects: vi.fn(async () => [
    {
      projectId: 'project-example',
      name: 'Example project',
      description: '',
      context: '',
      conversationCount: 1,
      artifactCount: 0,
      latestConversationId: 'conversation-example',
      createdAt: null as string | null,
      updatedAt: null as string | null,
      lastActiveAt: null as string | null,
    },
  ]),
  copy: vi.fn(async (_text: string): Promise<void> => {}),
  saveOrder: vi.fn(async (): Promise<void> => {}),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: { name?: string }) =>
      key === 'conversation.history.projectMenu.label' ? `Project actions: ${values?.name}` : key,
    i18n: { language: 'en' },
  }),
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
vi.mock('@renderer/utils/ui/siderTooltip', () => ({
  cleanupSiderTooltips: () => {},
  getSiderTooltipProps: () => ({ disabled: true, popupVisible: false }),
}));
vi.mock('@renderer/hooks/ui/useConversationShortcuts', () => ({ useConversationShortcuts: () => {} }));
vi.mock('@renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
vi.mock('@/renderer/utils/ui/clipboard', () => ({ copyText: mocks.copy }));
vi.mock('@/renderer/services/projectSidebarOrder', () => ({
  loadProjectSidebarOrder: async (): Promise<string[]> => [],
  saveProjectSidebarOrder: mocks.saveOrder,
}));
vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceProjects: mocks.projects,
  createXScienceProject: vi.fn(),
  updateXScienceProject: vi.fn(),
  deleteXScienceProject: vi.fn(),
  loadXScienceProjectArtifacts: vi.fn(),
}));
vi.mock('@/renderer/components/layout/Titlebar', async () => {
  const { useLayoutContext } = await import('@renderer/hooks/context/LayoutContext');
  return {
    default: () => {
      const layout = useLayoutContext()!;
      return (
        <header>
          <button
            type='button'
            id={layout.siderToggleId}
            aria-controls={layout.siderId}
            aria-label={layout.siderCollapsed ? 'Open navigation' : 'Close navigation'}
            aria-expanded={!layout.siderCollapsed}
            onClick={() => layout.setSiderCollapsed(!layout.siderCollapsed)}
          >
            Navigation
          </button>
          <button data-testid='background-notification'>Notification</button>
        </header>
      );
    },
  };
});

import Layout from '@renderer/components/layout/Layout';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import ProjectHistorySection from '@/renderer/pages/conversation/GroupedHistory/ProjectHistorySection';
import ConversationRow from '@/renderer/pages/conversation/GroupedHistory/ConversationRow';

const conversation: TChatConversation = {
  id: 'conversation-example',
  type: 'acp',
  name: 'Example conversation',
  created_at: 1,
  modified_at: 1,
  status: 'finished',
  extra: { backend: 'xscience' },
};
const noAction = (): void => {};

const MenuSider = ({ collapsed = false }: { collapsed?: boolean }) => {
  const [menuVisible, setMenuVisible] = useState(false);
  const [draft, setDraft] = useState('保留 draft');
  const [dialogOpen, setDialogOpen] = useState(false);
  return (
    <>
      <ProjectHistorySection collapsed={collapsed} activeProjectId='project-example' />
      <ConversationRow
        conversation={conversation}
        isGenerating={false}
        hasCompletionUnread={false}
        collapsed={collapsed}
        tooltipEnabled={false}
        batchMode={false}
        checked={false}
        selected={false}
        menuVisible={menuVisible}
        onToggleChecked={noAction}
        onConversationPrefetch={noAction}
        onConversationClick={noAction}
        onOpenMenu={() => setMenuVisible(true)}
        onMenuVisibleChange={(_id, visible) => setMenuVisible(visible)}
        onEditStart={noAction}
        onMove={noAction}
        onDelete={noAction}
        onExport={noAction}
        onDownloadArtifacts={noAction}
        onViewNotebook={noAction}
      />
      <input aria-label='Navigation draft' value={draft} onChange={(event) => setDraft(event.target.value)} />
      <button type='button' onClick={() => setDialogOpen(true)}>
        Open extra dialog
      </button>
      <button type='button'>Last navigation control</button>
      <WorkbenchModal
        visible={dialogOpen}
        title='Extra dialog'
        footer={null}
        onCancel={() => setDialogOpen(false)}
        unmountOnExit
        getPopupContainer={() => document.body}
      >
        <input aria-label='Dialog field' />
      </WorkbenchModal>
    </>
  );
};

let navigateForTest: ((path: string) => void) | undefined;
const MainContent = () => {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const button = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    navigateForTest = (path) => {
      void navigate(path);
    };
  }, [navigate]);
  useLayoutEffect(() => {
    if (pathname === '/after') button.current?.focus();
  }, [pathname]);
  return (
    <button ref={button} data-testid='background-import'>
      Import PDF
    </button>
  );
};
const renderLayout = () =>
  render(
    <MemoryRouter initialEntries={['/guid']}>
      <Routes>
        <Route element={<Layout sider={<MenuSider />} />}>
          <Route path='*' element={<MainContent />} />
        </Route>
      </Routes>
    </MemoryRouter>
  );

describe('mobile sidebar focus boundary', () => {
  const originalWidth = window.innerWidth;
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    sessionStorage.clear();
    navigateForTest = undefined;
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null as MediaQueryList['onchange'],
        addListener: noAction,
        removeListener: noAction,
        addEventListener: noAction,
        removeEventListener: noAction,
        dispatchEvent: () => false,
      }),
    });
  });
  afterEach(() => {
    act(() => Message.clear());
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalWidth });
  });

  it('contains Tab and ShiftTab inside the open drawer and restores its opener on Escape', async () => {
    const user = userEvent.setup();
    const { container } = renderLayout();
    const toggle = screen.getByRole('button', { name: 'Open navigation' });
    await user.click(toggle);
    const initialFocus = document.activeElement;
    await user.tab();
    const sidebar = container.querySelector('.layout-sider')!;
    expect(sidebar.contains(document.activeElement)).toBe(true);
    const close = screen.getByTestId('mobile-sider-close');
    expect(initialFocus).toBe(close);
    const dialog = screen.getByRole('dialog', { name: 'common.workspaceNavigation' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByTestId('background-notification').closest('[inert]')).not.toBeNull();
    expect(screen.getByTestId('background-import').closest('[inert]')).not.toBeNull();
    const last = screen.getByRole('button', { name: 'Last navigation control' });
    last.focus();
    await user.tab();
    expect(toggle).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.tab({ shift: true });
    expect(toggle).toHaveFocus();
    await user.tab({ shift: true });
    expect(last).toHaveFocus();
    screen.getByTestId('background-import').focus();
    expect(dialog.contains(document.activeElement)).toBe(true);
    const draft = screen.getByRole('textbox', { name: 'Navigation draft' });
    await user.clear(draft);
    await user.type(draft, '保留 edited draft');
    await user.keyboard('{Escape}');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(toggle).toHaveFocus());
    expect(screen.getByLabelText('Navigation draft')).toBe(draft);
    expect(draft).toHaveValue('保留 edited draft');
    expect(screen.getByTestId('background-import').closest('[inert]')).toBeNull();
  });

  it('keeps the original top toggle available while its neighbouring controls are isolated', async () => {
    const user = userEvent.setup();
    renderLayout();
    const toggle = screen.getByRole('button', { name: 'Open navigation' });
    await user.click(toggle);
    const dialog = screen.getByRole('dialog', { name: 'common.workspaceNavigation' });
    expect(toggle).toHaveAttribute('aria-controls', dialog.id);
    expect(dialog.getAttribute('aria-owns')?.split(' ')).toContain(toggle.id);
    expect(toggle.closest('[inert], [aria-hidden="true"]')).toBeNull();
    expect(screen.getByTestId('background-notification').closest('[inert]')).not.toBeNull();
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(dialog).not.toHaveAttribute('aria-owns');
    await waitFor(() => expect(toggle).toHaveFocus());
  });

  it('isolates newly inserted shell background and restores its original attributes on close', async () => {
    const user = userEvent.setup();
    const view = renderLayout();
    await user.click(screen.getByRole('button', { name: 'Open navigation' }));
    const dialog = screen.getByRole('dialog', { name: 'common.workspaceNavigation' });
    const inserted = document.createElement('button');
    inserted.textContent = 'Late background control';
    inserted.setAttribute('aria-hidden', 'false');
    view.container.querySelector('.app-shell')!.append(inserted);
    inserted.focus();
    expect(dialog.contains(document.activeElement)).toBe(true);
    await waitFor(() => {
      expect(inserted).toHaveAttribute('inert');
      expect(inserted).toHaveAttribute('aria-hidden', 'true');
    });
    await user.click(screen.getByTestId('mobile-sider-close'));
    expect(inserted).not.toHaveAttribute('inert');
    expect(inserted).toHaveAttribute('aria-hidden', 'false');
    inserted.focus();
    expect(inserted).toHaveFocus();
    inserted.remove();
  });

  it.each([
    { kind: 'project', name: 'Project actions: Example project', first: 'conversation.history.projectMenu.copyId' },
    { kind: 'conversation', name: 'common.more · Example conversation', first: 'conversation.history.copyTaskId' },
  ])('includes the real $kind body portal without consuming the menu owner Escape', async ({ name, first }) => {
    const user = userEvent.setup();
    const { container } = renderLayout();
    const toggle = screen.getByRole('button', { name: 'Open navigation' });
    await user.click(toggle);
    const trigger = await screen.findByRole('button', { name });
    trigger.focus();
    await user.keyboard('{ArrowDown}');
    const menu = await screen.findByRole('menu', { name });
    expect(container).not.toContainElement(menu);
    await waitFor(() => expect(within(menu).getByRole('menuitem', { name: first })).toHaveFocus());
    expect(trigger).toHaveAttribute('aria-controls', menu.id);
    const dialog = screen.getByRole('dialog', { name: 'common.workspaceNavigation' });
    await waitFor(() => expect(dialog.getAttribute('aria-owns')?.split(' ')).toContain(menu.id));
    await user.tab();
    const target = document.activeElement;
    expect(container.querySelector('.layout-sider')!.contains(target) || menu.contains(target)).toBe(true);
    within(menu).getByRole('menuitem', { name: first }).focus();
    await user.keyboard('{Escape}');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(trigger).not.toHaveAttribute('aria-controls');
    await waitFor(() => expect(dialog.getAttribute('aria-owns')?.split(' ')).not.toContain(menu.id));
    screen.getByTestId('mobile-sider-close').focus();
    await user.keyboard('{Escape}');
    await waitFor(() => expect(toggle).toHaveFocus());
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('menu', { name, hidden: true })).toBeNull();
    await user.keyboard('{Enter}');
    expect(screen.queryByRole('menu', { name })).toBeNull();
  });

  it('cleans up on backdrop, navigation and desktop mode without delayed focus theft', async () => {
    const user = userEvent.setup();
    const { container } = renderLayout();
    const toggle = screen.getByRole('button', { name: 'Open navigation' });
    await user.click(toggle);
    await user.click(container.querySelector('.workbench-modal-backdrop')!);
    await waitFor(() => expect(toggle).toHaveFocus());
    await user.keyboard('{Enter}');
    fireEvent.click(screen.getByTestId('mobile-sider-close'));
    fireEvent.click(toggle);
    await waitFor(() => expect(screen.getByTestId('mobile-sider-close')).toHaveFocus());
    expect(screen.getByTestId('background-import').closest('[inert]')).not.toBeNull();
    act(() => navigateForTest!('/after'));
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(screen.getByTestId('background-import')).toHaveFocus());
    expect(screen.getByTestId('background-import').closest('[inert]')).toBeNull();
    await user.click(toggle);
    const draft = screen.getByRole('textbox', { name: 'Navigation draft' });
    draft.focus();
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    fireEvent(window, new Event('resize'));
    expect(screen.queryByRole('dialog', { name: 'common.workspaceNavigation' })).toBeNull();
    expect(screen.getByTestId('background-import').closest('[inert]')).toBeNull();
    expect(draft).toHaveFocus();
    screen.getByTestId('background-notification').focus();
    expect(screen.getByTestId('background-notification')).toHaveFocus();
  });

  it('preserves later background ownership even when it changes in the closing tick', async () => {
    const user = userEvent.setup();
    const { container } = renderLayout();
    const toggle = screen.getByRole('button', { name: 'Open navigation' });
    const main = screen.getByTestId('background-import').closest('[role="main"]')!;
    main.setAttribute('aria-hidden', 'false');
    await user.click(toggle);
    main.setAttribute('inert', '');
    main.setAttribute('aria-hidden', 'true');
    fireEvent.click(screen.getByTestId('mobile-sider-close'));
    expect(main).toHaveAttribute('inert');
    expect(main).toHaveAttribute('aria-hidden', 'true');
    main.removeAttribute('inert');
    main.setAttribute('aria-hidden', 'false');
    fireEvent.click(toggle);
    main.setAttribute('aria-hidden', 'false');
    fireEvent.click(screen.getByTestId('mobile-sider-close'));
    expect(main).not.toHaveAttribute('inert');
    expect(main).toHaveAttribute('aria-hidden', 'false');
    expect(container.querySelector('.workbench-modal-backdrop')).toBeNull();
  });

  it('yields to the real WorkbenchModal owner and releases the boundary on unmount', async () => {
    const user = userEvent.setup();
    const view = renderLayout();
    await user.click(screen.getByRole('button', { name: 'Open navigation' }));
    await user.click(screen.getByRole('button', { name: 'Open extra dialog' }));
    const modal = screen.getByRole('dialog', { name: 'Extra dialog' });
    expect(view.container).not.toContainElement(modal);
    expect(modal).toHaveAttribute('aria-modal', 'true');
    const field = within(modal).getByRole('textbox', { name: 'Dialog field' });
    field.focus();
    expect(field).toHaveFocus();
    await user.tab();
    expect(modal.contains(document.activeElement)).toBe(true);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Extra dialog' })).toBeNull());
    expect(screen.getByRole('dialog', { name: 'common.workspaceNavigation' })).toBeInTheDocument();
    await user.tab();
    expect(view.container.querySelector('.layout-sider')!.contains(document.activeElement)).toBe(true);
    const staleDialog = document.createElement('div');
    staleDialog.setAttribute('role', 'dialog');
    staleDialog.setAttribute('aria-modal', 'true');
    staleDialog.hidden = true;
    const staleTarget = document.createElement('button');
    staleDialog.append(staleTarget);
    document.body.append(staleDialog);
    staleTarget.focus();
    expect(view.container.querySelector('.layout-sider')!.contains(document.activeElement)).toBe(true);
    staleDialog.remove();
    view.unmount();
    const outside = document.createElement('button');
    document.body.append(outside);
    outside.focus();
    await Promise.resolve();
    expect(outside).toHaveFocus();
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    outside.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(false);
    outside.remove();
  });
});
