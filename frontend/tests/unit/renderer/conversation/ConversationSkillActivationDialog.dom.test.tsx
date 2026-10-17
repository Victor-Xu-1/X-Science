import React from 'react';
import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../i18nTestUtils';
import { ConversationSkillActivationDialog } from '@/renderer/pages/conversation/platforms/acp/ConversationSkillActivationDialog';

vi.mock('@/renderer/components/base/WorkbenchModal', () => ({
  default: ({
    title,
    children,
    okText,
    cancelText,
    confirmLoading,
    onOk,
    onCancel,
  }: {
    title: React.ReactNode;
    children: React.ReactNode;
    okText: string;
    cancelText: string;
    confirmLoading: boolean;
    onOk: () => void;
    onCancel: () => void;
  }) => (
    <div role='dialog' aria-label={typeof title === 'string' ? title : undefined}>
      {children}
      <button disabled={confirmLoading} onClick={onOk}>
        {okText}
      </button>
      <button disabled={confirmLoading} onClick={onCancel}>
        {cancelText}
      </button>
    </div>
  ),
}));

describe('SkillActivation dialog', () => {
  it('requires an explicit translated confirmation and offers cancellation without execution', async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const onCancel = vi.fn();
    const state = { name: 'new-workflow', phase: 'confirm' as const, attached: false, pending: false };
    const view = await renderWithI18n(
      <ConversationSkillActivationDialog state={state} onConfirm={onConfirm} onCancel={onCancel} />,
      'en-US'
    );
    expect(screen.getByRole('dialog')).toHaveTextContent('No task will be sent.');
    expect(onConfirm).not.toHaveBeenCalled();
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('dialog', { name: '为当前专家启用 Skill' })).toHaveTextContent('不会发送任务');
    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '启用并选择' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('uses refresh-only recovery text and blocks duplicate confirmation while pending', async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const state = { name: 'new-workflow', phase: 'refresh-error' as const, attached: true, pending: true };
    await renderWithI18n(
      <ConversationSkillActivationDialog state={state} onConfirm={onConfirm} onCancel={vi.fn()} />,
      'en-US'
    );
    expect(screen.getByRole('alert')).toHaveTextContent('will not save, bind, or send again');
    expect(screen.getByRole('button', { name: 'Retry refresh' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry refresh' }));
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
