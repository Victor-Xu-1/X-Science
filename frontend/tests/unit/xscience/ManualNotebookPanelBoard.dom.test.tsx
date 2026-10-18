import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import { notebookFixture, submissions } from './manualNotebookControllerFixture';
import { owner, rawTarget } from './manualNotebookFixtures';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { openNotebookPreview } from '@/renderer/services/notebook/notebookPreviewNavigation';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';

const events = vi.hoisted(() => ({ wakes: new Set<(event: unknown) => void>() }));
vi.mock('@/common', () => ({
  ipcBridge: {
    kernel: {
      executionCellUpdate: {
        on: (listener: (event: unknown) => void) => {
          events.wakes.add(listener);
          return () => events.wakes.delete(listener);
        },
      },
    },
    realtime: { reconnected: { on: () => () => {} } },
    fileStream: { contentUpdate: { on: () => () => {} } },
    preview: { open: { on: () => () => {} } },
    fs: { getFileMetadata: { invoke: vi.fn() } },
  },
}));
// Theme configuration is not an execution/preview authority; preserve real Panel, Board and Notebook consumers.
vi.mock('@/renderer/hooks/context/ThemeContext', () => ({ useThemeContext: () => ({ theme: 'light' }) }));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: string }) => <div>{children}</div>,
}));
vi.mock('@/renderer/components/Markdown/CodeBlock', () => ({
  default: ({ children }: { children: string }) => <pre>{children}</pre>,
}));

let PreviewPanel: React.ComponentType;
let PreviewBoard: React.ComponentType;
function Consumer({ board }: { board: boolean }) {
  const preview = usePreviewContext();
  const open = (kernelId: string) =>
    openNotebookPreview(
      {
        openPreview: (content, type, metadata, options) =>
          preview.openPreview(content, type, metadata, { ...options, presentation: board ? 'board' : 'single' }),
      },
      { frameId: 'frame-1', rootFrameId: 'root-1', kernelId },
      'Notebook'
    );
  return (
    <>
      <button onClick={() => open('kernel-1')}>Open {board ? 'board' : 'panel'} Notebook</button>
      <button onClick={() => open('kernel-2')}>Select {board ? 'board' : 'panel'} second kernel</button>
      <button onClick={preview.closePreview}>Close {board ? 'board' : 'panel'} Notebook</button>
      {board ? <PreviewBoard /> : <PreviewPanel />}
    </>
  );
}
beforeEach(async () => {
  localStorage.clear();
  setRendererAccountOwner(owner);
  Element.prototype.scrollIntoView = vi.fn();
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => null);
  if (!PreviewPanel)
    PreviewPanel = (await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel')).default;
  if (!PreviewBoard)
    PreviewBoard = (await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewBoard')).default;
}, 30000);
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('real Preview Panel and Board Notebook consumers', () => {
  it('share one owner and select the newly requested target instead of executing the previous kernel', async () => {
    const fixture = notebookFixture();
    fixture.targets = [
      rawTarget,
      { ...rawTarget, kernel_id: 'kernel-2', kernel_generation: 8, environment: 'analysis' },
    ];
    await renderWithI18n(
      <>
        <PreviewProvider scope='embedded'>
          <Consumer board={false} />
        </PreviewProvider>
        <PreviewProvider scope='embedded'>
          <Consumer board />
        </PreviewProvider>
      </>,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Open panel Notebook' }));
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Code' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Open board Notebook' }));
    await waitFor(() => expect(screen.getAllByRole('textbox', { name: 'Code' })).toHaveLength(2));
    expect(events.wakes.size).toBe(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.change(screen.getAllByRole('textbox', { name: 'Code' })[0], { target: { value: 'old target draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'Select panel second kernel' }));
    await waitFor(() =>
      screen.getAllByRole('textbox', { name: 'Code' }).forEach((input) => expect(input).toHaveValue(''))
    );
    fireEvent.change(screen.getAllByRole('textbox', { name: 'Code' })[1], { target: { value: 'print("new target")' } });
    const runs = screen.getAllByRole('button', { name: 'Run', exact: true });
    fireEvent.click(runs[0]);
    fireEvent.click(runs[1]);
    await waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body));
    expect(body).toMatchObject({
      kernel_id: 'kernel-2',
      expected_kernel_generation: 8,
      environment: 'analysis',
      code: 'print("new target")',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Close panel Notebook' }));
    expect(events.wakes.size).toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'Close board Notebook' }));
    await waitFor(() => expect(events.wakes.size).toBe(0));
    expect(fixture.fetch.mock.calls.some(([input]) => String(input).endsWith('/cancel'))).toBe(false);
    act(() => {
      setRendererAccountOwner('next-account');
    });
    expect(screen.queryByTestId('live-notebook-preview')).not.toBeInTheDocument();
  }, 30000);
});
