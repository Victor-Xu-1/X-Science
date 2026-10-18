import React from 'react';
import { MemoryRouter } from 'react-router';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/common', () => ({
  ipcBridge: {
    kernel: { executionCellUpdate: { on: () => () => {} } },
    compute: { jobUpdate: { on: () => () => {} }, jobLogChunk: { on: () => () => {} } },
    realtime: { reconnected: { on: () => () => {} } },
    fileStream: { contentUpdate: { on: () => () => {} } },
    fs: { getFileMetadata: { invoke: vi.fn() } },
  },
}));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: string }) => <div>{children}</div>,
}));
vi.mock('@/renderer/components/Markdown/CodeBlock', () => ({
  default: ({ children }: { children: string }) => <pre>{children}</pre>,
}));

import XScienceComputeRuntimePanel from '@/renderer/components/xScience/runtime/XScienceComputeRuntimePanel';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import XScienceNotebookViewer from '@/renderer/pages/conversation/Preview/components/viewers/XScienceNotebookViewer';

const target = {
  schema: 'x-science.manual-kernel-target.v1',
  frame_id: 'frame-1',
  root_frame_id: 'root-1',
  frame_incarnation_id: 'frame-incarnation-1',
  root_frame_incarnation_id: 'root-incarnation-1',
  kernel_id: 'kernel-1',
  kernel_generation: 7,
  language: 'python',
  environment: 'python',
};

function PreviewProbe() {
  const { activeTab, isOpen } = usePreviewContext();
  return <output data-testid='notebook-preview-state'>{JSON.stringify({ activeTab, isOpen })}</output>;
}

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => null);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('manual Notebook Preview Pane entry', () => {
  it('opens the selected kernel in the existing preview context without a modal or execution request', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      expect(init?.method ?? 'GET').toBe('GET');
      if (url.pathname !== '/api/kernels') throw new Error(`unexpected route: ${url.pathname}`);
      return new Response(
        JSON.stringify({
          has_history: true,
          kernels: [
            {
              frame_id: 'frame-1',
              root_frame_id: 'root-1',
              project_id: 'project-1',
              agent_name: 'OPERON',
              session_title: 'Notebook fixture',
              kernel_id: 'kernel-1',
              kind: 'analysis',
              language: 'python',
              environment: 'python',
              pid_visible: true,
              busy: false,
              starting: false,
              execution_count: 1,
              cell_count: 1,
              manual_execution_target: target,
            },
          ],
        }),
        { headers: { 'content-type': 'application/json' } }
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(
      <MemoryRouter>
        <PreviewProvider>
          <XScienceComputeRuntimePanel rootFrameId='root-1' projectId={null} sessionTitle='Notebook fixture' />
          <PreviewProbe />
        </PreviewProvider>
      </MemoryRouter>,
      'en-US'
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    fireEvent.click(await screen.findByRole('button', { name: 'Notebook' }));
    await waitFor(() => {
      const state = JSON.parse(screen.getByTestId('notebook-preview-state').textContent ?? '{}');
      expect(state).toMatchObject({
        isOpen: true,
        activeTab: {
          content_type: 'notebook',
          metadata: { notebookSession: { frameId: 'frame-1', rootFrameId: 'root-1' } },
        },
      });
      expect(state.activeTab.metadata.file_path).toBeUndefined();
      expect(state.activeTab.metadata.contentUrl).toBeUndefined();
    });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.every(([, init]) => (init?.method ?? 'GET') === 'GET')).toBe(true);
  });

  it('keeps arbitrary ipynb source and embedded target-shaped metadata read-only', async () => {
    const fetchMock = vi.fn(() => {
      throw new Error('a file preview must not submit code');
    });
    vi.stubGlobal('fetch', fetchMock);
    const content = JSON.stringify({
      nbformat: 4,
      nbformat_minor: 5,
      metadata: {
        kernelspec: { name: 'python3', language: 'python', display_name: 'Python' },
        manual_execution_target: target,
      },
      cells: [{ cell_type: 'code', execution_count: 1, source: ['print("readonly")'], outputs: [] }],
    });
    await renderWithI18n(<XScienceNotebookViewer filename='untrusted.ipynb' content={content} />, 'en-US');
    expect(await screen.findByText('print("readonly")')).toBeVisible();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /run|execute|cancel/i })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
