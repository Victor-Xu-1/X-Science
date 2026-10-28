/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { describe, expect, it, vi } from 'vitest';
import { installReactRootLifecycle, scheduleReactRootCleanup } from '@/renderer/utils/ui/reactRootLifecycle';

const hide = (persisted: boolean): Event => {
  const event = new Event('pagehide');
  Object.defineProperty(event, 'persisted', { value: persisted });
  return event;
};

describe('react root lifecycle', () => {
  it('defers HMR cleanup until the current render stack has completed', async () => {
    const cleanup = vi.fn();

    scheduleReactRootCleanup(cleanup);

    expect(cleanup).not.toHaveBeenCalled();
    await Promise.resolve();
    expect(cleanup).toHaveBeenCalledOnce();
  });

  it('preserves cached pages and still disposes exactly once on a later permanent departure', () => {
    const target = new EventTarget();
    const unmount = vi.fn();
    const disposeResources = vi.fn();
    const lifecycle = installReactRootLifecycle({ unmount, disposeResources, target: target as Window });
    target.dispatchEvent(hide(true));
    target.dispatchEvent(hide(true));
    expect(unmount).not.toHaveBeenCalled();
    expect(disposeResources).not.toHaveBeenCalled();
    target.dispatchEvent(hide(false));
    target.dispatchEvent(hide(false));
    lifecycle.dispose();
    expect(unmount).toHaveBeenCalledOnce();
    expect(disposeResources).toHaveBeenCalledOnce();
  });

  it('detaches HMR synchronously and defers one root cleanup after a cached departure', async () => {
    const target = new EventTarget();
    const unmount = vi.fn();
    const disposeResources = vi.fn();
    const lifecycle = installReactRootLifecycle({ unmount, disposeResources, target: target as Window });
    target.dispatchEvent(hide(true));
    lifecycle.disposeForHotReload();
    target.dispatchEvent(hide(false));
    lifecycle.disposeForHotReload();
    lifecycle.dispose();
    expect(disposeResources).toHaveBeenCalledOnce();
    expect(unmount).not.toHaveBeenCalled();
    await Promise.resolve();
    expect(unmount).toHaveBeenCalledOnce();
  });
});
