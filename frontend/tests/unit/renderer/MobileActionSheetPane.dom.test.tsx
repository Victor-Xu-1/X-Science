import React from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MobileActionSheet from '@/renderer/components/chat/MobileActionSheet';
import type { MobileActionSheetEntry } from '@/renderer/components/chat/MobileActionSheet';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

const entries: MobileActionSheetEntry[] = [
  {
    key: 'settings',
    label: 'Settings',
    submenu: {
      title: 'Settings',
      options: [{ key: 'option', label: 'Option' }],
      onSelect: () => {},
      footer: () => (
        <button type='button' data-testid='policy-footer'>
          Policy control
        </button>
      ),
    },
  },
];

function advance(ms = 64) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

function openSheet() {
  const view = render(<MobileActionSheet open onClose={() => {}} title='Actions' entries={entries} />);
  advance();
  const main = screen.getByTestId('mobile-action-sheet-settings');
  const pane = main.closest('[aria-hidden]') as HTMLElement;
  expect(pane).not.toBeNull();
  return { ...view, main, pane };
}

describe('mobile sheet pane ownership', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) =>
      setTimeout(() => callback(performance.now()), 16)
    );
    vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id));
  });
  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('makes the outgoing main pane inert while the actual submenu and footer are active', () => {
    const { main, pane } = openSheet();
    fireEvent.click(main);
    advance();
    expect(pane).toHaveAttribute('aria-hidden', 'true');
    expect(pane).toHaveAttribute('inert');
    const footer = screen.getByTestId('policy-footer');
    expect(footer.closest('[aria-hidden]')).not.toHaveAttribute('inert');
    expect(screen.getByRole('menuitemradio', { name: 'Option' })).toBeEnabled();
  });

  it('M454 keeps a disabled busy Save action focusable without invoking it or closing More', () => {
    const onClick = vi.fn();
    const onClose = vi.fn();
    render(
      <MobileActionSheet
        open
        onClose={onClose}
        entries={[
          {
            key: 'save-skill',
            label: 'Save Skill',
            disabled: true,
            keepDisabledFocusable: true,
            busy: true,
            description: 'Wait for the current task',
            onClick,
          },
        ]}
      />
    );
    advance();
    const save = screen.getByRole('menuitem', { name: /Save Skill/ });
    expect(save).not.toBeDisabled();
    expect(save).toHaveAttribute('aria-disabled', 'true');
    expect(save).toHaveAttribute('aria-busy', 'true');
    save.focus();
    expect(save).toHaveFocus();
    fireEvent.click(save);
    expect(onClick).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByText('Wait for the current task')).toBeVisible();
  });

  it('M454 deactivates inline footer immediately on Back and close, before retained pane unmount', () => {
    const footer = vi.fn((active: boolean) => <span data-testid='inline-active'>{String(active)}</span>);
    const items: MobileActionSheetEntry[] = [
      {
        key: 'your-files',
        label: 'Your files',
        submenu: { title: 'Your files', options: [], onSelect: vi.fn(), footer },
      },
    ];
    const view = render(<MobileActionSheet open onClose={vi.fn()} entries={items} />);
    advance();
    fireEvent.click(screen.getByTestId('mobile-action-sheet-your-files'));
    advance();
    expect(screen.getByTestId('inline-active')).toHaveTextContent('true');
    fireEvent.click(screen.getByRole('button', { name: 'common.back' }));
    expect(screen.getByTestId('inline-active')).toHaveTextContent('false');
    advance(300);
    fireEvent.click(screen.getByTestId('mobile-action-sheet-your-files'));
    advance();
    view.rerender(<MobileActionSheet open={false} onClose={vi.fn()} entries={items} />);
    expect(screen.getByTestId('inline-active')).toHaveTextContent('false');
    expect(screen.getByTestId('inline-active').closest('[aria-hidden]')).toHaveAttribute('inert');
  });

  it('keeps the retained outgoing submenu and its footer inert after going back', () => {
    const { main, pane } = openSheet();
    fireEvent.click(main);
    advance();
    const oldSub = screen.getByTestId('policy-footer').closest('[aria-hidden]') as HTMLElement;
    fireEvent.click(screen.getByRole('button', { name: 'common.back' }));
    expect(oldSub).toBeInTheDocument();
    expect(oldSub).toHaveAttribute('aria-hidden', 'true');
    expect(oldSub).toHaveAttribute('inert');
    expect(pane).not.toHaveAttribute('inert');
  });

  it('isolates the entire closing dialog until its existing exit animation unmounts it', () => {
    const { rerender } = openSheet();
    rerender(<MobileActionSheet open={false} onClose={() => {}} title='Actions' entries={entries} />);
    const closing = screen.getByRole('dialog', { hidden: true });
    expect(closing).toBeInTheDocument();
    expect(closing).toHaveAttribute('inert');
    expect(closing).toHaveAttribute('aria-hidden', 'true');
    advance(300);
    expect(screen.queryByRole('dialog', { hidden: true })).toBeNull();
  });

  it('does not let the old second animation frame reactivate a closed submenu on quick reopen', () => {
    const { main, pane, rerender } = openSheet();
    fireEvent.click(main);
    advance(16);
    rerender(<MobileActionSheet open={false} onClose={() => {}} title='Actions' entries={entries} />);
    advance(16);
    rerender(<MobileActionSheet open onClose={() => {}} title='Actions' entries={entries} />);
    advance(64);
    expect(pane).toHaveAttribute('aria-hidden', 'false');
    expect(pane).not.toHaveAttribute('inert');
    expect(screen.getByRole('menuitem', { name: 'Settings' })).toBeEnabled();
    const retained = screen.queryByTestId('policy-footer');
    if (retained) expect(retained.closest('[aria-hidden]')).toHaveAttribute('inert');
  });
});
