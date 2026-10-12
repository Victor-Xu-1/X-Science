import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import XScienceSendOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSendOptionsMenu';
import { renderWithI18n } from '../i18nTestUtils';

describe('XScienceSendOptionsMenu', () => {
  afterEach(() => {
    cleanup();
  });

  it('keeps the menu compact and reveals each explanation only while hovering', async () => {
    const onSelect = vi.fn();
    await renderWithI18n(<XScienceSendOptionsMenu hasDraft onSelect={onSelect} />);

    fireEvent.click(screen.getByRole('button', { name: '更多发送选项' }));
    const menu = await screen.findByRole('menu', { name: '更多发送选项' });
    const planItem = within(menu).getByRole('menuitem', { name: '计划优先' });

    expect(within(menu).getByRole('menuitem', { name: '侧聊' })).toBeInTheDocument();
    expect(within(menu).getByRole('menuitem', { name: '在新会话中分支' })).toBeInTheDocument();
    expect(menu).not.toHaveTextContent('先规划任务计划，再执行');
    expect(planItem).toHaveAttribute('title', '先规划任务计划，再执行');

    fireEvent.mouseEnter(planItem);
    expect(await screen.findByText('先规划任务计划，再执行')).toBeInTheDocument();

    fireEvent.mouseLeave(planItem);
    await waitFor(() => expect(screen.queryByText('先规划任务计划，再执行')).not.toBeInTheDocument());

    fireEvent.click(planItem);
    expect(onSelect).toHaveBeenCalledWith('plan_first');
  });

  it('keeps the trigger glyph centered in its compact action button', async () => {
    await renderWithI18n(<XScienceSendOptionsMenu hasDraft onSelect={vi.fn()} />);

    const trigger = screen.getByRole('button', { name: '更多发送选项' });

    expect(trigger).toHaveClass('flex', 'items-center', 'justify-center');
    expect(trigger.querySelector('.sendbox-action-icon')).toBeInTheDocument();
  });
});
