import XScienceTaskAttentionNotice from '@/renderer/components/xScience/runtime/XScienceTaskAttentionNotice';
import { runtimePausePresentation } from '@/renderer/components/xScience/runtime/runtimePausePresentation';
import { fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

describe('XScienceTaskAttentionNotice pause causes', () => {
  it.each(['en-US', 'zh-CN'] as const)(
    'explains a subscription refusal through a closed category in %s',
    async (language) => {
      const onAction = vi.fn();
      const props = {
        kind: 'paused' as const,
        reason: 'model_provider_unavailable',
        providerFailureKind: 'subscription' as const,
        onAction,
      };
      await renderWithI18n(<XScienceTaskAttentionNotice {...props} />, language);
      const notice = screen.getByTestId('x-science-paused-attention');
      expect(notice).toHaveTextContent(language === 'en-US' ? 'subscription or billing' : '订阅或计费');
      expect(notice).toHaveTextContent(language === 'en-US' ? 'preserved' : '已保留');
      expect(notice).not.toHaveTextContent(/model_provider_unavailable|waiting for model selection|等待选择模型/);
      expect(onAction).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button'));
      expect(onAction).toHaveBeenCalledOnce();
    }
  );

  it.each(['en-US', 'zh-CN'] as const)('shows the retained-input continuation action once in %s', async (language) => {
    const onAction = vi.fn();
    await renderWithI18n(
      <XScienceTaskAttentionNotice kind='pending_input' reason={null} onAction={onAction} />,
      language
    );
    const notice = screen.getByTestId('x-science-pending_input-attention');
    expect(notice).toHaveTextContent(language === 'en-US' ? 'still pending' : '仍待处理');
    expect(notice).toHaveTextContent(language === 'en-US' ? 'completed results are preserved' : '已完成结果均已保留');
    expect(onAction).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button'));
    expect(onAction).toHaveBeenCalledOnce();
  });
  it.each(['en-US', 'zh-CN'] as const)(
    'explains model no-progress in %s without inventing retry history',
    async (language) => {
      const onAction = vi.fn();
      await renderWithI18n(
        <XScienceTaskAttentionNotice kind='paused' reason='provider_stream_no_progress' onAction={onAction} />,
        language
      );
      const notice = screen.getByTestId('x-science-paused-attention');
      expect(notice).toHaveTextContent(
        language === 'en-US'
          ? 'The model did not return usable public text or a tool action in this request.'
          : '本轮模型未返回可用的公开回复或工具动作'
      );
      expect(notice).toHaveTextContent(language === 'en-US' ? 'preserved' : '已保留');
      expect(notice).not.toHaveTextContent(/连续恢复|repeated recovery|120|provider_stream_no_progress/);
      expect(onAction).not.toHaveBeenCalled();
      fireEvent.click(screen.getByRole('button'));
      expect(onAction).toHaveBeenCalledOnce();
    }
  );

  it.each(['en-US', 'zh-CN'] as const)('retains neutral closed fallback in %s', async (language) => {
    await renderWithI18n(
      <XScienceTaskAttentionNotice kind='paused' reason='PRIVATE_PROVIDER_ERROR_SENTINEL' />,
      language
    );
    const notice = screen.getByTestId('x-science-paused-attention');
    expect(notice).toHaveTextContent(language === 'en-US' ? 'Execution is paused' : '当前是运行暂停');
    expect(notice).not.toHaveTextContent(/PRIVATE_PROVIDER_ERROR_SENTINEL|连续恢复|repeated recovery/);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('preserves output-limit copy and keeps arbitrary reasons out of translation keys', () => {
    expect(runtimePausePresentation('provider_output_token_limit')).toEqual({
      title: 'taskPaused',
      description: 'outputLimitPaused',
    });
    for (const reason of [null, 'unknown', 'provider_stream_no_progress.extra', 'PRIVATE_PROVIDER_ERROR_SENTINEL']) {
      expect(runtimePausePresentation(reason)).toEqual({ title: 'taskPaused', description: 'recoveryPaused' });
    }
  });
});
