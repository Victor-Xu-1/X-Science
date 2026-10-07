import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { usePreviewFocusReturn } from '@/renderer/pages/conversation/Preview/context/usePreviewFocusReturn';

describe('Preview focus return', () => {
  afterEach(() => {
    vi.useRealTimers();
    document.querySelectorAll('[data-focus-fixture]').forEach((node) => node.remove());
  });
  function button() {
    const node = document.createElement('button');
    node.dataset.focusFixture = '';
    document.body.append(node);
    return node;
  }
  it('returns to the actual opener after the close control is removed', () => {
    vi.useFakeTimers();
    const opener = button();
    const close = button();
    const { result } = renderHook(usePreviewFocusReturn);
    result.current.remember('file-a', opener);
    close.focus();
    result.current.restore('file-a');
    close.remove();
    act(() => vi.advanceTimersByTime(20));
    expect(opener).toHaveFocus();
  });
  it('does not steal focus when another preview opens or the user focuses an outside control', () => {
    vi.useFakeTimers();
    const opener = button();
    const outside = button();
    const { result } = renderHook(usePreviewFocusReturn);
    result.current.remember('file-a', opener);
    result.current.restore('file-a');
    result.current.remember('file-b', outside);
    outside.focus();
    act(() => vi.advanceTimersByTime(20));
    expect(outside).toHaveFocus();
    result.current.restore('file-b');
    opener.focus();
    act(() => vi.advanceTimersByTime(20));
    expect(opener).toHaveFocus();
  });
  it('never focuses a removed opener or lets an unmounted provider apply a pending restoration', () => {
    vi.useFakeTimers();
    const opener = button();
    const outside = button();
    const { result, unmount } = renderHook(usePreviewFocusReturn);
    result.current.remember('file-a', opener);
    result.current.restore('file-a');
    opener.remove();
    outside.focus();
    unmount();
    act(() => vi.advanceTimersByTime(20));
    expect(outside).toHaveFocus();
  });
  it('preserves the external opener when the same file is reopened from an internal control', () => {
    vi.useFakeTimers();
    const opener = button();
    const internal = button();
    const panel = document.createElement('div');
    panel.dataset.focusFixture = '';
    panel.className = 'preview-panel';
    document.body.append(panel);
    panel.append(internal);
    const { result } = renderHook(usePreviewFocusReturn);
    result.current.remember('file-a', opener);
    result.current.remember('file-a', internal);
    internal.focus();
    result.current.restore('file-a', true);
    panel.remove();
    act(() => vi.advanceTimersByTime(20));
    expect(opener).toHaveFocus();
  });
  it('keeps focus in a surviving preview when one file is closed', () => {
    vi.useFakeTimers();
    const opener = button();
    const close = button();
    const panel = document.createElement('section');
    panel.dataset.focusFixture = '';
    panel.dataset.previewFocusTarget = '';
    panel.tabIndex = -1;
    document.body.append(panel);
    const { result } = renderHook(usePreviewFocusReturn);
    result.current.remember('file-a', opener);
    close.focus();
    result.current.restore('file-a');
    close.remove();
    act(() => vi.advanceTimersByTime(20));
    expect(panel).toHaveFocus();
  });
  it('drops references for tabs that did not survive a committed render', () => {
    vi.useFakeTimers();
    const opener = button();
    const outside = button();
    const { result, rerender } = renderHook(({ ids }) => usePreviewFocusReturn(ids), {
      initialProps: { ids: ['file-a', 'discarded'] },
    });
    result.current.remember('discarded', opener);
    rerender({ ids: ['file-a'] });
    outside.focus();
    result.current.restore('discarded');
    act(() => vi.advanceTimersByTime(20));
    expect(outside).toHaveFocus();
  });
});
