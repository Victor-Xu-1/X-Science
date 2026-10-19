import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
vi.mock('@/renderer/services/FileService', () => ({ FileService: { processDroppedFiles: vi.fn() } }));
vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: async () => [],
  loadXScienceMcpServers: async () => [],
}));

afterEach(() => {
  cleanup();
  resetRendererAccountScopeForTest();
});

async function renderMenu(props: Partial<React.ComponentProps<typeof FileAttachButton>> = {}) {
  return renderWithI18n(<FileAttachButton openFileSelector={vi.fn()} {...props} />, 'en-US', { wrapper: MemoryRouter });
}

describe('composer plus keyboard ownership', () => {
  it('opens from ArrowDown and navigates root actions without entering disabled actions', async () => {
    await renderMenu({ onViewPlan: vi.fn(), onRequestReview: vi.fn(), reviewDisabled: true, onSaveSkill: vi.fn() });
    const trigger = screen.getByRole('button', { name: 'Add to message' });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    const menu = await screen.findByRole('menu', { name: 'Add to message' });
    const attach = within(menu).getByRole('menuitem', { name: 'Add file' });
    await waitFor(() => expect(attach).toHaveFocus());
    fireEvent.keyDown(attach, { key: 'End' });
    const save = within(menu).getByRole('menuitem', { name: 'Save as Skill' });
    expect(save).toHaveFocus();
    fireEvent.keyDown(save, { key: 'ArrowUp' });
    expect(within(menu).getByRole('menuitem', { name: 'View plan' })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Add to message' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });

  it('opens Skills with ArrowRight and returns focus without closing the root menu', async () => {
    const user = userEvent.setup();
    const onSelectSkill = vi.fn();
    await renderMenu({ loadedSkills: ['workflow'], onSelectSkill });
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    const trigger = screen.getByRole('menuitem', { name: 'Loaded Skills · 1' });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowRight' });
    const panel = await screen.findByRole('menu', { name: 'Loaded Skills' });
    const search = within(panel).getByRole('textbox');
    await waitFor(() => expect(search).toHaveFocus());
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    const skill = within(panel).getByRole('menuitemcheckbox', { name: 'workflow' });
    expect(skill).toHaveFocus();
    fireEvent.keyDown(skill, { key: 'ArrowLeft' });
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Loaded Skills' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
    expect(screen.getByRole('menu', { name: 'Add to message' })).toBeVisible();
    expect(onSelectSkill).not.toHaveBeenCalled();
    await user.keyboard(' ');
    await screen.findByRole('menu', { name: 'Loaded Skills' });
    await user.keyboard('{ArrowDown}{Enter}');
    expect(onSelectSkill).toHaveBeenCalledExactlyOnceWith('workflow');
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Add to message' })).not.toBeInTheDocument());
  });

  it('opens MCP with Enter, preserves search editing, and skips unavailable connectors', async () => {
    const user = userEvent.setup();
    const onSelectMcpServer = vi.fn();
    await renderMenu({
      loadedMcpStatuses: [
        { id: 'broken', name: 'Broken', status: 'failed' },
        { id: 'ready', name: 'Ready', status: 'loaded' },
      ],
      onSelectMcpServer,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    const trigger = screen.getByRole('menuitem', { name: 'Loaded MCP · 2' });
    trigger.focus();
    await user.keyboard('{Enter}');
    const panel = await screen.findByRole('menu', { name: 'Loaded MCP' });
    const search = within(panel).getByRole('textbox');
    await waitFor(() => expect(search).toHaveFocus());
    await user.keyboard('Rea');
    const home = new KeyboardEvent('keydown', { key: 'Home', bubbles: true, cancelable: true });
    expect(search.dispatchEvent(home)).toBe(true);
    expect(search).toHaveFocus();
    await user.keyboard('{Control>}a{/Control}{Backspace}');
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    expect(within(panel).getByRole('menuitemcheckbox', { name: 'Ready' })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Loaded MCP' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
    expect(onSelectMcpServer).not.toHaveBeenCalled();
  });

  it('opens the existing context window owner and passes the stable plus trigger for focus return', async () => {
    const onViewContextWindow = vi.fn();
    await renderMenu({ onViewContextWindow });
    const trigger = screen.getByRole('button', { name: 'Add to message' });
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Context usage' }));
    expect(onViewContextWindow).toHaveBeenCalledExactlyOnceWith(trigger);
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Add to message' })).not.toBeInTheDocument());
  });

  it('closes cached capability portals with the root and starts the next open without stale submenu state', async () => {
    await renderMenu({ loadedSkills: ['workflow'], onSelectSkill: vi.fn() });
    const trigger = screen.getByRole('button', { name: 'Add to message' });
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Loaded Skills · 1' }));
    await screen.findByRole('menu', { name: 'Loaded Skills' });
    fireEvent.click(trigger);
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Loaded Skills' })).not.toBeInTheDocument());
    fireEvent.click(trigger);
    expect(screen.getByRole('menu', { name: 'Add to message' })).toBeVisible();
    expect(screen.queryByRole('menu', { name: 'Loaded Skills' })).not.toBeInTheDocument();
  });

  it('disposes keyboard-open capability portals on the account boundary', async () => {
    act(() => setRendererAccountOwner('first-owner'));
    await renderMenu({ loadedSkills: ['workflow'], onSelectSkill: vi.fn() });
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    const skillTrigger = screen.getByRole('menuitem', { name: 'Loaded Skills · 1' });
    fireEvent.keyDown(skillTrigger, { key: 'ArrowRight' });
    await screen.findByRole('menu', { name: 'Loaded Skills' });
    act(() => setRendererAccountOwner('next-owner'));
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Loaded Skills' })).not.toBeInTheDocument());
    expect(screen.queryByRole('menu', { name: 'Add to message' })).not.toBeInTheDocument();
  });
});
