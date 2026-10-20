import FileAttachButton from '@/renderer/components/media/FileAttachButton';
import { focusMenuSurface, focusMenuSurfaceWhenVisible } from '@/renderer/utils/menuKeyboard';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const processDroppedFilesMock = vi.fn();

vi.mock('@/renderer/utils/platform', () => ({ isElectronDesktop: () => false }));
vi.mock('@/renderer/services/FileService', () => ({
  FileService: { processDroppedFiles: (...args: unknown[]) => processDroppedFilesMock(...args) },
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  processDroppedFilesMock.mockReset();
});

function renderMenu(
  options: {
    onViewPlan?: () => void;
    onRequestReview?: () => void;
    onSaveSkill?: () => void;
  } = {},
  language: 'zh-CN' | 'en-US' = 'zh-CN'
) {
  return renderWithI18n(
    <FileAttachButton openFileSelector={vi.fn()} onLocalFilesAdded={vi.fn()} {...options} />,
    language,
    { wrapper: MemoryRouter }
  );
}

describe('v1.1 add-to-message menu', () => {
  it('C419 return focus hands View Plan the surviving Plus opener rather than its temporary row', async () => {
    let handedFocus: Element | null = null;
    const onViewPlan = vi.fn(() => {
      handedFocus = document.activeElement;
    });
    const user = userEvent.setup();
    await renderMenu({ onViewPlan }, 'en-US');
    await user.click(screen.getByRole('button', { name: 'Add to message' }));
    const item = screen.getByRole('menuitem', { name: 'View plan' });
    await waitFor(() => expect(getComputedStyle(item).pointerEvents).not.toBe('none'));
    item.focus();
    await user.click(item);
    expect(onViewPlan).toHaveBeenCalledTimes(1);
    expect(handedFocus).toBe(screen.getByRole('button', { name: 'Add to message' }));
    expect(screen.getByRole('button', { name: 'Add to message' })).toHaveAttribute('aria-expanded', 'false');
  });

  it('hides unavailable plan, review, and save-skill operations instead of presenting no-op actions', async () => {
    await renderMenu();
    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    const menu = screen.getByRole('menu', { name: '添加到消息' });
    expect(menu).toHaveStyle({ width: 'min(224px, calc(100vw - 24px))' });
    expect(menu).toHaveClass('conversation-control-menu');
    expect(screen.getByText('添加文件')).toBeInTheDocument();
    expect(screen.getByText('你的文件')).toBeInTheDocument();
    expect(screen.queryByText('查看计划')).not.toBeInTheDocument();
    expect(screen.queryByText('审阅发现问题')).not.toBeInTheDocument();
    expect(screen.queryByText('保存为 Skill')).not.toBeInTheDocument();
  });

  it('keeps Project files routed to the project artifact selector instead of the native file picker', async () => {
    const openFileSelector = vi.fn();
    const onSelectProjectFiles = vi.fn();
    await renderWithI18n(
      <FileAttachButton
        openFileSelector={openFileSelector}
        onSelectProjectFiles={onSelectProjectFiles}
        onLocalFilesAdded={vi.fn()}
      />,
      'en-US',
      { wrapper: MemoryRouter }
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Project files' }));

    expect(onSelectProjectFiles).toHaveBeenCalledTimes(1);
    expect(openFileSelector).not.toHaveBeenCalled();
  });

  it('opens the browser file chooser and uploads local files into the current draft', async () => {
    const openFileSelector = vi.fn();
    const onLocalFilesAdded = vi.fn();
    processDroppedFilesMock.mockResolvedValueOnce([
      { name: 'results.csv', path: '/uploads/results.csv', size: 8, type: 'text/csv', lastModified: 1 },
    ]);
    await renderWithI18n(
      <FileAttachButton openFileSelector={openFileSelector} onLocalFilesAdded={onLocalFilesAdded} />,
      'zh-CN',
      { wrapper: MemoryRouter }
    );

    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '添加文件' }));

    const file = new File(['a,b\n1,2\n'], 'results.csv', { type: 'text/csv' });
    fireEvent.change(screen.getByTestId('conversation-file-upload-input'), { target: { files: [file] } });
    await waitFor(() =>
      expect(onLocalFilesAdded).toHaveBeenCalledWith([expect.objectContaining({ path: '/uploads/results.csv' })])
    );
    expect(openFileSelector).not.toHaveBeenCalled();
  });

  it('keeps browser-selected files in the new-message draft instead of calling the host upload path', async () => {
    const openFileSelector = vi.fn();
    const onLocalFilesSelected = vi.fn();
    await renderWithI18n(
      <FileAttachButton openFileSelector={openFileSelector} onLocalFilesSelected={onLocalFilesSelected} />,
      'zh-CN',
      { wrapper: MemoryRouter }
    );

    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '添加文件' }));

    const file = new File(['name,value\nexample,1\n'], 'results.csv', { type: 'text/csv' });
    fireEvent.change(screen.getByTestId('conversation-file-upload-input'), {
      target: { files: [file] },
    });

    await waitFor(() => expect(onLocalFilesSelected).toHaveBeenCalledWith([file]));
    await waitFor(() => expect(screen.getByRole('button', { name: '添加到消息' })).toBeEnabled());
    expect(openFileSelector).not.toHaveBeenCalled();
  });

  it('shows each operation only when a real handler is supplied', async () => {
    const onViewPlan = vi.fn();
    const onRequestReview = vi.fn();
    const onSaveSkill = vi.fn();
    await renderWithI18n(
      <FileAttachButton
        openFileSelector={vi.fn()}
        onLocalFilesAdded={vi.fn()}
        onViewPlan={onViewPlan}
        planProgress={{ completed: 2, total: 3 }}
        onRequestReview={onRequestReview}
        reviewLabelMode='manual'
        onSaveSkill={onSaveSkill}
      />,
      'zh-CN',
      { wrapper: MemoryRouter }
    );
    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    const planItem = screen.getByRole('menuitem', { name: '查看计划 2/3' });
    const reviewItem = screen.getByRole('menuitem', { name: '手动审阅' });
    expect(planItem).not.toHaveAttribute('aria-checked');
    expect(planItem).toHaveTextContent('2/3');
    expect(reviewItem).not.toHaveAttribute('aria-checked');
    expect(screen.queryByText('检查当前会话')).not.toBeInTheDocument();
    const reviewTooltipTrigger = reviewItem.parentElement;
    expect(reviewTooltipTrigger).not.toBeNull();
    fireEvent.mouseEnter(reviewTooltipTrigger!);
    await waitFor(() => expect(screen.getByText('检查当前会话')).toBeInTheDocument());
    fireEvent.mouseLeave(reviewTooltipTrigger!);
    await waitFor(() => expect(screen.queryByText('检查当前会话')).not.toBeInTheDocument());
    fireEvent.click(planItem);
    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '手动审阅' }));
    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    fireEvent.click(screen.getByRole('menuitem', { name: '保存为 Skill' }));
    expect(onViewPlan).toHaveBeenCalledTimes(1);
    expect(onRequestReview).toHaveBeenCalledTimes(1);
    expect(onSaveSkill).toHaveBeenCalledTimes(1);
  });

  it('adds loaded Skills and MCP connectors as typed turn context instead of plain text', async () => {
    const onSelectSkill = vi.fn();
    const onSelectMcpServer = vi.fn();
    await renderWithI18n(
      <FileAttachButton
        openFileSelector={vi.fn()}
        onLocalFilesAdded={vi.fn()}
        loadedSkills={['literature-review']}
        loadedMcpStatuses={[
          { id: 'pubmed', name: 'PubMed', status: 'loaded' },
          { id: 'broken', name: 'Broken MCP', status: 'failed', reason: 'unavailable' },
        ]}
        selectedSkillNames={[]}
        selectedMcpServerIds={[]}
        onSelectSkill={onSelectSkill}
        onSelectMcpServer={onSelectMcpServer}
      />,
      'en-US',
      { wrapper: MemoryRouter }
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    fireEvent.mouseEnter(screen.getByText('Loaded Skills · 1'));
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox', { name: 'literature-review' })).toBeVisible());
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'literature-review' }));
    expect(onSelectSkill).toHaveBeenCalledWith('literature-review');

    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));
    fireEvent.mouseEnter(screen.getByText('Loaded MCP · 2'));
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox', { name: 'PubMed' })).toBeVisible());
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'PubMed' }));
    expect(onSelectMcpServer).toHaveBeenCalledWith(expect.objectContaining({ id: 'pubmed' }));
    expect(screen.getByRole('menuitem', { name: 'Broken MCP' })).toBeDisabled();
  });

  it('closes with Escape and restores focus to the v1.1 trigger', async () => {
    const user = userEvent.setup();
    await renderMenu({ onViewPlan: vi.fn() });
    const trigger = screen.getByRole('button', { name: '添加到消息' });
    fireEvent.click(trigger);
    screen.getByRole('menuitem', { name: '查看计划' }).focus();
    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('menu', { name: '添加到消息' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });

  it('focuses the menu container after pointer opening without selecting the first action', async () => {
    const user = userEvent.setup();
    await renderMenu();
    await user.click(screen.getByRole('button', { name: '添加到消息' }));

    const menu = screen.getByRole('menu', { name: '添加到消息' });
    await waitFor(() => expect(menu).toHaveFocus());
    expect(menu).toHaveAttribute('tabindex', '-1');
    expect(screen.getByRole('menuitem', { name: '添加文件' })).not.toHaveFocus();
  });

  it('closes with Escape after a pointer click and restores the original trigger focus', async () => {
    const user = userEvent.setup();
    await renderMenu();
    const trigger = screen.getByRole('button', { name: '添加到消息' });
    await user.click(trigger);
    expect(trigger).toHaveAttribute('aria-expanded', 'true');

    await user.keyboard('{Escape}');

    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    await waitFor(() => expect(screen.queryByRole('menu', { name: '添加到消息' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });

  it('preserves outside-click focus and focuses the cached menu on the next pointer opening', async () => {
    const user = userEvent.setup();
    await renderWithI18n(
      <>
        <FileAttachButton openFileSelector={vi.fn()} />
        <button type='button'>Outside action</button>
      </>,
      'en-US',
      { wrapper: MemoryRouter }
    );
    const trigger = screen.getByRole('button', { name: 'Add to message' });
    const outside = screen.getByRole('button', { name: 'Outside action' });
    await user.click(trigger);
    await user.click(outside);

    await waitFor(() => expect(trigger).toHaveAttribute('aria-expanded', 'false'));
    expect(outside).toHaveFocus();
    await user.click(trigger);
    await waitFor(() => expect(screen.getByRole('menu', { name: 'Add to message' })).toHaveFocus());
  });

  it.each(['ArrowDown', 'ArrowUp'])('focuses an action when %s reopens the cached plus menu', async (key) => {
    // jsdom focuses display:none descendants, unlike a browser. Keep the real
    // Arco wrapper and its lifecycle; adapt only this missing platform rule.
    const domFocus = HTMLElement.prototype.focus;
    vi.spyOn(HTMLElement.prototype, 'focus').mockImplementation(function (this: HTMLElement, options) {
      if (getComputedStyle(this).display === 'none') return;
      for (let node = this.parentElement; node; node = node.parentElement) {
        if (getComputedStyle(node).display === 'none') return;
      }
      domFocus.call(this, options);
    });
    const user = userEvent.setup();
    await renderMenu({ onViewPlan: vi.fn() }, 'en-US');
    const trigger = screen.getByRole('button', { name: 'Add to message' });
    await user.click(trigger);
    await waitFor(() => expect(screen.getByRole('menu', { name: 'Add to message' })).toHaveFocus());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('menu', { name: 'Add to message' })).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();

    await user.keyboard(`{${key}}`);

    const target = screen.getByRole('menuitem', { name: key === 'ArrowUp' ? 'View plan' : 'Add file' });
    await waitFor(() => expect(target).toHaveFocus());
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
  });

  it('reports focus success only when the requested menu target actually receives it', async () => {
    await renderWithI18n(
      <div role='menu' aria-label='Non-focusable action'>
        <span role='menuitem'>Read-only label</span>
      </div>
    );
    expect(focusMenuSurface(screen.getByRole('menu'), 'first')).toBe(false);
  });

  it.each(['close', 'user movement'])(
    'cancels pending menu focus after %s before a late visible change',
    async (reason) => {
      await renderWithI18n(
        <>
          <button type='button'>Focus opener</button>
          <div style={{ display: 'none' }} data-testid='cached-popup'>
            <div role='menu' tabIndex={-1} data-testid='delayed-menu'>
              <button type='button' role='menuitem'>
                First action
              </button>
            </div>
          </div>
          <button type='button'>Outside focus</button>
        </>
      );
      const opener = screen.getByRole('button', { name: 'Focus opener' });
      const outside = screen.getByRole('button', { name: 'Outside focus' });
      const onFocused = vi.fn();
      opener.focus();
      const cancel = focusMenuSurfaceWhenVisible(screen.getByTestId('delayed-menu'), 'first', opener, onFocused);
      if (reason === 'close') cancel();
      else outside.focus();

      await act(async () => {
        screen.getByTestId('cached-popup').style.display = 'block';
      });

      expect(reason === 'close' ? opener : outside).toHaveFocus();
      expect(onFocused).not.toHaveBeenCalled();
      cancel();
    }
  );

  it('keeps only the current opening focus and disconnects before later submenu activity', async () => {
    await renderWithI18n(
      <>
        <button type='button'>Focus opener</button>
        <div style={{ display: 'none' }} data-testid='cached-popup'>
          <div role='menu' tabIndex={-1} data-testid='delayed-menu'>
            <button type='button' role='menuitem'>
              First action
            </button>
            <button type='button' role='menuitem'>
              Last action
            </button>
            <input aria-label='Submenu search' />
          </div>
        </div>
      </>
    );
    const opener = screen.getByRole('button', { name: 'Focus opener' });
    const menu = screen.getByTestId('delayed-menu');
    const staleFocused = vi.fn();
    const currentFocused = vi.fn();
    opener.focus();
    focusMenuSurfaceWhenVisible(menu, 'first', opener, staleFocused)();
    const cancel = focusMenuSurfaceWhenVisible(menu, 'last', opener, currentFocused);
    await act(async () => {
      screen.getByTestId('cached-popup').style.display = 'block';
    });
    expect(screen.getByRole('menuitem', { name: 'Last action' })).toHaveFocus();
    expect(staleFocused).not.toHaveBeenCalled();
    expect(currentFocused).toHaveBeenCalledTimes(1);

    const search = screen.getByRole('textbox', { name: 'Submenu search' });
    search.focus();
    await act(async () => {
      screen.getByTestId('cached-popup').style.opacity = '1';
    });
    expect(search).toHaveFocus();
    expect(currentFocused).toHaveBeenCalledTimes(1);
    cancel();
  });

  it('renders the complete menu in English', async () => {
    await renderMenu({ onViewPlan: vi.fn(), onRequestReview: vi.fn(), onSaveSkill: vi.fn() }, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Add to message' }));

    expect(screen.getByRole('menu', { name: 'Add to message' })).toBeInTheDocument();
    expect(screen.getByText('Your files')).toBeInTheDocument();
    expect(screen.getByText('Review findings')).toBeInTheDocument();
    expect(screen.queryByText('Manual review')).not.toBeInTheDocument();
  });

  it('does not append a manual-review status label to the review action', async () => {
    await renderMenu({ onRequestReview: vi.fn() });
    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));

    expect(screen.getByRole('menuitem', { name: '审阅发现问题' })).toBeInTheDocument();
    expect(screen.queryByText('手动审阅')).not.toBeInTheDocument();
  });

  it('shows authoritative in-flight states and prevents duplicate review or skill actions', async () => {
    const onRequestReview = vi.fn();
    const onSaveSkill = vi.fn();
    await renderWithI18n(
      <FileAttachButton
        openFileSelector={vi.fn()}
        onLocalFilesAdded={vi.fn()}
        onRequestReview={onRequestReview}
        reviewLabelMode='manual'
        reviewInFlight
        onSaveSkill={onSaveSkill}
        saveAsSkillDisabled
      />,
      'zh-CN',
      { wrapper: MemoryRouter }
    );

    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    const reviewItem = screen.getByRole('menuitem', { name: '审阅中...' });
    const saveSkillItem = screen.getByRole('menuitem', { name: '保存为 Skill' });
    expect(reviewItem).toBeDisabled();
    expect(saveSkillItem).toHaveAttribute('aria-disabled', 'true');
    expect(screen.queryByText('专家正在审阅')).not.toBeInTheDocument();
    expect(screen.queryByText('任务空闲后可用')).not.toBeInTheDocument();
    fireEvent.click(reviewItem);
    fireEvent.click(saveSkillItem);
    expect(onRequestReview).not.toHaveBeenCalled();
    expect(onSaveSkill).not.toHaveBeenCalled();
  });

  it('labels the actual Skill task as running instead of waiting for an idle task', async () => {
    const onSaveSkill = vi.fn();
    await renderWithI18n(
      <FileAttachButton openFileSelector={vi.fn()} onSaveSkill={onSaveSkill} saveAsSkillDisabled saveAsSkillInFlight />,
      'zh-CN',
      { wrapper: MemoryRouter }
    );
    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    const item = screen.getByRole('menuitem', { name: '正在保存为 Skill…' });
    expect(item).toHaveAttribute('aria-disabled', 'true');
    expect(item).toHaveAttribute('aria-busy', 'true');
    expect(item.querySelector('.conversation-control-menu__spinner')).toHaveAttribute('width', '16');
    fireEvent.mouseEnter(item.parentElement!);
    await waitFor(() =>
      expect(screen.getByText('正在将本次任务梳理为 Skill，请在对话中查看真实执行过程和结果。')).toBeVisible()
    );
    expect(screen.queryByText('任务空闲时可用')).not.toBeInTheDocument();
    fireEvent.click(item);
    expect(onSaveSkill).not.toHaveBeenCalled();
  });

  it('prevents manual review while the main task is still running', async () => {
    const onRequestReview = vi.fn();
    await renderWithI18n(
      <FileAttachButton
        openFileSelector={vi.fn()}
        onLocalFilesAdded={vi.fn()}
        onRequestReview={onRequestReview}
        reviewLabelMode='manual'
        reviewDisabled
      />,
      'zh-CN',
      { wrapper: MemoryRouter }
    );

    fireEvent.click(screen.getByRole('button', { name: '添加到消息' }));
    const review = screen.getByRole('menuitem', { name: '手动审阅' });
    expect(review).toBeDisabled();
    expect(screen.queryByText('任务完成后可用')).not.toBeInTheDocument();
    const reviewTooltipTrigger = review.parentElement;
    expect(reviewTooltipTrigger).not.toBeNull();
    fireEvent.mouseEnter(reviewTooltipTrigger!);
    await waitFor(() => expect(screen.getByText('任务完成后可用')).toBeInTheDocument());
    fireEvent.click(review);
    expect(onRequestReview).not.toHaveBeenCalled();
  });
});
