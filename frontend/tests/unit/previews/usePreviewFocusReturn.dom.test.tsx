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

  it('does not borrow an unrelated workspace panel when an embedded board closes', () => {
    vi.useFakeTimers();
    const boundary = document.createElement('div');
    boundary.dataset.focusFixture = '';
    document.body.append(boundary);
    const opener = button();
    boundary.append(opener);
    const outsidePanel = document.createElement('section');
    outsidePanel.dataset.focusFixture = '';
    outsidePanel.dataset.previewFocusTarget = '';
    outsidePanel.tabIndex = -1;
    document.body.append(outsidePanel);
    const close = button();
    const { result } = renderHook(() => usePreviewFocusReturn(['embedded'], { current: boundary }));
    result.current.remember('embedded', opener);
    close.focus();
    result.current.restore('embedded', true);
    close.remove();
    act(() => vi.advanceTimersByTime(20));
    expect(opener).toHaveFocus();
  });

  it('selects a surviving panel only inside the owned embedded boundary', () => {
    vi.useFakeTimers();
    const outside = document.createElement('section');
    outside.dataset.focusFixture = '';
    outside.dataset.previewFocusTarget = '';
    outside.tabIndex = -1;
    document.body.append(outside);
    const boundary = document.createElement('div');
    boundary.dataset.focusFixture = '';
    document.body.append(boundary);
    const panel = outside.cloneNode() as HTMLElement;
    boundary.append(panel);
    const opener = button();
    boundary.append(opener);
    const close = button();
    const { result } = renderHook(() => usePreviewFocusReturn(['embedded'], { current: boundary }));
    result.current.remember('embedded', opener);
    close.focus();
    result.current.restore('embedded');
    close.remove();
    act(() => vi.advanceTimersByTime(20));
    expect(panel).toHaveFocus();
  });

  it('cannot retain an unrelated outside opener or restore after its boundary is removed', () => {
    vi.useFakeTimers();
    const outside = button();
    const boundary = document.createElement('div');
    boundary.dataset.focusFixture = '';
    document.body.append(boundary);
    const ref: { current: HTMLElement | null } = { current: boundary };
    const { result } = renderHook(() => usePreviewFocusReturn(['embedded'], ref));
    result.current.remember('embedded', outside);
    result.current.restore('embedded');
    ref.current = null;
    outside.focus();
    act(() => vi.advanceTimersByTime(20));
    expect(outside).toHaveFocus();
  });

  it('retains an owned portalled fullscreen target without choosing an unrelated earlier panel', () => {
    vi.useFakeTimers();
    const boundary = document.createElement('div');
    boundary.dataset.focusFixture = '';
    document.body.append(boundary);
    const opener = button();
    boundary.append(opener);
    const outside = document.createElement('section');
    outside.dataset.focusFixture = '';
    outside.dataset.previewFocusTarget = '';
    outside.dataset.previewFocusScope = 'other';
    outside.tabIndex = -1;
    document.body.append(outside);
    const owned = outside.cloneNode() as HTMLElement;
    owned.dataset.previewFocusScope = 'embedded';
    document.body.append(owned);
    const close = button();
    const { result } = renderHook(() => usePreviewFocusReturn(['file'], { current: boundary }, 'embedded'));
    result.current.remember('file', opener);
    close.focus();
    result.current.restore('file');
    close.remove();
    act(() => vi.advanceTimersByTime(20));
    expect(owned).toHaveFocus();
  });
});
