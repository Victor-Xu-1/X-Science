/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ConfigProvider, Message } from '@arco-design/web-react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import React, { useRef, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

function MessageHost({
  captureClose,
  onClose,
}: {
  captureClose?: (close: () => void) => void;
  onClose?: (id: string) => void;
}) {
  const [message, holder] = Message.useMessage({ duration: 0 });
  const firstClose = useRef<(() => void) | undefined>(undefined);
  const secondClose = useRef<(() => void) | undefined>(undefined);
  const [revision, setRevision] = useState(0);

  return (
    <ConfigProvider prefixCls='identity' effectGlobalNotice={false}>
      <button
        onClick={() => {
          firstClose.current = message.success({
            id: 'first',
            content: 'First notice',
            onClose: () => onClose?.('first'),
          });
          if (firstClose.current) captureClose?.(firstClose.current);
          secondClose.current = message.error({
            id: 'second',
            content: 'Second notice',
            onClose: () => onClose?.('second'),
          });
        }}
      >
        Publish first burst
      </button>
      <button onClick={() => firstClose.current?.()}>Close first notice</button>
      <button onClick={() => secondClose.current?.()}>Close second notice</button>
      <button onClick={() => setRevision((value) => value + 1)}>Rerender host</button>
      <button onClick={() => message.info({ id: 'third', content: 'Third notice', duration: 1000 })}>
        Publish after rerender
      </button>
      <button
        onClick={() => {
          const close = message.warning({ id: 'cancelled', content: 'Cancelled before mount' });
          close();
        }}
      >
        Publish and close immediately
      </button>
      <output>{revision}</output>
      {holder}
    </ConfigProvider>
  );
}

describe('real Arco hook message identity', () => {
  beforeEach(() => vi.useFakeTimers());

  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('publishes a first-position burst and another container after rerender without key warnings', () => {
    const forwardError = console.error.bind(console);
    const errors = vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => forwardError(...args));

    render(<MessageHost />);
    fireEvent.click(screen.getByRole('button', { name: 'Publish first burst' }));
    expect(screen.getByText('First notice')).toBeTruthy();
    expect(screen.getByText('Second notice')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Rerender host' }));
    fireEvent.click(screen.getByRole('button', { name: 'Publish after rerender' }));
    expect(screen.getByText('Third notice')).toBeTruthy();
    act(() => vi.advanceTimersByTime(1000));
    act(() => vi.advanceTimersByTime(100));
    act(() => vi.advanceTimersByTime(400));
    expect(screen.queryByText('Third notice')).toBeNull();
    expect(screen.getByText('First notice')).toBeTruthy();
    expect(screen.getByText('Second notice')).toBeTruthy();

    expect(errors.mock.calls.filter((args) => /unique.*key|same key/i.test(args.map(String).join(' ')))).toEqual([]);
  });

  it('closes each notice from the first same-position burst through its own returned handle', () => {
    const onClose = vi.fn();
    render(<MessageHost onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Publish first burst' }));
    expect(screen.getByText('First notice')).toBeTruthy();
    expect(screen.getByText('Second notice')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Close second notice' }));
    act(() => vi.advanceTimersByTime(100));
    act(() => vi.advanceTimersByTime(400));
    expect(screen.queryByText('Second notice')).toBeNull();
    expect(screen.getByText('First notice')).toBeTruthy();
    expect(onClose.mock.calls).toEqual([['second']]);

    fireEvent.click(screen.getByRole('button', { name: 'Close first notice' }));
    act(() => vi.advanceTimersByTime(100));
    act(() => vi.advanceTimersByTime(400));
    expect(screen.queryByText('First notice')).toBeNull();
    expect(onClose.mock.calls).toEqual([['second'], ['first']]);
  });

  it('does not mount a notice cancelled through its handle before the container attaches', () => {
    render(<MessageHost />);
    fireEvent.click(screen.getByRole('button', { name: 'Publish and close immediately' }));
    expect(screen.queryByText('Cancelled before mount')).toBeNull();
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.queryByText('Cancelled before mount')).toBeNull();
  });

  it('drops an old close handle after unmount and lets a StrictMode host publish again', () => {
    let oldClose: (() => void) | undefined;
    const first = render(<MessageHost captureClose={(close) => (oldClose = close)} />);
    fireEvent.click(screen.getByRole('button', { name: 'Publish first burst' }));
    expect(oldClose).toBeTypeOf('function');
    first.unmount();
    expect(() => oldClose?.()).not.toThrow();

    render(
      <React.StrictMode>
        <MessageHost />
      </React.StrictMode>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Publish first burst' }));
    expect(screen.getByText('First notice')).toBeTruthy();
    expect(screen.getByText('Second notice')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Close first notice' }));
    act(() => vi.advanceTimersByTime(100));
    act(() => vi.advanceTimersByTime(400));
    expect(screen.queryByText('First notice')).toBeNull();
    expect(screen.getByText('Second notice')).toBeTruthy();
  });
});
