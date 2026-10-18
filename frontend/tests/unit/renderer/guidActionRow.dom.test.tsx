import GuidActionRow from '@/renderer/pages/guid/components/GuidActionRow';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import React from 'react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n, type TestLanguage } from '../i18nTestUtils';

afterEach(cleanup);

describe.each([
  { branch: 'single action', sendOptionsNode: undefined },
  { branch: 'joined actions', sendOptionsNode: <button>Send options</button> },
])('GuidActionRow $branch send control', ({ sendOptionsNode }) => {
  describe.each([
    { language: 'en-US' as TestLanguage, label: 'Send' },
    { language: 'zh-CN' as TestLanguage, label: '发送' },
  ])('$language', ({ language, label }) => {
    const renderSendAction = async (state: { disabled?: boolean; loading?: boolean } = {}) => {
      const onSend = vi.fn();
      await renderWithI18n(
        <GuidActionRow
          files={[]}
          onFilesUploaded={vi.fn()}
          openFileSelector={vi.fn()}
          onSelectProjectFiles={vi.fn()}
          modelSelectorNode={null}
          loading={state.loading ?? false}
          isButtonDisabled={state.disabled ?? false}
          sendOptionsNode={sendOptionsNode}
          onSend={onSend}
        />,
        language,
        { wrapper: MemoryRouter }
      );
      return { onSend, button: screen.getByRole('button', { name: label, exact: true }) };
    };

    it('names the enabled icon control and submits once for one click', async () => {
      const { onSend, button } = await renderSendAction();
      expect(button).toBeEnabled();
      fireEvent.click(button);
      expect(onSend).toHaveBeenCalledTimes(1);
    });

    it('retains its name while disabled and prevents submission', async () => {
      const { onSend, button } = await renderSendAction({ disabled: true });
      expect(button).toBeDisabled();
      fireEvent.click(button);
      expect(onSend).not.toHaveBeenCalled();
    });

    it('retains its name while loading and prevents another submission', async () => {
      const { onSend, button } = await renderSendAction({ loading: true });
      expect(button).toHaveClass('arco-btn-loading');
      fireEvent.click(button);
      expect(onSend).not.toHaveBeenCalled();
    });
  });
});

describe('GuidActionRow attachment actions', () => {
  it('matches the formal conversation menu structure while keeping unavailable actions disabled', async () => {
    const openFileSelector = vi.fn();
    const onSelectProjectFiles = vi.fn();
    const onSelectSkill = vi.fn();
    const onSelectMcpServer = vi.fn();
    const onSend = vi.fn();
    await renderWithI18n(
      <GuidActionRow
        files={[]}
        onFilesUploaded={vi.fn()}
        openFileSelector={openFileSelector}
        onSelectProjectFiles={onSelectProjectFiles}
        loadedSkills={['autodock-vina']}
        loadedMcpStatuses={[{ id: 'bundled:pubmed', name: 'PubMed', status: 'loaded' }]}
        selectedSkillNames={[]}
        selectedMcpServerIds={[]}
        onSelectSkill={onSelectSkill}
        onSelectMcpServer={onSelectMcpServer}
        modelSelectorNode={<span>model</span>}
        loading={false}
        isButtonDisabled
        onSend={onSend}
      />,
      'en-US',
      { wrapper: MemoryRouter }
    );

    const attachTrigger = screen.getByRole('button', { name: 'Add to message' });
    expect(attachTrigger).toHaveClass('composer-icon-control');
    fireEvent.click(attachTrigger);

    const menu = screen.getByRole('menu', { name: 'Add to message' });
    expect(menu).toHaveClass('app-overlay-menu', 'composer-control-menu');
    expect(menu).toHaveStyle({ width: 'min(210px, calc(100vw - 24px))' });
    expect(screen.getByRole('menuitem', { name: 'Loaded Skills · 1' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Loaded MCP · 1' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Add file' })).toBeInTheDocument();
    const projectFilesItem = screen.getByRole('menuitem', { name: 'Your files' });
    expect(projectFilesItem).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'View plan' })).toBeDisabled();
    expect(screen.getByRole('menuitem', { name: 'Review findings' })).toBeDisabled();
    const saveSkillItem = screen.getByRole('menuitem', { name: 'Save as Skill' });
    // Unavailable Skill actions stay keyboard-focusable for their explanatory
    // tooltip; aria-disabled and the shared click guard own non-activation.
    expect(saveSkillItem).toHaveAttribute('aria-disabled', 'true');
    expect(saveSkillItem).toHaveAttribute('tabindex', '0');
    fireEvent.click(saveSkillItem);
    saveSkillItem.focus();
    await userEvent.setup().keyboard('{Enter} ');
    expect(menu).toBeInTheDocument();
    expect(onSend).not.toHaveBeenCalled();
    expect(screen.queryByText('Upload from device')).not.toBeInTheDocument();

    fireEvent.click(projectFilesItem);
    expect(onSelectProjectFiles).toHaveBeenCalledTimes(1);
    expect(openFileSelector).not.toHaveBeenCalled();
  });
});
