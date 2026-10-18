import React from 'react';
import { MemoryRouter } from 'react-router';
import { cleanup, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import { LayoutContext } from '@/renderer/hooks/context/LayoutContext';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import Titlebar from '@/renderer/components/layout/Titlebar';
import SiderFooter from '@/renderer/components/layout/Sider/SiderFooter';

vi.mock('@/renderer/pages/conversation/GroupedHistory/ConversationSearchPopover', () => ({ default: () => null }));
vi.mock('@/renderer/components/layout/Titlebar/ProjectCommandPalette', () => ({ default: () => null }));
vi.mock('@/renderer/components/layout/Titlebar/DesktopConversationTitle', () => ({ default: () => null }));
vi.mock('@/renderer/components/layout/Titlebar/MobileConversationBrand', () => ({ default: () => null }));
vi.mock('@/renderer/components/layout/WindowControls', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/utils/conversationCache', () => ({ getConversationOrNull: async () => null }));
vi.mock('@/renderer/utils/platform', () => ({
  isElectronDesktop: () => false,
  isMacOS: () => false,
  isWindows: () => false,
  openExternalUrl: vi.fn(),
}));
vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: { responseStream: { on: () => () => {} }, turnCompleted: { on: () => () => {} } },
    realtime: { reconnected: { on: () => () => {} } },
    runtime: { statusChanged: { on: () => () => {} } },
  },
}));

beforeEach(() => {
  setRendererAccountOwner('notification-owner');
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{}', { headers: { 'content-type': 'application/json' } }))
  );
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
});

describe('notification inbox existing navigation entries', () => {
  it('exposes the same Messages entry in the Guid titlebar and existing account footer', async () => {
    await renderWithI18n(
      <MemoryRouter initialEntries={['/guid']}>
        <LayoutContext.Provider value={{ isMobile: false, siderCollapsed: false, setSiderCollapsed: vi.fn() }}>
          <Titlebar workspaceAvailable={false} />
          <SiderFooter
            isMobile={false}
            isSettings={false}
            username='Victor'
            siderTooltipProps={{ disabled: true } as never}
            onSettingsClick={vi.fn()}
            onAccountClick={vi.fn()}
            onPlansUsageClick={vi.fn()}
          />
        </LayoutContext.Provider>
      </MemoryRouter>,
      'en-US'
    );
    const bells = screen.getAllByRole('button', { name: /^Messages,/ });
    expect(bells).toHaveLength(2);
    expect(bells[0].closest('.app-titlebar__toolbar')).not.toBeNull();
    expect(bells[1].closest('.sider-footer')).not.toBeNull();
  });

  it('keeps Messages reachable in the mobile conversation titlebar when navigation is collapsed', async () => {
    await renderWithI18n(
      <MemoryRouter initialEntries={['/conversation/frame-1']}>
        <LayoutContext.Provider value={{ isMobile: true, siderCollapsed: true, setSiderCollapsed: vi.fn() }}>
          <Titlebar workspaceAvailable />
        </LayoutContext.Provider>
      </MemoryRouter>,
      'en-US'
    );
    expect(screen.getByRole('button', { name: /^Messages,/ })).toBeVisible();
  });
});
