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
