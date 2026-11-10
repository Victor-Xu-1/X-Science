/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import React, { useEffect, useRef, useState } from 'react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createTestI18n } from '../i18nTestUtils';
import { XScienceHttpError } from '@/renderer/services/xScienceHttp';

const commonMocks = vi.hoisted(() => ({
  writeFileInvoke: vi.fn(),
  createArtifactVersion: vi.fn(),
  loadArtifact: vi.fn(),
  loadVersionText: vi.fn(),
}));

let PreviewBoardComponent: React.ComponentType;

vi.mock('@/common', () => ({
  ipcBridge: {
    fileStream: { contentUpdate: { on: vi.fn(() => vi.fn()) } },
    preview: { open: { on: vi.fn(() => vi.fn()) } },
    fs: {
      writeFile: { invoke: commonMocks.writeFileInvoke },
      getFileMetadata: { invoke: vi.fn() },
      readFile: { invoke: vi.fn() },
      getImageBase64: { invoke: vi.fn() },
    },
    theme: {
      setActive: { invoke: vi.fn().mockResolvedValue(undefined) },
      changed: { on: vi.fn(() => vi.fn()) },
    },
  },
}));

vi.mock('@/renderer/pages/conversation/Preview/components/editors/DocumentWysiwygEditor', () => ({
  default: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => (
    <textarea aria-label='Board file editor' value={value} onChange={(event) => onChange(event.target.value)} />
  ),
}));

vi.mock('@/renderer/services/xScienceArtifacts', () => ({
  createXScienceTextArtifactVersion: commonMocks.createArtifactVersion,
  getXScienceArtifactVersionContentUrl: (versionId: string) => `/api/artifact-versions/${versionId}/content`,
  loadXScienceArtifactVersionText: commonMocks.loadVersionText,
}));
// The board regression owns the metadata read boundary, not the unrelated
// gateway module's runtime/dependency initialization.
vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceArtifact: commonMocks.loadArtifact,
}));

vi.mock('@/renderer/utils/emitter', () => ({
  emitter: { on: vi.fn(), off: vi.fn() },
}));

const BoardHarness: React.FC = () => {
  const context = usePreviewContext();
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    context.openPreview(
      '',
      'code',
      {
        file_name: 'analysis.py',
        file_path: '/project/analysis.py',
        missingFile: true,
      },
      {
        presentation: 'board',
      }
    );
    context.openPreview(
      '',
      'image',
      {
        file_name: 'result.png',
        file_path: '/project/result.png',
        missingFile: true,
      },
      {
        presentation: 'board',
      }
    );
  }, [context]);

  const PreviewBoard = PreviewBoardComponent;
  return <PreviewBoard />;
};

const EditableBoardHarness: React.FC = () => {
  const context = usePreviewContext();
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    context.openPreview(
      '# Initial note',
      'markdown',
      {
        file_name: 'notes.md',
        file_path: 'notes.md',
        workspace: '/project',
        editable: true,
      },
      { presentation: 'board' }
    );
  }, [context]);

  const PreviewBoard = PreviewBoardComponent;
  return <PreviewBoard />;
};

const RemoteEditableBoardHarness: React.FC = () => {
  const context = usePreviewContext();
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    context.openPreview(
      '# Published note',
      'markdown',
      {
        file_name: 'published.md',
        artifactId: 'artifact-1',
        versionId: 'version-1',
        contentUrl: '/api/artifact-versions/version-1/content',
        editable: true,
      },
      { presentation: 'board' }
    );
  }, [context]);

  const PreviewBoard = PreviewBoardComponent;
  return <PreviewBoard />;
};

const ScopedEditableFile: React.FC<{ projectId: string }> = ({ projectId }) => {
  const { openPreview } = usePreviewContext();
  const initialized = useRef(new Set<string>());
  useEffect(() => {
    if (initialized.current.has(projectId)) return;
    initialized.current.add(projectId);
    openPreview(
      `# Project ${projectId}`,
      'markdown',
      {
        projectId,
        file_name: `${projectId}.md`,
        file_path: `${projectId}.md`,
        workspace: `/project/${projectId}`,
        editable: true,
      },
      { presentation: 'board' }
    );
  }, [openPreview, projectId]);
  return null;
};

const ProjectSaveScopeHarness: React.FC = () => {
  const [projectId, setProjectId] = useState('b');
  const PreviewBoard = PreviewBoardComponent;
  return (
    <PreviewProvider workspace={{ ownerId: 'save-owner', routeKey: 'same-conversation-route', projectId }}>
      <button onClick={() => setProjectId('a')}>Project A</button>
      <button onClick={() => setProjectId('b')}>Project B</button>
      <ScopedEditableFile projectId={projectId} />
      <PreviewBoard />
    </PreviewProvider>
  );
};

function deferredWrite() {
  let resolve!: (success: boolean) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<boolean>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

describe('PreviewBoard', () => {
  beforeEach(async () => {
    localStorage.clear();
    commonMocks.writeFileInvoke.mockReset().mockResolvedValue(true);
    commonMocks.createArtifactVersion
      .mockReset()
      .mockResolvedValue({ artifactId: 'artifact-1', versionId: 'version-2' });
    commonMocks.loadArtifact.mockReset().mockResolvedValue({ artifactId: 'artifact-1', versionId: 'version-1' });
    commonMocks.loadVersionText.mockReset().mockResolvedValue('# New publication');
    Element.prototype.scrollIntoView = vi.fn();
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({ matches: false }))
    );
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ data: {} }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          })
      )
    );
    if (!PreviewBoardComponent) {
      PreviewBoardComponent = (
        await import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewBoard')
      ).default;
    }
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it.each(['resolve', 'reject'] as const)(
    'does not publish a save %s after StrictMode board unmount',
    async (outcome) => {
      const write = deferredWrite();
      commonMocks.writeFileInvoke.mockReturnValueOnce(write.promise);
      const i18n = await createTestI18n('en-US');
      const view = render(
        <React.StrictMode>
          <I18nextProvider i18n={i18n}>
            <PreviewProvider>
              <EditableBoardHarness />
            </PreviewProvider>
          </I18nextProvider>
        </React.StrictMode>
      );
      await screen.findByText('notes.md');
      fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
      const editor = await screen.findByRole('textbox', { name: 'Board file editor' });
      fireEvent.change(editor, { target: { value: '# Pending before unmount' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
      await waitFor(() => expect(commonMocks.writeFileInvoke).toHaveBeenCalledTimes(1));
      expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();

      view.unmount();
      await act(async () => {
        if (outcome === 'resolve') write.resolve(true);
        else write.reject(new Error('write-rejected-after-unmount'));
        await write.promise.catch((): void => undefined);
      });
      expect(screen.queryByTestId('preview-board')).not.toBeInTheDocument();
      expect(screen.queryByText(i18n.t('preview.board.saveSucceeded'))).not.toBeInTheDocument();
      expect(screen.queryByText(i18n.t('preview.board.saveFailed'))).not.toBeInTheDocument();
    }
  );

  it.each(['resolve', 'reject'] as const)(
    'ignores an old project save %s while the restored project is saving',
    async (outcome) => {
      const writeA = deferredWrite();
      const writeB = deferredWrite();
      commonMocks.writeFileInvoke.mockImplementation(({ workspace }: { workspace: string }) =>
        workspace === '/project/a' ? writeA.promise : writeB.promise
      );
      const i18n = await createTestI18n('en-US');
      render(
        <I18nextProvider i18n={i18n}>
          <ProjectSaveScopeHarness />
        </I18nextProvider>
      );

      // Visit B first so returning from A restores an already-open board rather
      // than remounting the consumer or creating B only after A has finished.
      await screen.findByText('b.md');
      fireEvent.click(screen.getByRole('button', { name: 'Project A' }));
      await screen.findByText('a.md');
      fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
      const editorA = await screen.findByRole('textbox', { name: 'Board file editor' });
      fireEvent.change(editorA, { target: { value: '# Unsaved A' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
      await waitFor(() => expect(commonMocks.writeFileInvoke).toHaveBeenCalledTimes(1));
      const boardInstance = screen.getByTestId('preview-board');

      fireEvent.click(screen.getByRole('button', { name: 'Project B' }));
      await screen.findByText('b.md');
      expect(screen.getByTestId('preview-board')).toBe(boardInstance);
      fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
      const editorB = await screen.findByRole('textbox', { name: 'Board file editor' });
      fireEvent.change(editorB, { target: { value: '# Unsaved B' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
      await waitFor(() => expect(commonMocks.writeFileInvoke).toHaveBeenCalledTimes(2));
      expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();

      await act(async () => {
        if (outcome === 'resolve') writeA.resolve(true);
        else writeA.reject(new Error('project-a-write-failed'));
        await writeA.promise.catch((): void => undefined);
      });
      expect.soft(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
      expect.soft(editorB).toBeInTheDocument();
      expect.soft(editorB).toHaveValue('# Unsaved B');
      expect.soft(screen.queryByText(i18n.t('preview.board.saveSucceeded'))).not.toBeInTheDocument();
      expect.soft(screen.queryByText(i18n.t('preview.board.saveFailed'))).not.toBeInTheDocument();

      await act(async () => {
        writeB.resolve(true);
        await writeB.promise;
      });
      await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Board file editor' })).not.toBeInTheDocument());
      expect(boardInstance.querySelector('span[role="status"][aria-live="polite"]')).toHaveTextContent(
        i18n.t('preview.board.saveSucceeded')
      );
    }
  );

  it('promotes one project file to fullscreen without reflowing the other tile', async () => {
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <BoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );

    await waitFor(() => expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2), { timeout: 3000 });
    expect(screen.getByText('analysis.py')).toBeInTheDocument();
    expect(screen.getByText('result.png')).toBeInTheDocument();
    expect(screen.getByText('2 files open')).toBeInTheDocument();
    const slots = screen.getAllByTestId('preview-board-slot');
    const sibling = screen.getAllByTestId('preview-board-tile')[1];

    fireEvent.click(screen.getAllByRole('button', { name: 'Expand preview' })[0]);
    expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2);
    expect(screen.getByTestId('preview-board-fullscreen-layer')).toBeInTheDocument();
    expect(screen.getByTestId('preview-board').querySelectorAll('[data-testid="preview-board-tile"]')).toHaveLength(2);
    expect(screen.getAllByTestId('preview-board-slot')).toEqual(slots);
    expect(slots[1]).toContainElement(sibling);
    expect(document.body).toHaveClass('workbench-preview-scroll-lock');

    fireEvent.click(screen.getByRole('button', { name: 'Return to file board' }));
    expect(screen.queryByTestId('preview-board-fullscreen-layer')).not.toBeInTheDocument();
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
    expect(screen.getByTestId('preview-board').querySelectorAll('[data-testid="preview-board-tile"]')).toHaveLength(2);
    expect(document.body.style.overflow).toBe('');

    fireEvent.click(screen.getAllByRole('button', { name: 'Expand preview' })[0]);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('preview-board-fullscreen-layer')).not.toBeInTheDocument();
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
    expect(document.body.style.overflow).toBe('');

    fireEvent.click(screen.getAllByRole('button', { name: 'Close file' })[0]);
    expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(1);
    expect(screen.getByText('1 files open')).toBeInTheDocument();
  });
  it('opens the complete file board in fullscreen without collapsing its tiles', async () => {
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <BoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );

    await waitFor(() => expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2));
    expect(screen.queryByTestId('preview-board-fullscreen-layer')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Open file board fullscreen' }));
    const layer = screen.getByTestId('preview-board-fullscreen-layer');
    expect(layer).toContainElement(screen.getByTestId('preview-board'));
    expect(layer.querySelectorAll('[data-testid="preview-board-tile"]')).toHaveLength(2);
    expect(document.body).toHaveClass('workbench-preview-scroll-lock');

    fireEvent.click(screen.getByRole('button', { name: 'Exit file board fullscreen' }));
    expect(screen.queryByTestId('preview-board-fullscreen-layer')).not.toBeInTheDocument();
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
    expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2);
    expect(document.body.style.overflow).toBe('');

    fireEvent.click(screen.getByRole('button', { name: 'Open file board fullscreen' }));
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('preview-board-fullscreen-layer')).not.toBeInTheDocument();
    expect(document.body).not.toHaveClass('workbench-preview-scroll-lock');
    expect(document.body.style.overflow).toBe('');
  });

  it('persists the selected canvas column count across remounts', async () => {
    const i18n = await createTestI18n('en-US');
    const renderBoard = () =>
      render(
        <I18nextProvider i18n={i18n}>
          <PreviewProvider workspace={{ ownerId: 'board-owner', routeKey: 'board-route', projectId: 'board-project' }}>
            <BoardHarness />
          </PreviewProvider>
        </I18nextProvider>
      );

    const firstRender = renderBoard();
    await waitFor(() => expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2));
    expect(screen.getByTestId('preview-board')).toHaveAttribute('data-columns', '2');
    expect(screen.getByTestId('preview-board')).toHaveStyle({ '--preview-board-columns': '2' });

    fireEvent.click(screen.getByRole('button', { name: 'Single column' }));
    expect(screen.getByTestId('preview-board')).toHaveAttribute('data-columns', '1');
    expect(screen.getByTestId('preview-board')).toHaveStyle({ '--preview-board-columns': '1' });
    expect(screen.getByRole('button', { name: 'Single column' })).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(screen.getByRole('button', { name: 'Up to three columns' }));
    expect(screen.getByTestId('preview-board')).toHaveAttribute('data-columns', '3');
    expect(screen.getByTestId('preview-board')).toHaveStyle({ '--preview-board-columns': '3' });
    expect(screen.getByRole('button', { name: 'Up to three columns' })).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(screen.getByRole('button', { name: 'Single column' }));

    firstRender.unmount();
    renderBoard();
    await waitFor(() => expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2));
    expect(screen.getByTestId('preview-board')).toHaveAttribute('data-columns', '1');
  });

  it('avoids smooth auto-scrolling when reduced motion is requested', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({ matches: true }))
    );
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <BoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );

    await waitFor(() => expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2));
    await waitFor(() =>
      expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({
        block: 'nearest',
        inline: 'nearest',
        behavior: 'auto',
      })
    );
  });

  it('edits one text tile and writes the saved content back to its workspace file', async () => {
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <EditableBoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );

    await waitFor(() => expect(screen.getByText('notes.md')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Board file editor' })).toBeInTheDocument());
    fireEvent.change(screen.getByRole('textbox', { name: 'Board file editor' }), {
      target: { value: '# Updated note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    await waitFor(() =>
      expect(commonMocks.writeFileInvoke).toHaveBeenCalledWith({
        path: 'notes.md',
        data: '# Updated note',
        workspace: '/project',
      })
    );
    expect(screen.queryByRole('textbox', { name: 'Board file editor' })).not.toBeInTheDocument();
  });

  it('saves remote text edits as a child artifact version instead of overwriting history', async () => {
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <RemoteEditableBoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );

    await waitFor(() => expect(screen.getByText('published.md')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Board file editor' })).toBeInTheDocument());
    fireEvent.change(screen.getByRole('textbox', { name: 'Board file editor' }), {
      target: { value: '# Revised published note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));

    await waitFor(() =>
      expect(commonMocks.createArtifactVersion).toHaveBeenCalledWith({
        artifactId: 'artifact-1',
        content: '# Revised published note',
        contentType: 'text/markdown',
        parentVersionId: 'version-1',
        expectedHeadVersionId: 'version-1',
        operationId: expect.any(String),
      })
    );
    expect(commonMocks.writeFileInvoke).not.toHaveBeenCalled();
    expect(screen.queryByRole('textbox', { name: 'Board file editor' })).not.toBeInTheDocument();
  });

  it('keeps the draft on conflict and opens the newer publication as a separate tile', async () => {
    commonMocks.createArtifactVersion.mockRejectedValueOnce(
      new XScienceHttpError({
        path: '/api/artifacts/artifact-1/versions',
        status: 409,
        body: {
          code: 'ARTIFACT_HEAD_CONFLICT',
          details: { artifact_id: 'artifact-1', current_head_version_id: 'version-2' },
        },
      })
    );
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <RemoteEditableBoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );
    await waitFor(() => expect(screen.getByText('published.md')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
    const editor = await screen.findByRole('textbox', { name: 'Board file editor' });
    fireEvent.change(editor, { target: { value: '# My unsaved draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await screen.findByText('A newer version is available. Your draft is kept and has not been published.');
    expect(editor).toHaveValue('# My unsaved draft');
    fireEvent.click(screen.getByRole('button', { name: 'Open newer version' }));
    await waitFor(() => expect(screen.getAllByTestId('preview-board-tile')).toHaveLength(2));
    expect(editor).toHaveValue('# My unsaved draft');
    expect(commonMocks.createArtifactVersion).toHaveBeenCalledTimes(1);
  });

  it('preserves edits typed while a save acknowledgement is pending', async () => {
    let resolve!: (value: { artifactId: string; versionId: string }) => void;
    commonMocks.createArtifactVersion.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      })
    );
    const i18n = await createTestI18n('en-US');
    render(
      <I18nextProvider i18n={i18n}>
        <PreviewProvider>
          <RemoteEditableBoardHarness />
        </PreviewProvider>
      </I18nextProvider>
    );
    await waitFor(() => expect(screen.getByText('published.md')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Edit file' }));
    const editor = await screen.findByRole('textbox', { name: 'Board file editor' });
    fireEvent.change(editor, { target: { value: '# First draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(commonMocks.createArtifactVersion).toHaveBeenCalledTimes(1));
    fireEvent.change(editor, { target: { value: '# Added later' } });
    resolve({ artifactId: 'artifact-1', versionId: 'version-2' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save changes' })).not.toBeDisabled());
    expect(editor).toHaveValue('# Added later');
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(commonMocks.createArtifactVersion).toHaveBeenCalledTimes(2));
    expect(commonMocks.createArtifactVersion.mock.calls[1]?.[0]).toMatchObject({
      parentVersionId: 'version-2',
      expectedHeadVersionId: 'version-2',
      content: '# Added later',
    });
  });
});
