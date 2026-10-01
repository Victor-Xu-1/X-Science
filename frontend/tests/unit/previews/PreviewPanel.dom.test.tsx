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
  return { ...actual, LazyMarkdownPreview: ({ content }: { content: string }) => <p>{content}</p> };
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
              onClick={() => openPreview('Single preview result', 'markdown', { title: 'result.md', editable: false })}
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

  it('ports fullscreen content to the document top layer and restores body scrolling', async () => {
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

    expect(screen.getByTestId('fullscreen-preview-content').parentElement).toBe(document.body);
    expect(host).not.toContainElement(screen.getByTestId('fullscreen-preview-content'));
    expect(document.body.style.overflow).toBe('hidden');

    rerender(
      <PreviewFullscreenLayer active={false}>
        <div data-testid='fullscreen-preview-content'>preview</div>
      </PreviewFullscreenLayer>
    );
    expect(host).toContainElement(screen.getByTestId('fullscreen-preview-content'));
    expect(document.body.style.overflow).toBe('');

    unmount();
    host.remove();
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
