import type { TouchEvent } from 'react';
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useTouchScrollIntent } from '@/renderer/pages/conversation/Messages/useTouchScrollIntent';

const touch = (y: number, identifier = 1) => ({ identifier, clientY: y });
const event = (...touches: ReturnType<typeof touch>[]) => ({ touches }) as unknown as TouchEvent<HTMLElement>;

describe('touch reading intent', () => {
  it('distinguishes finger movement from scroll direction and ignores a stationary touch', () => {
    const intent = vi.fn();
    const { result } = renderHook(() => useTouchScrollIntent('c', intent));
    act(() => result.current.onTouchStart(event(touch(200))));
    act(() => result.current.onTouchMove(event(touch(200))));
    expect(intent).not.toHaveBeenCalled();
    act(() => result.current.onTouchMove(event(touch(250))));
    expect(intent).toHaveBeenLastCalledWith('away-from-tail');
    act(() => result.current.onTouchMove(event(touch(100))));
    expect(intent).toHaveBeenLastCalledWith('toward-tail');
  });
  it('does not inherit canceled, multitouch or another conversation gestures', () => {
    const intent = vi.fn();
    const { result, rerender } = renderHook(({ owner }) => useTouchScrollIntent(owner, intent), {
      initialProps: { owner: 'c' },
    });
    act(() => result.current.onTouchStart(event(touch(200))));
    act(() => result.current.onTouchCancel());
    act(() => result.current.onTouchMove(event(touch(100))));
    act(() => result.current.onTouchStart(event(touch(200), touch(300, 2))));
    act(() => result.current.onTouchMove(event(touch(100))));
    act(() => result.current.onTouchStart(event(touch(200))));
    rerender({ owner: 'other' });
    act(() => result.current.onTouchMove(event(touch(100))));
    expect(intent).not.toHaveBeenCalled();
  });
});
