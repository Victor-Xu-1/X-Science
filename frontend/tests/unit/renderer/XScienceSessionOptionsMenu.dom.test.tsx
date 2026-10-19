import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import type { XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({
  loadAssistants: vi.fn(),
  loadProviders: vi.fn(),
  loadSessionProviders: vi.fn(),
  setSessionProvider: vi.fn(),
  persistOption: vi.fn(),
  persistPolicy: vi.fn(),
  prepareSpecialist: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock('@/renderer/services/xScienceCatalog', () => ({
  loadXScienceAssistants: mocks.loadAssistants,
}));

vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: mocks.loadProviders,
  loadXScienceSessionComputeProviders: mocks.loadSessionProviders,
  setXScienceSessionComputeProvider: mocks.setSessionProvider,
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: { ...actual.Message, success: mocks.success, error: mocks.error },
  };
});

const initialValue: XScienceSessionOptions = {
  delegation: false,
  autoReview: false,
  memory: false,
  targetAgent: 'OPERON',
  goalText: null,
  asRoutine: false,
};

const Harness = () => {
  const { i18n } = useTranslation();
  const [value, setValue] = useState(initialValue);
  const [delegation, setDelegation] = useState(true);
  const persistOption = async (key: 'autoReview' | 'memory', checked: boolean) => {
    await mocks.persistOption(key, checked);
    setValue((current) => ({ ...current, [key]: checked }));
  };
  return (
    <MemoryRouter>
      <>
        <XScienceSessionOptionsMenu
          rootFrameId='frame-stat6'
          value={value}
          onChange={setValue}
          onPersistentOptionChange={persistOption}
          specialistControl={{
            applied: {
              agent_name: 'DEFAULT_RUNTIME',
              selection_name: 'DEFAULT_LOGICAL',
              assistant_id: 'default',
              display_name: 'Main',
              is_default: true,
              snapshot_id: '',
              revision: 0,
            },
            desired: null,
            choices: [
              {
                id: 'expert',
                name: 'AIDD_EXPERT',
                label: i18n.language === 'en-US' ? 'AI Drug Discovery Expert' : 'AI 药物研发专家',
              },
            ],
            pending: false,
            available: true,
            unavailable: false,
            error: null,
            retryable: false,
            catalogLoading: false,
            catalogError: false,
            hasLiveAttempt: false,
            prepared: false,
            change: mocks.prepareSpecialist,
            refresh: () => {},
            retry: () => {},
            current: () => true,
            capture: () => ({}),
          }}
          delegationControl={{
            enabled: delegation,
            pending: false,
            available: true,
            hasLiveAttempts: false,
            error: null,
            retryable: false,
            change: async (enabled) => {
              await mocks.persistPolicy(enabled);
              setDelegation(enabled);
            },
            retry: () => {},
            refresh: () => {},
            current: () => true,
          }}
          memoryControl={{
            availability: { status: 'ready', enabled: true },
            refresh: () => undefined,
            current: () => true,
            canUpdate: () => true,
          }}
        />
        <output data-testid='session-value'>{JSON.stringify(value)}</output>
      </>
    </MemoryRouter>
  );
};

describe('XScienceSessionOptionsMenu component', () => {
  beforeEach(() => {
    mocks.loadAssistants.mockResolvedValue([
      { id: 'xscience:operon', name: 'OPERON', agent_id: 'OPERON' },
      {
        id: 'xscience:aidd-expert',
        name: 'AI Drug Discovery Expert',
        name_i18n: {
          'en-US': 'AI Drug Discovery Expert',
          'zh-CN': 'AI 药物研发专家',
        },
        agent_id: 'AIDD_EXPERT',
      },
    ]);
    mocks.loadProviders.mockResolvedValue([
      { name: 'local', displayName: 'Local', family: 'local', checked: true },
      { name: 'ssh:hpc-a', displayName: 'HPC A', family: 'ssh', checked: true },
    ]);
    mocks.loadSessionProviders.mockResolvedValue(['local']);
    mocks.setSessionProvider.mockResolvedValue(undefined);
    mocks.persistOption.mockResolvedValue(undefined);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders every v1.1 session control in the original order', async () => {
    await renderWithI18n(<Harness />);
    const trigger = screen.getByRole('button', { name: '会话选项' });
    expect(trigger).toHaveClass('composer-icon-control');
    fireEvent.click(trigger);

    const menu = await screen.findByRole('menu', { name: '会话选项' });
    expect(menu).toHaveClass('app-overlay-menu', 'composer-control-menu');
    expect(menu).toHaveStyle({
      maxWidth: 'calc(100vw - 24px)',
      maxHeight: 'calc(100vh - 24px)',
    });
    expect(menu).toHaveTextContent('自动审阅记忆委派专家无计算');
    expect(screen.queryByTestId('session-config-row-control-center')).not.toBeInTheDocument();
    expect(screen.queryByText('对话能力中枢')).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: '手动审阅' })).not.toBeInTheDocument();
    expect(screen.getByRole('menuitemcheckbox', { name: '委派' })).toBeChecked();
    const autoReviewOption = screen.getByRole('menuitemcheckbox', { name: '自动审阅' });
    expect(autoReviewOption).not.toBeChecked();
    expect(autoReviewOption).toHaveClass('session-options-menu__row');
    expect(screen.getByRole('menuitemcheckbox', { name: '记忆' })).not.toBeChecked();

    expect(screen.queryByText('AI 药物研发专家')).not.toBeInTheDocument();
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-specialist'));
    const specialistOption = await screen.findByText('AI 药物研发专家');
    expect(specialistOption.closest('[role="menu"]')).toHaveClass('app-overlay-menu', 'composer-control-submenu');
    expect(specialistOption.closest('[role="menuitemradio"]')).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('menuitem', { name: '新建专家...' })).toBeInTheDocument();
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-compute'));
    expect(
      (await screen.findByText('HPC A')).parentElement?.querySelector('[role="menuitemcheckbox"]')
    ).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('menuitem', { name: '管理计算资源...' })).toBeInTheDocument();
  });

  it('uses the authoritative delegation control and persists reviewer and memory changes before committing UI state', async () => {
    await renderWithI18n(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));

    fireEvent.click(await screen.findByRole('menuitemcheckbox', { name: '委派' }));
    await waitFor(() => expect(mocks.persistPolicy).toHaveBeenCalledWith(false));
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox', { name: '委派' })).not.toBeChecked());
    expect(screen.getByTestId('session-value')).toHaveTextContent('"delegation":false');

    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: '自动审阅' }));
    await waitFor(() => expect(mocks.persistOption).toHaveBeenCalledWith('autoReview', true));
    expect(screen.getByTestId('session-value')).toHaveTextContent('"autoReview":true');

    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: '记忆' }));
    await waitFor(() => expect(mocks.persistOption).toHaveBeenCalledWith('memory', true));
    expect(screen.getByTestId('session-value')).toHaveTextContent('"memory":true');
  });

  it('keeps the previous option value when persistence fails', async () => {
    mocks.persistOption.mockRejectedValueOnce(new Error('frame is archived'));
    await renderWithI18n(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));

    fireEvent.click(await screen.findByRole('menuitemcheckbox', { name: '自动审阅' }));
    await waitFor(() => expect(mocks.error).toHaveBeenCalledWith('会话选项更新失败。'));
    expect(screen.getByTestId('session-value')).toHaveTextContent('"autoReview":false');
  });

  it('keeps manual review out of session settings', async () => {
    await renderWithI18n(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));
    await screen.findByRole('menu', { name: '会话选项' });
    expect(screen.queryByRole('menuitem', { name: '手动审阅' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('session-config-request-review')).not.toBeInTheDocument();
  });

  it('keeps task recovery out of session settings because the task status center owns it', async () => {
    await renderWithI18n(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));

    await screen.findByRole('menu', { name: '会话选项' });
    expect(screen.queryByRole('menuitem', { name: '继续任务' })).not.toBeInTheDocument();
  });

  it('prepares a specialist through its owner and changes a compute provider through its service', async () => {
    await renderWithI18n(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));

    fireEvent.mouseEnter(screen.getByTestId('session-config-row-specialist'));
    fireEvent.click((await screen.findByText('AI 药物研发专家')).closest('[role="menuitemradio"]')!);
    expect(mocks.prepareSpecialist).toHaveBeenCalledWith('AIDD_EXPERT');
    expect(screen.getByTestId('session-value')).toHaveTextContent('"targetAgent":"OPERON"');

    fireEvent.mouseEnter(screen.getByTestId('session-config-row-compute'));
    const computeToggle = (await screen.findByText('HPC A')).parentElement?.querySelector('[role="menuitemcheckbox"]');
    if (!computeToggle) throw new Error('compute provider toggle is unavailable');
    fireEvent.click(computeToggle);
    await waitFor(() => expect(mocks.setSessionProvider).toHaveBeenCalledWith('frame-stat6', 'ssh:hpc-a', true));
  });

  it('keeps the expert submenu open while the pointer crosses from the parent menu', async () => {
    await renderWithI18n(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));

    const parentMenu = await screen.findByRole('menu', { name: '会话选项' });
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-specialist'));
    const expertMenu = await screen.findByRole('menu', { name: '专家' });

    fireEvent.mouseLeave(parentMenu);
    fireEvent.mouseEnter(expertMenu);

    await act(async () => new Promise((resolve) => window.setTimeout(resolve, 180)));
    expect(screen.getByRole('menu', { name: '专家' })).toBeInTheDocument();

    fireEvent.mouseLeave(expertMenu);
    await waitFor(() => expect(screen.queryByRole('menu', { name: '专家' })).not.toBeInTheDocument());
  });

  it('shows a truthful empty state when no compute provider is enabled', async () => {
    mocks.loadProviders.mockResolvedValue([]);
    mocks.loadSessionProviders.mockResolvedValue([]);
    await renderWithI18n(<Harness />);

    fireEvent.click(screen.getByRole('button', { name: '会话选项' }));
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-compute'));
    expect(await screen.findByText('尚未配置额外计算资源；任务仍可使用本机')).toBeInTheDocument();
    expect(screen.getByRole('menuitemradio', { name: '本机' })).toHaveAttribute('aria-checked', 'true');
  });

  it('renders session controls in English', async () => {
    await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));

    const menu = await screen.findByRole('menu', { name: 'Conversation options' });
    expect(menu).toHaveTextContent('Auto reviewMemoryDelegationExpertNoneComputeLocal');
    expect(screen.getByRole('menuitemcheckbox', { name: 'Delegation' })).toBeInTheDocument();
    fireEvent.mouseEnter(screen.getByTestId('session-config-row-specialist'));
    expect(await screen.findByText('AI Drug Discovery Expert')).toBeInTheDocument();
  });

  it('opens with arrows, navigates the menu and submenus, and restores focus on Escape', async () => {
    await renderWithI18n(<Harness />);
    const trigger = screen.getByRole('button', { name: '会话选项' });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });
    const menu = await screen.findByRole('menu', { name: '会话选项' });
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox', { name: '自动审阅' })).toHaveFocus());
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(screen.getByRole('menuitemcheckbox', { name: '记忆' })).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'End' });
    const compute = screen.getByTestId('session-config-row-compute');
    expect(compute).toHaveFocus();
    fireEvent.keyDown(compute, { key: 'ArrowRight' });
    const submenu = await screen.findByRole('menu', { name: '计算' });
    await waitFor(() => expect(screen.getByRole('menuitemradio', { name: '本机' })).toHaveFocus());
    fireEvent.keyDown(submenu, { key: 'Escape' });
    expect(compute).toHaveFocus();
    expect(compute).toHaveAttribute('aria-expanded', 'false');
    fireEvent.keyDown(menu, { key: 'Escape' });
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
  });

  it('focuses the menu container after pointer opening without selecting the first option', async () => {
    const user = userEvent.setup();
    await renderWithI18n(<Harness />);
    await user.click(screen.getByRole('button', { name: '会话选项' }));

    const menu = screen.getByRole('menu', { name: '会话选项' });
    await waitFor(() => expect(menu).toHaveFocus());
    expect(menu).toHaveAttribute('tabindex', '-1');
    expect(screen.getByRole('menuitemcheckbox', { name: '自动审阅' })).not.toHaveFocus();
  });

  it('closes with Escape after a pointer click on the session-options trigger', async () => {
    const user = userEvent.setup();
    await renderWithI18n(<Harness />);
    const trigger = screen.getByRole('button', { name: '会话选项' });
    await user.click(trigger);
    expect(trigger).toHaveAttribute('aria-expanded', 'true');

    await user.keyboard('{Escape}');

    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    await waitFor(() => expect(screen.queryByRole('menu', { name: '会话选项' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });

  it('preserves outside-click focus and focuses the session-options menu on reopening', async () => {
    const user = userEvent.setup();
    await renderWithI18n(
      <>
        <Harness />
        <button type='button'>Outside action</button>
      </>
    );
    const trigger = screen.getByRole('button', { name: '会话选项' });
    const outside = screen.getByRole('button', { name: 'Outside action' });
    await user.click(trigger);
    await user.click(outside);

    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    expect(outside).toHaveFocus();
    await user.click(trigger);
    await waitFor(() => expect(screen.getByRole('menu', { name: '会话选项' })).toHaveFocus());
  });

  it('focuses an option when ArrowDown reopens the session-options menu', async () => {
    const user = userEvent.setup();
    await renderWithI18n(<Harness />);
    const trigger = screen.getByRole('button', { name: '会话选项' });
    await user.click(trigger);
    await waitFor(() => expect(screen.getByRole('menu', { name: '会话选项' })).toHaveFocus());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('menu', { name: '会话选项' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();

    await user.keyboard('{ArrowDown}');

    await waitFor(() => expect(screen.getByRole('menuitemcheckbox', { name: '自动审阅' })).toHaveFocus());
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
  });

  it('does not reclaim submenu focus when an asynchronous catalog finishes', async () => {
    let finishCatalog!: (providers: unknown[]) => void;
    mocks.loadProviders.mockReturnValueOnce(
      new Promise((resolve) => {
        finishCatalog = resolve;
      })
    );
    const user = userEvent.setup();
    await renderWithI18n(<Harness />);
    await user.click(screen.getByRole('button', { name: '会话选项' }));
    const expertTrigger = screen.getByTestId('session-config-row-specialist');
    expertTrigger.focus();
    fireEvent.keyDown(expertTrigger, { key: 'ArrowRight' });
    const submenu = await screen.findByRole('menu', { name: '专家' });
    const choice = submenu.querySelector<HTMLElement>('[role="menuitemradio"]')!;
    await waitFor(() => expect(choice).toHaveFocus());

    await act(async () => {
      finishCatalog([]);
    });

    expect(choice).toHaveFocus();
    expect(screen.getByRole('menu', { name: '会话选项' })).not.toHaveFocus();
  });
});
