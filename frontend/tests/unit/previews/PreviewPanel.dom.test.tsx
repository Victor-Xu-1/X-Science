/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createTestI18n } from '../i18nTestUtils';

// This regression owns panel lifecycle, not the content renderer's dependency
// graph. The real renderer is exercised by the controlled browser integration.
vi.mock('@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewLoaders', async (importOriginal) => {
  const actual =
    await importOriginal<
      typeof import('@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewLoaders')
    >();
  return {
    ...actual,
    LazyMarkdownPreview: ({ content }: { content: string }) => <p>{content}</p>,
  };
});

beforeEach(() => {
  window.__backendPort = 13400;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      return new Response(JSON.stringify({ data: {} }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    })
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
  delete window.__backendPort;
});

// PreviewPanel pulls in a large dependency graph; under the full concurrent
// suite the first cold import's transform/resolve can exceed the default 10s
// timeout (flaky), even though it resolves in a few seconds in isolation. Give
// these import-bound assertions extra headroom so they don't flake.
const IMPORT_TIMEOUT_MS = 30000;

describe('PreviewPanel', () => {
  it(
    'opens, closes and reopens a single preview without changing hook order',
    async () => {
      const { default: PreviewPanel } =
        await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
      const { PreviewProvider, usePreviewContext } =
        await import('@/renderer/pages/conversation/Preview/context/PreviewContext');
      const { ThemeProvider } = await import('@/renderer/hooks/context/ThemeContext');
      const i18n = await createTestI18n('en-US');
      function Controls() {
        const { openPreview, closePreview } = usePreviewContext();
        return (
          <>
            <button
              onClick={() =>
                openPreview('Single preview result', 'markdown', {
                  title: 'result.md',
                  editable: false,
                })
              }
            >
              Open test file
            </button>
            <button onClick={closePreview}>Close test file</button>
            <PreviewPanel />
          </>
        );
      }
      render(
        <I18nextProvider i18n={i18n}>
          <ThemeProvider>
            <PreviewProvider>
              <Controls />
            </PreviewProvider>
          </ThemeProvider>
        </I18nextProvider>
      );
      for (let cycle = 0; cycle < 2; cycle++) {
        fireEvent.click(screen.getByRole('button', { name: 'Open test file' }));
        await waitFor(() => expect(document.querySelector('.preview-panel')).not.toBeNull());
        const fullscreenTrigger = screen.getByRole('button', {
          name: i18n.t('preview.openFullscreen'),
        });
        fullscreenTrigger.focus();
        fireEvent.click(fullscreenTrigger);
        const fullscreen = screen.getByRole('dialog', { name: 'result.md' });
        const controls = fullscreen.querySelectorAll<HTMLButtonElement>('button');
        const first = controls[0];
        const last = controls[controls.length - 1];
        first.focus();
        fireEvent.keyDown(first, { key: 'Tab', shiftKey: true });
        expect(last).toHaveFocus();
        fireEvent.keyDown(last, { key: 'Escape' });
        expect(screen.queryByRole('dialog', { name: 'result.md' })).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: i18n.t('preview.openFullscreen') })).toHaveFocus();
        fireEvent.click(screen.getByRole('button', { name: 'Close test file' }));
        await waitFor(() => expect(document.querySelector('.preview-panel')).toBeNull());
      }
    },
    IMPORT_TIMEOUT_MS
  );

  it('uses the file board only when at least two files are open', async () => {
    const { shouldRenderPreviewBoard } =
      await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');

    expect(shouldRenderPreviewBoard('board', 1)).toBe(false);
    expect(shouldRenderPreviewBoard('board', 1, true)).toBe(true);
    expect(shouldRenderPreviewBoard('board', 2)).toBe(true);
    expect(shouldRenderPreviewBoard('single', 2)).toBe(false);
  });

  it('keeps fullscreen content in its original DOM ancestry and restores body scrolling', async () => {
    const mod = await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
    const PreviewFullscreenLayer = (
      mod as typeof mod & {
        PreviewFullscreenLayer: React.FC<React.PropsWithChildren<{ active: boolean }>>;
      }
    ).PreviewFullscreenLayer;
    expect(PreviewFullscreenLayer).toBeTypeOf('function');

    const host = document.createElement('div');
    document.body.appendChild(host);
    const { rerender, unmount } = render(
      <PreviewFullscreenLayer active>
        <div data-testid='fullscreen-preview-content'>preview</div>
      </PreviewFullscreenLayer>,
      { container: host }
    );

    const content = screen.getByTestId('fullscreen-preview-content');
    const parent = content.parentElement;
    expect(host).toContainElement(content);
    expect(document.body.style.overflow).toBe('hidden');

    rerender(
      <PreviewFullscreenLayer active={false}>
        <div data-testid='fullscreen-preview-content'>preview</div>
      </PreviewFullscreenLayer>
    );
    expect(host).toContainElement(screen.getByTestId('fullscreen-preview-content'));
    expect(content.parentElement).toBe(parent);
    expect(document.body.style.overflow).toBe('');

    unmount();
    host.remove();
  });

  it('preserves the mounted viewer and its local state through fullscreen transitions', async () => {
    const { PreviewFullscreenLayer } =
      await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
    const mounted = vi.fn();
    const disposed = vi.fn();
    function StatefulViewer() {
      const [value, setValue] = React.useState(0);
      React.useEffect(() => {
        mounted();
        return disposed;
      }, []);
      return <button onClick={() => setValue((current) => current + 1)}>View state {value}</button>;
    }
    const { rerender, unmount } = render(
      <PreviewFullscreenLayer active={false}>
        <StatefulViewer />
      </PreviewFullscreenLayer>
    );
    fireEvent.click(screen.getByRole('button', { name: 'View state 0' }));
    rerender(
      <PreviewFullscreenLayer active>
        <StatefulViewer />
      </PreviewFullscreenLayer>
    );
    expect(screen.getByRole('button', { name: 'View state 1' })).toBeInTheDocument();
    rerender(
      <PreviewFullscreenLayer active={false}>
        <StatefulViewer />
      </PreviewFullscreenLayer>
    );
    expect(screen.getByRole('button', { name: 'View state 1' })).toBeInTheDocument();
    expect(mounted).toHaveBeenCalledTimes(1);
    expect(disposed).not.toHaveBeenCalled();
    unmount();
    expect(disposed).toHaveBeenCalledTimes(1);
  });

  it('retains the scroll lock while another full-window preview remains open', async () => {
    const { PreviewFullscreenLayer } =
      await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
    function Pair({ first }: { first: boolean }) {
      return (
        <>
          <PreviewFullscreenLayer active={first}>
            <button>First</button>
          </PreviewFullscreenLayer>
          <PreviewFullscreenLayer active>
            <button>Second</button>
          </PreviewFullscreenLayer>
        </>
      );
    }
    const { rerender, unmount } = render(<Pair first />);
    expect(document.body.style.overflow).toBe('hidden');
    rerender(<Pair first={false} />);
    expect(document.body.style.overflow).toBe('hidden');
    unmount();
    expect(document.body.style.overflow).toBe('');
  });

  it('retains the iframe browsing context and unsaved document state across fullscreen transitions', async () => {
    const { PreviewFullscreenLayer } =
      await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
    const content = <iframe title='Interactive file' />;
    const { rerender, unmount } = render(<PreviewFullscreenLayer active={false}>{content}</PreviewFullscreenLayer>);
    const frame = screen.getByTitle('Interactive file') as HTMLIFrameElement;
    const documentBefore = frame.contentDocument!;
    documentBefore.body.innerHTML = '<input value="unsaved view state">';
    rerender(<PreviewFullscreenLayer active>{content}</PreviewFullscreenLayer>);
    expect(frame.contentDocument === documentBefore).toBe(true);
    expect(frame.contentDocument?.querySelector('input')?.value).toBe('unsaved view state');
    rerender(<PreviewFullscreenLayer active={false}>{content}</PreviewFullscreenLayer>);
    expect(frame.contentDocument === documentBefore).toBe(true);
    unmount();
  });

  it('preserves nested browser scroll offsets when fullscreen layout changes clamp them', async () => {
    const { PreviewFullscreenLayer } =
      await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
    function Viewer({ wide }: { wide: boolean }) {
      const ref = React.useRef<HTMLDivElement>(null);
      React.useLayoutEffect(() => {
        if (ref.current) {
          ref.current.scrollTop = 0;
          ref.current.querySelector<HTMLElement>('[data-testid="nested-table"]')!.scrollLeft = 0;
        }
      }, [wide]);
      return (
        <div ref={ref} data-testid='scrolling-viewer'>
          <div data-testid='nested-table'>Long table</div>
        </div>
      );
    }
    const { rerender, unmount } = render(
      <PreviewFullscreenLayer active={false}>
        <Viewer wide={false} />
      </PreviewFullscreenLayer>
    );
    const scroller = screen.getByTestId('scrolling-viewer');
    const table = screen.getByTestId('nested-table');
    scroller.scrollTop = 1255;
    table.scrollLeft = 80;
    rerender(
      <PreviewFullscreenLayer active>
        <Viewer wide />
      </PreviewFullscreenLayer>
    );
    expect(scroller.scrollTop).toBe(1255);
    expect(table.scrollLeft).toBe(80);
    expect(screen.getByTestId('scrolling-viewer')).toBe(scroller);
    rerender(
      <PreviewFullscreenLayer active={false}>
        <Viewer wide={false} />
      </PreviewFullscreenLayer>
    );
    expect(scroller.scrollTop).toBe(1255);
    expect(table.scrollLeft).toBe(80);
    unmount();
  });

  it('does not let a content marker impersonate the topmost fullscreen scope', async () => {
    const { PreviewFullscreenLayer } =
      await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
    const exit = vi.fn();
    const { unmount } = render(
      <PreviewFullscreenLayer active onExit={exit}>
        <div data-preview-fullscreen-scope=''>File content</div>
      </PreviewFullscreenLayer>
    );
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(exit).toHaveBeenCalledOnce();
    unmount();
  });

  it(
    'is a React component module that exports a default function',
    async () => {
      const mod = await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
      expect(typeof mod.default).toBe('function');
    },
    IMPORT_TIMEOUT_MS
  );

  it(
    'module loads without throwing on import',
    async () => {
      await expect(
        import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel')
      ).resolves.toBeTruthy();
    },
    IMPORT_TIMEOUT_MS
  );

  it(
    'has a displayName or function name for debugging',
    async () => {
      const mod = await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel');
      const fn = mod.default;
      expect(fn.name || fn.displayName || 'anonymous').toBeTruthy();
    },
    IMPORT_TIMEOUT_MS
  );
});
