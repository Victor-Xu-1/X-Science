import { fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import SynonModal from '@/renderer/components/base/SynonModal';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/hooks/context/ThemeContext', () => ({
  useThemeContext: () => ({ fontScale: 1 }),
}));

describe('Shared workbench modal', () => {
  it('names the real Arco dialog from its visible custom header and uses a non-submitting close control', async () => {
    const onCancel = vi.fn();
    await renderWithI18n(
      <SynonModal visible header='Fixture details' onCancel={onCancel} footer={null}>
        Read-only content
      </SynonModal>,
      'en-US'
    );
    const dialog = screen.getByRole('dialog', { name: 'Fixture details' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    const close = screen.getByRole('button', { name: 'Close' });
    expect(close).toHaveAttribute('type', 'button');
    fireEvent.click(close);
    expect(onCancel).toHaveBeenCalledOnce();
  });
  it('preserves the caller render hook while retaining the internal accessible name', async () => {
    const modalRender = vi.fn((node: React.ReactNode) => <div data-testid='custom-modal-shell'>{node}</div>);
    await renderWithI18n(
      <SynonModal visible title='Custom shell' modalRender={modalRender} footer={null}>
        Details
      </SynonModal>,
      'en-US'
    );
    expect(screen.getByTestId('custom-modal-shell')).toContainElement(
      screen.getByRole('dialog', { name: 'Custom shell' })
    );
    expect(modalRender).toHaveBeenCalled();
  });
});
