import React, { Suspense } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { openNotebookPreview } from '@/renderer/services/notebook/notebookPreviewNavigation';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import NotebookPreviewContent from '@/renderer/pages/conversation/Preview/components/notebook/NotebookPreviewContent';
import { owner } from './manualNotebookFixtures';

vi.mock('@/common', () => ({ ipcBridge: { fs: { getFileMetadata: { invoke: vi.fn() } } } }));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: string }) => <div>{children}</div>,
}));
vi.mock('@/renderer/components/Markdown/CodeBlock', () => ({
  default: ({ children }: { children: string }) => <pre>{children}</pre>,
}));

beforeEach(() => {
  localStorage.clear();
  setRendererAccountOwner(owner);
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
});
const ipynb = JSON.stringify({
  nbformat: 4,
  nbformat_minor: 5,
  metadata: {},
  cells: [{ cell_type: 'code', execution_count: 1, source: ['print("file remains readonly")'], outputs: [] }],
});

function Controls() {
  const preview = usePreviewContext();
  const open = (frameId: string, kernelId: string) =>
    openNotebookPreview(preview, { frameId, rootFrameId: 'root-1', kernelId }, 'Notebook');
  return (
    <>
      <button onClick={() => open('frame-1', 'kernel-1')}>First kernel</button>
      <button onClick={() => open('frame-1', 'kernel-2')}>Other kernel, same frame</button>
      <button onClick={() => open('frame-2', 'kernel-3')}>Other frame</button>
      <button onClick={() => preview.openPreview(ipynb, 'notebook', { title: 'Notebook', file_name: 'Notebook' })}>
        File
      </button>
      <output data-testid='tabs'>{JSON.stringify({ tabs: preview.tabs, active: preview.activeTab })}</output>
    </>
  );
}
describe('live Notebook preview identity', () => {
  it('separates file and real frame identities while explicitly updating a reused frame target', async () => {
    await renderWithI18n(
      <PreviewProvider>
        <Controls />
      </PreviewProvider>,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'First kernel' }));
    const first = JSON.parse(screen.getByTestId('tabs').textContent ?? '{}').active.id;
    fireEvent.click(screen.getByRole('button', { name: 'File', exact: true }));
    fireEvent.click(screen.getByRole('button', { name: 'Other frame' }));
    expect(JSON.parse(screen.getByTestId('tabs').textContent ?? '{}').tabs).toHaveLength(3);
    fireEvent.click(screen.getByRole('button', { name: 'Other kernel, same frame' }));
    const state = JSON.parse(screen.getByTestId('tabs').textContent ?? '{}');
    expect(state.tabs).toHaveLength(3);
    expect(state.active.id).toBe(first);
    expect(state.active.metadata.notebookSession.preferredKernelId).toBe('kernel-2');
    expect(state.active.metadata.notebookSession.focusRequest).toBeTruthy();
    act(() => {
      setRendererAccountOwner('next-owner');
    });
    await waitFor(() => {
      const next = JSON.parse(screen.getByTestId('tabs').textContent ?? '{}');
      expect(next.tabs).toHaveLength(1);
      expect(next.tabs[0].metadata.notebookSession).toBeUndefined();
    });
  });

  it('does not accept a target-shaped backend artifact metadata object as live navigation', async () => {
    const fetchMock = vi.fn(() => {
      throw new Error('untrusted metadata cannot open live runtime');
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(
      <Suspense fallback={<p>Loading</p>}>
        <NotebookPreviewContent
          filename='metadata.ipynb'
          content={ipynb}
          metadata={{
            notebookSession: { frameId: 'frame-1', rootFrameId: 'root-1', preferredKernelId: 'kernel-1' },
          }}
        />
      </Suspense>,
      'en-US'
    );
    expect(await screen.findByText('print("file remains readonly")')).toBeVisible();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
