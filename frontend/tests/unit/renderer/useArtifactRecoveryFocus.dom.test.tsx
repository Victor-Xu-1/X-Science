import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useArtifactRecoveryFocus } from '@/renderer/pages/artifact/useArtifactRecoveryFocus';
import type { ArtifactReadState } from '@/renderer/pages/artifact/useArtifactMetadataReads';

let destination: HTMLDivElement;
beforeEach(() => {
  destination = document.createElement('div');
  destination.tabIndex = -1;
  document.body.append(destination);
});
afterEach(() => destination.remove());

function setup() {
  const target = { current: destination };
  return renderHook(({ owner, status }) => useArtifactRecoveryFocus(owner, status, target), {
    initialProps: { owner: 'file-a', status: 'failed' as ArtifactReadState<unknown>['status'] },
  });
}

describe('artifact recovery focus ownership', () => {
  it.each(['ready', 'failed'] as const)('restores a manually requested terminal state: %s', (status) => {
    const view = setup();
    act(() => view.result.current());
    view.rerender({ owner: 'file-a', status: 'loading' });
    expect(destination).not.toHaveFocus();
    view.rerender({ owner: 'file-a', status });
    expect(destination).toHaveFocus();
    expect(destination.tabIndex).toBe(-1);
  });
  it('does not focus a normal read without a manual recovery intent', () => {
    const view = setup();
    view.rerender({ owner: 'file-a', status: 'ready' });
    expect(destination).not.toHaveFocus();
  });
  it('rejects recovery intent after the view owner changes', () => {
    const view = setup();
    act(() => view.result.current());
    view.rerender({ owner: 'file-b', status: 'ready' });
    expect(destination).not.toHaveFocus();
  });
  it('does not steal a connected external focus destination', () => {
    const external = document.createElement('button');
    document.body.append(external);
    const view = setup();
    act(() => {
      view.result.current();
      external.focus();
    });
    view.rerender({ owner: 'file-a', status: 'ready' });
    expect(external).toHaveFocus();
    external.remove();
  });
  it.each(['hidden', 'inert'])('never focuses an unavailable target: %s', (attribute) => {
    const view = setup();
    act(() => view.result.current());
    destination.setAttribute(attribute, '');
    view.rerender({ owner: 'file-a', status: 'ready' });
    expect(destination).not.toHaveFocus();
  });
  it('never focuses a disconnected target', () => {
    const view = setup();
    act(() => view.result.current());
    destination.remove();
    view.rerender({ owner: 'file-a', status: 'ready' });
    expect(destination).not.toHaveFocus();
  });
});
