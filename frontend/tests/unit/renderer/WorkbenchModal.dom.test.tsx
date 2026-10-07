import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import { renderWithI18n } from '../i18nTestUtils';

function Fixture({ afterClose }: { afterClose: () => void }) {
  const [visible, setVisible] = React.useState(false);
  return (
    <>
      <button onClick={() => setVisible(true)}>Open fixture</button>
      <WorkbenchModal
        visible={visible}
        title='Fixture'
        footer={null}
        onCancel={() => setVisible(false)}
        afterClose={afterClose}
      >
        <button>Inner action</button>
      </WorkbenchModal>
    </>
  );
}
describe('Workbench modal lifecycle adapter', () => {
  it('localizes a keyboard-reachable close control and returns focus after close without losing callbacks', async () => {
    const afterClose = vi.fn();
    await renderWithI18n(<Fixture afterClose={afterClose} />, 'zh-CN');
    const opener = screen.getByRole('button', { name: 'Open fixture' });
    opener.focus();
    fireEvent.click(opener);
    const close = screen.getByRole('button', { name: '关闭' });
    expect(close.tagName).toBe('BUTTON');
    expect(close.parentElement?.getAttribute('role')).not.toBe('button');
    fireEvent.click(close);
    await waitFor(() => expect(afterClose).toHaveBeenCalledTimes(1));
    expect(opener).toHaveFocus();
  });
  it('preserves the framework static API without replacing imperative dialog implementations', () => {
    expect(WorkbenchModal.confirm).toBeTypeOf('function');
    expect(WorkbenchModal.useModal).toBeTypeOf('function');
  });
});
