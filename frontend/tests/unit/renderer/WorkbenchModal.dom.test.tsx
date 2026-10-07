import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getI18n, setI18n } from 'react-i18next';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import { renderWithI18n } from '../i18nTestUtils';
// Vitest resolves Arco's CommonJS entry; match the production ESM bootstrap's
// official React 19 adapter for the same real imperative rendering path.
import '@arco-design/web-react/lib/_util/react-19-adapter';

let previousI18n: ReturnType<typeof getI18n>;
beforeEach(() => {
  previousI18n = getI18n();
});
afterEach(async () => {
  cleanup();
  act(() => WorkbenchModal.destroyAll());
  await waitFor(() => expect(document.querySelector('[data-workbench-modal-scope]')).toBeNull());
  setI18n(previousI18n);
});

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

  it('returns an imperative confirmation to its opener after cancelling without invoking the action', async () => {
    const afterClose = vi.fn();
    const onCancel = vi.fn();
    const onOk = vi.fn();
    await renderWithI18n(
      <button
        onClick={() =>
          WorkbenchModal.confirm({
            title: 'Read-only confirmation',
            content: 'Fixture content',
            cancelText: 'Dismiss',
            okText: 'Apply',
            afterClose,
            onCancel,
            onOk,
          })
        }
      >
        Open confirmation
      </button>
    );
    const opener = screen.getByRole('button', { name: 'Open confirmation' });
    opener.focus();
    fireEvent.click(opener);
    const cancel = await screen.findByRole('button', { name: 'Dismiss' });
    cancel.focus();
    fireEvent.click(cancel);
    await waitFor(() => expect(afterClose).toHaveBeenCalledTimes(1));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onOk).not.toHaveBeenCalled();
    expect(opener).toHaveFocus();
  });

  it('retains the real notice update/close instance and localizes an explicitly enabled close action', async () => {
    const afterClose = vi.fn();
    let instance: ReturnType<typeof WorkbenchModal.info> | undefined;
    const view = await renderWithI18n(
      <button
        onClick={() => {
          instance = WorkbenchModal.info({
            title: 'Read-only notice',
            content: 'Initial details',
            closable: true,
            footer: null,
            afterClose,
          });
        }}
      >
        Open notice
      </button>,
      'zh-CN'
    );
    // Imperative roots are outside the provider tree, as in the application;
    // production bootstraps the same default instance via initReactI18next.
    setI18n(view.i18n);
    const opener = screen.getByRole('button', { name: 'Open notice' });
    opener.focus();
    fireEvent.click(opener);
    await screen.findByText('Initial details');
    expect(instance?.close).toBeTypeOf('function');
    act(() => instance?.update({ content: 'Updated details' }));
    expect(await screen.findByText('Updated details')).toBeInTheDocument();
    const close = screen.getByRole('button', { name: '关闭' });
    expect(close.tagName).toBe('BUTTON');
    expect(close.tabIndex).toBe(0);
    fireEvent.click(close);
    await waitFor(() => expect(afterClose).toHaveBeenCalledTimes(1));
    expect(opener).toHaveFocus();
  });

  it('leaves asynchronous confirmation and loading to the real framework', async () => {
    const afterClose = vi.fn();
    let resolveAction!: () => void;
    const action = new Promise<void>((resolve) => {
      resolveAction = resolve;
    });
    const onOk = vi.fn(() => action);
    await renderWithI18n(
      <button
        onClick={() =>
          WorkbenchModal.confirm({
            title: 'Async confirmation',
            content: 'Fixture details',
            okText: 'Apply',
            cancelText: 'Dismiss',
            onOk,
            afterClose,
          })
        }
      >
        Open async confirmation
      </button>
    );
    const opener = screen.getByRole('button', { name: 'Open async confirmation' });
    opener.focus();
    fireEvent.click(opener);
    const apply = await screen.findByRole('button', { name: 'Apply' });
    fireEvent.click(apply);
    expect(onOk).toHaveBeenCalledTimes(1);
    expect(apply).toHaveClass('arco-btn-loading');
    expect(screen.getByRole('dialog', { name: /Async confirmation/ })).toBeInTheDocument();
    expect(afterClose).not.toHaveBeenCalled();
    await act(async () => resolveAction());
    await waitFor(() => expect(afterClose).toHaveBeenCalledTimes(1));
    expect(opener).toHaveFocus();
  });

  it('retains custom close content and deliberate focus outside an imperative notice', async () => {
    const afterClose = vi.fn();
    let instance: ReturnType<typeof WorkbenchModal.info> | undefined;
    await renderWithI18n(
      <>
        <button
          onClick={() => {
            instance = WorkbenchModal.info({
              title: 'Custom notice',
              content: 'Fixture details',
              closable: true,
              closeIcon: <button aria-label='Custom dismissal'>Dismiss</button>,
              footer: null,
              afterClose,
            });
          }}
        >
          Open custom notice
        </button>
        <button>Outside destination</button>
      </>
    );
    const opener = screen.getByRole('button', { name: 'Open custom notice' });
    opener.focus();
    fireEvent.click(opener);
    expect(await screen.findByRole('button', { name: 'Custom dismissal' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '关闭' })).not.toBeInTheDocument();
    act(() => instance?.close());
    const destination = screen.getByRole('button', { name: 'Outside destination' });
    destination.focus();
    await waitFor(() => expect(afterClose).toHaveBeenCalledTimes(1));
    expect(destination).toHaveFocus();
  });
});
