import React, { useState } from 'react';
// Vitest loads Arco's CJS entry, so bootstrap its matching React 19 adapter.
import '@arco-design/web-react/lib/_util/react-19-adapter';
import { Message } from '@arco-design/web-react';
import type { TChatConversation } from '@/common/config/storage';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
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
      );
    },
  };
});

import Layout from '@renderer/components/layout/Layout';
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
    </>
  );
};

const renderLayout = () =>
  render(
    <MemoryRouter initialEntries={['/guid']}>
      <Layout sider={<MenuSider />} />
    </MemoryRouter>
  );

describe('mobile sidebar external menu lifetime', () => {
  const originalWidth = window.innerWidth;
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    sessionStorage.clear();
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

  it.each([
    {
      kind: 'project',
      triggerName: 'Project actions: Example project',
      copyName: 'conversation.history.projectMenu.copyId',
      copiedId: 'project-example',
    },
    {
      kind: 'conversation',
      triggerName: 'common.more · Example conversation',
      copyName: 'conversation.history.copyTaskId',
      copiedId: 'conversation-example',
    },
  ])(
    'closes the $kind body menu on keyboard drawer close without reviving it on reopen',
    async ({ kind, triggerName, copyName, copiedId }) => {
      const user = userEvent.setup();
      const { container } = renderLayout();
      const toggle = screen.getByRole('button', { name: 'Open navigation' });
      await user.click(toggle);
      const trigger = await screen.findByRole('button', { name: triggerName });
      trigger.focus();
      await user.keyboard('{ArrowDown}');
      const menu = await screen.findByRole('menu', { name: triggerName });
      expect(container).not.toContainElement(menu);
      await waitFor(() => expect(within(menu).getByRole('menuitem', { name: copyName })).toHaveFocus());
      const orderWritesBeforeClose = mocks.saveOrder.mock.calls.length;

      // Keyboard activation avoids the unrelated outside-mousedown cleanup path.
      const close = screen.getByTestId('mobile-sider-close');
      close.focus();
      await user.keyboard(' ');
      expect(toggle).toHaveAttribute('aria-expanded', 'false');
      expect(screen.queryByRole('menu', { name: triggerName, hidden: true })).toBeNull();
      expect(container.querySelector('.layout-sider [inert]')).toHaveAttribute('aria-hidden', 'true');
      expect(screen.getByText('Example project')).toBeInTheDocument();
      expect(screen.getByText('Example conversation')).toBeInTheDocument();
      expect(mocks.copy).not.toHaveBeenCalled();

      toggle.focus();
      await user.keyboard('{Enter}');
      expect(toggle).toHaveAttribute('aria-expanded', 'true');
      expect(screen.queryByRole('menu', { name: triggerName })).toBeNull();
      const nextTrigger = screen.getByRole('button', { name: triggerName });
      expect(nextTrigger).toHaveAttribute('aria-expanded', 'false');
      nextTrigger.focus();
      await user.keyboard('{ArrowDown}');
      const nextMenu = await screen.findByRole('menu', { name: triggerName });
      await waitFor(() => expect(within(nextMenu).getByRole('menuitem', { name: copyName })).toHaveFocus());
      if (kind === 'conversation') {
        // Arco's div menu item reads the native legacy Enter fields rather than event.key.
        fireEvent.keyDown(within(nextMenu).getByRole('menuitem', { name: copyName }), {
          key: 'Enter',
          code: 'Enter',
          keyCode: 13,
          which: 13,
        });
      } else {
        await user.keyboard('{Enter}');
      }
      expect(mocks.copy).toHaveBeenCalledExactlyOnceWith(copiedId);
      expect(mocks.projects).toHaveBeenCalledTimes(1);
      expect(mocks.saveOrder).toHaveBeenCalledTimes(orderWritesBeforeClose);
    }
  );
});
