import React from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import AppLoader from '@/renderer/components/layout/AppLoader';
import en from '@/renderer/services/i18n/locales/en-US/common.json';
import zh from '@/renderer/services/i18n/locales/zh-CN/common.json';
import { renderWithI18n } from '../i18nTestUtils';

describe('route loading presentation', () => {
  it.each([
    { language: 'en-US' as const, copy: en.loading },
    { language: 'zh-CN' as const, copy: zh.loading },
  ])('announces the $language wait without a progress or completion claim', async ({ language, copy }) => {
    await renderWithI18n(<AppLoader />, language);
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(copy);
    expect(status).toHaveAttribute('aria-live', 'polite');
    expect(status).not.toHaveTextContent('%');
  });

  it('fills its owning slot rather than imposing a full viewport inside the workspace', async () => {
    const view = await renderWithI18n(<AppLoader />, 'en-US');
    expect(view.container.innerHTML).not.toContain('100vh');
    expect(screen.getByRole('status')).toHaveClass('size-full', 'min-h-0');
  });
});
