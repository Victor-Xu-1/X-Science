import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React, { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/renderer/pages/conversation/components/ChatLayout/WorkspacePanelHeader', () => ({
  default: () => <button>First control</button>,
}));
vi.mock('@/renderer/utils/workspace/workspaceEvents', () => ({ dispatchWorkspaceToggleEvent: vi.fn() }));
import MobileWorkspaceOverlay from '@/renderer/pages/conversation/components/ChatLayout/MobileWorkspaceOverlay';

function Fixture() {
  const [closed, setClosed] = useState(true);
  return (
    <>
      <button onClick={() => setClosed(false)}>Open files</button>
      <MobileWorkspaceOverlay
        rightSiderCollapsed={closed}
        setRightSiderCollapsed={setClosed}
        workspaceWidthPx={320}
        mobileWorkspaceHandleRight={320}
        sider={<button>Last control</button>}
      />
    </>
  );
}
describe('Mobile workspace drawer semantics and focus', () => {
  afterEach(cleanup);
  it('keeps a closed drawer inert without mounting another main landmark', () => {
    render(<Fixture />);
    const drawer = screen.getByText('First control').closest('.chat-layout-right-sider');
    expect(drawer).toHaveAttribute('inert');
    expect(drawer).toHaveAttribute('aria-hidden', 'true');
    expect(screen.queryByRole('main')).not.toBeInTheDocument();
  });
  it('names the open drawer, contains Tab and returns to its opener on Escape without losing contents', () => {
    render(<Fixture />);
    const opener = screen.getByRole('button', { name: 'Open files' });
    opener.focus();
    fireEvent.click(opener);
    expect(screen.getByRole('dialog', { name: 'common.workspace' })).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByRole('button', { name: 'First control' })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'Tab', shiftKey: true });
    expect(screen.getByRole('button', { name: 'Last control' })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'Tab' });
    expect(screen.getByRole('button', { name: 'First control' })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(opener).toHaveFocus();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText('Last control')).toBeInTheDocument();
  });
});
