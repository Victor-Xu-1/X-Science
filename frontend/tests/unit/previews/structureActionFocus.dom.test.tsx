import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { focusableElements } from '@/renderer/utils/focusScope';
import {
  captureStructureActionFocus,
  restoreStructureActionFocus,
  useStructureActionFocus,
} from '@/renderer/pages/conversation/Preview/components/viewers/structureActionFocus';

describe('owned structure action focus', () => {
  let wrapper: HTMLDivElement;
  let root: HTMLElement;
  let previous: HTMLButtonElement;
  let opener: HTMLButtonElement;
  let next: HTMLButtonElement;
  let outside: HTMLButtonElement;

  beforeEach(() => {
    wrapper = document.createElement('div');
    root = document.createElement('section');
    const group = document.createElement('footer');
    [previous, opener, next, outside] = Array.from({ length: 4 }, () => document.createElement('button'));
    group.append(previous, opener, next);
    root.append(group);
    wrapper.append(root);
    document.body.append(wrapper, outside);
    opener.focus();
  });

  afterEach(() => {
    wrapper.remove();
    outside.remove();
    vi.unstubAllGlobals();
  });

  const parkOnBody = () => {
    outside.focus();
    outside.blur();
    expect(document.activeElement).toBe(document.body);
  };

  it('returns to the same enabled control without scrolling', () => {
    const snapshot = captureStructureActionFocus(root);
    parkOnBody();
    const focus = vi.spyOn(opener, 'focus');
    restoreStructureActionFocus(snapshot, () => true);
    expect(opener).toHaveFocus();
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  it.each([false, true])('uses the nearest enabled neighbour in the same group (%s)', (disableNext) => {
    const snapshot = captureStructureActionFocus(root);
    opener.disabled = true;
    next.disabled = disableNext;
    parkOnBody();
    restoreStructureActionFocus(snapshot, () => true);
    expect(disableNext ? previous : next).toHaveFocus();
  });

  it('does not capture outside controls or a programmatic BODY destination', () => {
    outside.focus();
    expect(captureStructureActionFocus(root)).toBeNull();
    parkOnBody();
    expect(captureStructureActionFocus(root)).toBeNull();
    expect(captureStructureActionFocus(null)).toBeNull();
  });

  it.each(['outside', 'inside'] as const)('preserves a newer %s focus destination', (destination) => {
    const snapshot = captureStructureActionFocus(root);
    const newer = destination === 'outside' ? outside : next;
    newer.focus();
    restoreStructureActionFocus(snapshot, () => true);
    expect(newer).toHaveFocus();
  });

  it.each(['css', 'hidden', 'inert', 'aria'] as const)('never restores into a hidden ancestor (%s)', (mode) => {
    const snapshot = captureStructureActionFocus(root);
    if (mode === 'css') wrapper.style.display = 'none';
    else if (mode === 'hidden') wrapper.hidden = true;
    else if (mode === 'inert') wrapper.setAttribute('inert', '');
    else wrapper.setAttribute('aria-hidden', 'true');
    parkOnBody();
    expect(focusableElements(root)).toEqual([]);
    restoreStructureActionFocus(snapshot, () => true);
    expect(document.activeElement).toBe(document.body);
  });

  it('rejects a retired source generation and detached owner', () => {
    const snapshot = captureStructureActionFocus(root);
    parkOnBody();
    restoreStructureActionFocus(snapshot, () => false);
    expect(document.activeElement).toBe(document.body);
    root.remove();
    restoreStructureActionFocus(snapshot, () => true);
    expect(document.activeElement).toBe(document.body);
  });

  it.each(['new-action', 'unmount'] as const)('cancels a queued restoration on %s', (reason) => {
    const frames = new Map<number, FrameRequestCallback>();
    let revision = 0;
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      frames.set(++revision, callback);
      return revision;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
    const snapshot = captureStructureActionFocus(root);
    parkOnBody();
    const hook = renderHook(() => useStructureActionFocus(['source']));
    act(() => hook.result.current.restore(snapshot, () => true));
    expect(frames.size).toBe(1);
    if (reason === 'new-action') act(() => hook.result.current.cancel());
    else hook.unmount();
    expect(frames.size).toBe(0);
    expect(document.activeElement).toBe(document.body);
  });

  it('invalidates a source replacement at commit before a load effect can retire the engine', () => {
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      frames.push(callback);
      return frames.length;
    });
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    const snapshot = captureStructureActionFocus(root);
    parkOnBody();
    const hook = renderHook(({ source }) => useStructureActionFocus([source]), {
      initialProps: { source: 'old' },
    });
    act(() => hook.result.current.restore(snapshot, () => true));
    hook.rerender({ source: 'new' });
    act(() => frames.forEach((frame) => frame(0)));
    expect(document.activeElement).toBe(document.body);
  });
});
