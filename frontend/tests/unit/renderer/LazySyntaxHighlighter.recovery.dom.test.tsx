import { act, cleanup, render, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const effects = vi.hoisted(() => ({ defer: false, pending: [] as Array<() => void> }));

// Hold passive effects to reproduce a cache completion between render and effect,
// without changing the component or inserting timing behavior in production.
vi.mock('react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react')>();
  return {
    ...actual,
    useEffect: (effect: React.EffectCallback, dependencies: React.DependencyList) =>
      actual.useEffect(() => {
        if (!effects.defer) return effect();
        let cleanupEffect: ReturnType<React.EffectCallback>;
        effects.pending.push(() => {
          cleanupEffect = effect();
        });
        return () => cleanupEffect?.();
      }, dependencies),
  };
});

const modulePath = '@/renderer/components/Markdown/CodeHighlighter';
const highlighterModule = () => ({
  default: ({ children }: { children?: React.ReactNode }) => <pre data-testid='syntax-highlighter'>{children}</pre>,
});

beforeEach(() => {
  vi.resetModules();
  effects.defer = false;
  effects.pending = [];
  vi.doMock(modulePath, highlighterModule);
});

afterEach(() => {
  cleanup();
  effects.defer = false;
  effects.pending = [];
  vi.restoreAllMocks();
});

describe('lazy highlighter recovery', () => {
  it('observes an import failure and permits a later mount to retry', async () => {
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const failedLoad = vi.fn(() => {
      throw new Error('controlled chunk failure');
    });
    vi.doMock(modulePath, failedLoad);
    const { default: Highlighter } = await import('@/renderer/components/Markdown/LazySyntaxHighlighter');
    const first = render(<Highlighter>{'retained source'}</Highlighter>);
    await vi.dynamicImportSettled();
    expect(failedLoad).toHaveBeenCalledOnce();
    expect(warning).toHaveBeenCalledOnce();
    expect(first.container).toHaveTextContent('retained source');
    first.unmount();

    vi.doMock(modulePath, highlighterModule);
    const second = render(<Highlighter>{'retry source'}</Highlighter>);
    await waitFor(() => {
      expect(second.container.querySelector('[data-testid="syntax-highlighter"]')).toHaveTextContent('retry source');
    });
  });

  it('installs a shared cache completed after render but before its passive effect', async () => {
    const { default: Highlighter, preloadSyntaxHighlighter } =
      await import('@/renderer/components/Markdown/LazySyntaxHighlighter');
    effects.defer = true;
    const view = render(<Highlighter>{'late shared cache'}</Highlighter>);
    expect(view.container.querySelector('[data-testid="syntax-highlighter"]')).toBeNull();

    preloadSyntaxHighlighter();
    await vi.dynamicImportSettled();
    await act(async () => {
      effects.defer = false;
      for (const effect of effects.pending.splice(0)) effect();
    });
    expect(view.container.querySelector('[data-testid="syntax-highlighter"]')).toHaveTextContent('late shared cache');
  });
});
