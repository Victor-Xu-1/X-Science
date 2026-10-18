import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...actual,
    Message: {
      ...actual.Message,
      success: vi.fn(),
      error: vi.fn(),
    },
  };
});

import XSciencePermissionMenu from '@/renderer/components/xScience/runtime/XSciencePermissionMenu';
import { renderWithI18n } from '../i18nTestUtils';

const permissionOption = {
  id: 'mode',
  category: 'mode',
  currentValue: 'default',
  options: [{ value: 'default', label: 'Default', description: null }],
};

describe('XSciencePermissionMenu', () => {
  it('renders one task-wide permission standard and persists the selected mode', async () => {
    const setConfigOption = vi.fn().mockResolvedValue([]);
    const view = await renderWithI18n(
      <XSciencePermissionMenu
        option={permissionOption}
        setStatus={{ state: 'idle' }}
        setConfigOption={setConfigOption}
        includeStandardModes
      />,
      'zh-CN'
    );

    const trigger = screen.getByTestId('x-science-permission-selector');
    expect(trigger).toHaveAttribute('aria-label', '权限 · 请求批准');
    expect(trigger).toHaveClass('composer-icon-control');
    fireEvent.click(trigger);
    expect(screen.getByRole('menu')).toHaveAttribute('aria-label', '授权模式');
    expect(screen.getByRole('menu')).toHaveClass('app-overlay-menu', 'composer-control-menu');
    expect(screen.getByTestId('x-science-permission-option-bypassPermissions')).toHaveAttribute(
      'aria-label',
      expect.stringContaining('后台自动批准所有工具操作')
    );
    expect(screen.getByTestId('x-science-permission-option-bypassPermissions')).toHaveAttribute(
      'title',
      '后台自动批准所有工具操作，不显示权限弹窗'
    );
    expect(screen.getByTestId('x-science-permission-option-default')).toHaveAttribute(
      'aria-label',
      expect.stringContaining('请求批准')
    );
    expect(screen.getByTestId('x-science-permission-option-smart')).toHaveAttribute(
      'aria-label',
      expect.stringContaining('帮我批准')
    );
    expect(
      screen.getByTestId('x-science-permission-option-default').querySelector('[data-permission-icon="request"]')
    ).toBeInTheDocument();
    expect(
      screen.getByTestId('x-science-permission-option-smart').querySelector('[data-permission-icon="smart"]')
    ).toBeInTheDocument();
    expect(
      screen.getByTestId('x-science-permission-option-bypassPermissions').querySelector('[data-permission-icon="full"]')
    ).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('x-science-permission-option-bypassPermissions'));
    await waitFor(() => expect(setConfigOption).toHaveBeenCalledWith('mode', 'bypassPermissions'));
    // The write acknowledgement alone must not manufacture an observed mode.
    expect(screen.getByTestId('x-science-permission-selector')).toHaveAttribute('data-current-permission', 'default');
    view.rerender(
      <XSciencePermissionMenu
        option={{ ...permissionOption, currentValue: 'bypassPermissions' }}
        setStatus={{ state: 'idle' }}
        setConfigOption={setConfigOption}
        includeStandardModes
      />
    );
    expect(
      screen.getByTestId('x-science-permission-selector').querySelector('[data-permission-icon="full"]')
    ).toBeInTheDocument();
  });

  it('does not invent runtime modes that the active conversation did not advertise', async () => {
    await renderWithI18n(
      <XSciencePermissionMenu
        option={permissionOption}
        setStatus={{ state: 'idle' }}
        setConfigOption={vi.fn().mockResolvedValue([])}
      />,
      'zh-CN'
    );

    fireEvent.click(screen.getByTestId('x-science-permission-selector'));
    expect(screen.getByTestId('x-science-permission-option-default')).toBeInTheDocument();
    expect(screen.queryByTestId('x-science-permission-option-bypassPermissions')).not.toBeInTheDocument();
  });
});
