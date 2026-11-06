import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useWorkspaceCollapse } from '@/renderer/pages/conversation/hooks/useWorkspaceCollapse';
import {
  dispatchWorkspaceHasFilesEvent,
  dispatchWorkspaceOpenEvent,
  dispatchWorkspaceToggleEvent,
} from '@/renderer/utils/workspace/workspaceEvents';

const mobileWorkspace = {
  workspaceEnabled: true,
  isMobile: true,
  conversation_id: 'frame-a',
  preferenceKey: 'frame-a',
};

describe('workspace visibility ownership', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    cleanup();
  });

  it('keeps an explicitly opened mobile workspace open when search results announce files', () => {
    const { result } = renderHook(() => useWorkspaceCollapse(mobileWorkspace));
    expect(result.current.rightSiderCollapsed).toBe(true);

    act(() => dispatchWorkspaceToggleEvent());
    expect(result.current.rightSiderCollapsed).toBe(false);

    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, false));
    expect(result.current.rightSiderCollapsed).toBe(false);
  });

  it('does not auto-open mobile files or undo an explicit close', () => {
    const { result } = renderHook(() => useWorkspaceCollapse(mobileWorkspace));
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, true));
    expect(result.current.rightSiderCollapsed).toBe(true);

    act(() => dispatchWorkspaceOpenEvent());
    act(() => dispatchWorkspaceOpenEvent());
    expect(result.current.rightSiderCollapsed).toBe(false);

    act(() => dispatchWorkspaceToggleEvent());
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, false));
    expect(result.current.rightSiderCollapsed).toBe(true);
  });

  it('closes mobile files when switching conversations and does not reopen on their load', () => {
    const { result, rerender } = renderHook(useWorkspaceCollapse, { initialProps: mobileWorkspace });
    act(() => dispatchWorkspaceToggleEvent());
    expect(result.current.rightSiderCollapsed).toBe(false);

    rerender({ ...mobileWorkspace, conversation_id: 'frame-b', preferenceKey: 'frame-b' });
    expect(result.current.rightSiderCollapsed).toBe(true);
    act(() => dispatchWorkspaceHasFilesEvent(true, 'frame-b', true));
    expect(result.current.rightSiderCollapsed).toBe(true);
  });

  it('starts a fresh mobile hook scope closed despite the previous scope being expanded', () => {
    const first = renderHook(() => useWorkspaceCollapse(mobileWorkspace));
    act(() => dispatchWorkspaceToggleEvent());
    expect(first.result.current.rightSiderCollapsed).toBe(false);
    first.unmount();

    const next = renderHook(() => useWorkspaceCollapse(mobileWorkspace));
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, true));
    expect(next.result.current.rightSiderCollapsed).toBe(true);
  });

  it('preserves desktop file auto-expansion and manual collapse preferences', () => {
    const { result } = renderHook(() => useWorkspaceCollapse({ ...mobileWorkspace, isMobile: false }));
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, true));
    expect(result.current.rightSiderCollapsed).toBe(false);

    act(() => dispatchWorkspaceToggleEvent());
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, false));
    expect(result.current.rightSiderCollapsed).toBe(true);

    act(() => dispatchWorkspaceToggleEvent());
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, false));
    expect(result.current.rightSiderCollapsed).toBe(false);
  });

  it('still closes on mobile entry or when the workspace becomes disabled', () => {
    const { result, rerender } = renderHook(useWorkspaceCollapse, {
      initialProps: { ...mobileWorkspace, isMobile: false },
    });
    act(() => dispatchWorkspaceOpenEvent());
    expect(result.current.rightSiderCollapsed).toBe(false);

    rerender(mobileWorkspace);
    expect(result.current.rightSiderCollapsed).toBe(true);
    act(() => dispatchWorkspaceOpenEvent());
    expect(result.current.rightSiderCollapsed).toBe(false);

    rerender({ ...mobileWorkspace, workspaceEnabled: false });
    expect(result.current.rightSiderCollapsed).toBe(true);
    act(() => dispatchWorkspaceToggleEvent());
    act(() => dispatchWorkspaceHasFilesEvent(true, mobileWorkspace.conversation_id, false));
    expect(result.current.rightSiderCollapsed).toBe(true);
  });
});
