/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ArtifactPreview from '@/renderer/pages/artifact/ArtifactPreview';
import type { SynonBiomedProjectArtifact } from '@/renderer/services/synonBiomedGateway';
import type { SynonBiomedArtifactCanvasSelection } from '@/renderer/pages/artifact/artifactCanvasSelection';
import { renderWithI18n } from '../i18nTestUtils';

const state = vi.hoisted(() => ({
  artifactId: 'artifact-1' as string | undefined,
  load: vi.fn(),
  versions: vi.fn(),
  lineage: vi.fn(),
  folders: vi.fn(),
  related: vi.fn(),
  suggest: vi.fn(),
  apply: vi.fn(),
  navigate: vi.fn(),
  copy: vi.fn(),
  move: vi.fn(),
  credentials: vi.fn(),
  buckets: vi.fn(),
  export: vi.fn(),
}));
vi.mock('react-router', () => ({
  useParams: () => ({ artifactId: state.artifactId }),
  useLocation: () => ({ search: '' }),
  useNavigate: () => state.navigate,
}));
vi.mock('@/common/config/configService', () => ({
  configService: {
    whenReady: vi.fn().mockResolvedValue(undefined),
    get: vi.fn(),
    set: vi.fn(),
    setLocal: vi.fn(),
    setBatch: vi.fn(),
    remove: vi.fn(),
    subscribe: () => () => {},
  },
}));
vi.mock('@/renderer/services/synonBiomedGateway', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedGateway')>()),
  loadSynonBiomedArtifact: state.load,
  loadSynonBiomedProjectArtifacts: state.related,
  loadSynonBiomedProjectFolders: state.folders,
}));
vi.mock('@/renderer/services/synonBiomedArtifacts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedArtifacts')>()),
  loadSynonBiomedArtifactVersions: state.versions,
  loadSynonBiomedArtifactLineage: state.lineage,
  copySynonBiomedArtifact: state.copy,
  moveSynonBiomedArtifact: state.move,
}));
vi.mock('@/renderer/services/synonBiomedWorkspaceSettings', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedWorkspaceSettings')>()),
  loadSynonBiomedCloudCredentials: state.credentials,
  loadSynonBiomedCloudBuckets: state.buckets,
  exportSynonBiomedArtifactToCloud: state.export,
}));
vi.mock('@/renderer/services/synonBiomedAnnotations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedAnnotations')>()),
  loadSynonBiomedArtifactAnnotations: async () => ({ currentChecksum: null, annotations: [] }),
  suggestSynonBiomedArtifactEdit: state.suggest,
  applySynonBiomedArtifactEdit: state.apply,
}));
vi.mock('@/renderer/pages/artifact/SynonBiomedTextArtifactViewer', () => ({
  default: ({
    onSelectionChange,
    contentUrl,
  }: {
    onSelectionChange: (selection: SynonBiomedArtifactCanvasSelection) => void;
    contentUrl: string;
  }) => (
    <>
      <output data-testid='current-preview-url'>{contentUrl}</output>
      <button
        onClick={() =>
          onSelectionChange({
            type: 'text_selection',
            text: 'Original passage',
            x: 200,
            y: 200,
            startLine: 1,
            startColumn: 1,
            endLine: 1,
            endColumn: 17,
            selectionPrefix: '',
            pageNumber: null,
          })
        }
      >
        Select fixture passage
      </button>
    </>
  ),
}));
vi.mock('@/renderer/pages/artifact/SynonBiomedLatexArtifactViewer', () => ({
  default: ({ resourceUrls }: { resourceUrls: Record<string, string> }) => (
    <output data-testid='latex-resource-keys'>{Object.keys(resourceUrls).join(',')}</output>
  ),
}));

function artifact(id: string, versionNumber = 1): SynonBiomedProjectArtifact {
  return {
    artifactId: id,
    versionId: `${id}-v${versionNumber}`,
    versionNumber,
    projectId: 'project',
    rootFrameId: 'frame',
    frameId: 'frame',
    filename: `${id}.txt`,
    contentType: 'text/plain',
    sizeBytes: 50,
    createdAt: null,
    updatedAt: null,
    isUserUpload: false,
    agentName: 'fixture',
    isIntermediate: false,
    creatingFrameId: 'frame',
    checksum: 'fixture-checksum',
    filePath: '/fixture/example.txt',
    folderId: `folder-${id}`,
    priority: 'normal',
  };
}
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => {
    resolve = complete;
  });
  return { promise, resolve };
};
const renderPage = async () => {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(<ArtifactPreview />, 'en-US');
  });
  await screen.findByRole('heading', { name: 'artifact-1.txt' });
  return view;
};
async function openRefinement() {
  fireEvent.click(screen.getByRole('button', { name: 'Select fixture passage' }));
  const action = within(screen.getByRole('toolbar')).getByRole('button', { name: 'Refine' });
  fireEvent.mouseDown(action);
  fireEvent.click(action);
  await screen.findByRole('dialog', { name: 'Refine selection' });
  fireEvent.change(screen.getByRole('textbox', { name: 'Revision request or question' }), {
    target: { value: 'Clarify passage' },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Generate revision' }));
  await screen.findByText('Revision suggestion');
}

beforeEach(() => {
  state.artifactId = 'artifact-1';
  state.load.mockReset().mockImplementation(async (id: string) => artifact(id));
  state.versions.mockReset().mockImplementation(async (id: string) =>
    [1, 2].map((number) => ({
      artifactId: id,
      versionId: `${id}-v${number}`,
      versionNumber: number,
      contentType: 'text/plain',
      sizeBytes: 50,
      createdAt: null,
      filePath: '/fixture/example.txt',
      frameId: 'frame',
      agentName: 'fixture',
      parentVersionId: number === 2 ? `${id}-v1` : null,
      language: null,
    }))
  );
  state.lineage.mockReset().mockResolvedValue(null);
  state.related.mockReset().mockResolvedValue([]);
  state.folders.mockReset().mockResolvedValue(
    ['artifact-1', 'artifact-2'].map((id) => ({
      folderId: `folder-${id}`,
      projectId: 'project',
      parentId: null,
      rootFrameId: 'frame',
      name: `Folder ${id}`,
      sortOrder: 0,
      artifactCount: 1,
      isConversationFolder: true,
      isUserUploadsFolder: false,
    }))
  );
  state.suggest.mockReset().mockResolvedValue('Revised passage');
  state.navigate.mockReset();
  state.copy.mockReset().mockResolvedValue({ artifactId: 'artifact-copy' });
  state.move.mockReset().mockResolvedValue({ artifactId: 'artifact-1' });
  state.credentials.mockReset().mockResolvedValue([
    {
      id: 'credential-1',
      provider: 's3',
      name: 'Primary storage',
      credentialType: 'access_key',
      connected: true,
      defaultBucket: 'bucket-1',
      region: null,
    },
  ]);
  state.buckets.mockReset().mockResolvedValue(['bucket-1']);
  state.export.mockReset().mockResolvedValue({ exported: true });
  state.apply.mockReset().mockResolvedValue({
    artifactId: 'artifact-1',
    versionId: 'artifact-1-v2',
    versionNumber: 2,
    parentVersionId: 'artifact-1-v1',
    carriedAnnotations: [],
  });
});

describe('Artifact preview interaction ownership', () => {
  it('shows the file before slow auxiliary metadata without hiding healthy peers', async () => {
    state.versions.mockReturnValueOnce(new Promise(() => {}));
    await renderPage();
    expect(screen.getByTestId('current-preview-url')).toHaveTextContent('/api/artifacts/artifact-1');
    expect(screen.getByRole('link', { name: 'Download', exact: true })).toBeInTheDocument();
    expect(await screen.findByText('Folder artifact-1')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Versions' }));
    expect(within(screen.getByRole('group', { name: 'Versions', exact: true })).getByRole('status')).toHaveTextContent(
      'Loading Versions'
    );
    expect(screen.queryByText('No version history')).not.toBeInTheDocument();
  });

  it('keeps failed version metadata distinct from empty and retries only that read', async () => {
    state.versions.mockRejectedValueOnce(new Error('fixture versions failure'));
    await renderPage();
    fireEvent.click(screen.getByRole('tab', { name: 'Versions' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load Versions');
    expect(screen.queryByText('No version history')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry Versions' }));
    expect(await screen.findByRole('button', { name: /Version 2/ })).toBeInTheDocument();
    expect(state.versions).toHaveBeenCalledTimes(2);
    expect(state.load).toHaveBeenCalledTimes(1);
    expect(state.lineage).toHaveBeenCalledTimes(1);
    expect(state.folders).toHaveBeenCalledTimes(1);
    expect(state.related).toHaveBeenCalledTimes(1);
  });

  it('does not claim the project root or enable folder mutations after a failed folder read', async () => {
    state.folders.mockRejectedValueOnce(new Error('fixture folders failure'));
    await renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load Folder');
    expect(screen.queryByText('Project root')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy file' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Move to folder' })).toBeDisabled();
    expect(screen.getByRole('link', { name: 'Download', exact: true })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry Folder' }));
    expect(await screen.findByText('Folder artifact-1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy file' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Move to folder' })).toBeEnabled();
    expect(state.copy).not.toHaveBeenCalled();
    expect(state.move).not.toHaveBeenCalled();
  });

  it('keeps failed source metadata distinct from an absent lineage', async () => {
    state.lineage.mockRejectedValueOnce(new Error('fixture lineage failure'));
    await renderPage();
    fireEvent.click(screen.getByRole('tab', { name: 'Lineage' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load Lineage');
    expect(screen.queryByText('No lineage record')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry Lineage' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(state.lineage).toHaveBeenCalledTimes(2);
    expect(state.load).toHaveBeenCalledTimes(1);
  });

  it('binds lineage to a selected historical version without rereading healthy peers', async () => {
    state.lineage.mockImplementation(async (id: string, options: { versionId?: string }) => ({
      artifactId: id,
      versionId: options.versionId,
      versionNumber: 1,
      codeDescription: `Source ${options.versionId}`,
      code: null,
      pending: false,
      hasMessages: false,
      hasEnvironment: false,
      hasCellSources: false,
    }));
    await renderPage();
    expect(state.lineage).toHaveBeenCalledWith('artifact-1', { slim: true, versionId: 'artifact-1-v1' });
    fireEvent.click(screen.getByRole('tab', { name: 'Versions' }));
    fireEvent.click(await screen.findByRole('button', { name: /Version 2/ }));
    await waitFor(() =>
      expect(state.lineage).toHaveBeenCalledWith('artifact-1', { slim: true, versionId: 'artifact-1-v2' })
    );
    fireEvent.click(screen.getByRole('tab', { name: 'Lineage' }));
    expect(await screen.findByText('Source artifact-1-v2')).toBeInTheDocument();
    for (const read of [state.versions, state.folders, state.related, state.load])
      expect(read).toHaveBeenCalledTimes(1);
  });

  it('describes unready lineage without claiming a historical generation process is running', async () => {
    state.lineage.mockResolvedValueOnce({ artifactId: 'artifact-1', versionId: 'artifact-1-v1', pending: true });
    await renderPage();
    fireEvent.click(screen.getByRole('tab', { name: 'Lineage' }));
    expect(await screen.findByText('Lineage information is not ready yet')).toBeInTheDocument();
    expect(screen.queryByText('Lineage information is being generated')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the saved immutable version even when its metadata refresh is still pending', async () => {
    await renderPage();
    await openRefinement();
    state.versions.mockReturnValueOnce(new Promise(() => {}));
    state.load.mockResolvedValueOnce(artifact('artifact-1', 2));
    fireEvent.click(screen.getByRole('button', { name: 'Apply as new version' }));
    await waitFor(() =>
      expect(screen.getByTestId('current-preview-url')).toHaveTextContent('/api/artifacts/versions/artifact-1-v2')
    );
    expect(state.apply).toHaveBeenCalledTimes(1);
  });

  it('ignores a previous file folder read finishing after the route changed', async () => {
    const pending = deferred<Array<{ folderId: string; name: string }>>();
    state.folders.mockReturnValueOnce(pending.promise);
    const view = await renderPage();
    expect(screen.getByRole('button', { name: 'Move to folder' })).toBeDisabled();
    state.artifactId = 'artifact-2';
    view.rerender(<ArtifactPreview />);
    expect(await screen.findByText('Folder artifact-2')).toBeInTheDocument();
    await act(async () => {
      pending.resolve([{ folderId: 'folder-artifact-2', name: 'Obsolete folder' }]);
      await pending.promise;
    });
    expect(screen.getByText('Folder artifact-2')).toBeInTheDocument();
    expect(screen.queryByText('Obsolete folder')).not.toBeInTheDocument();
  });

  it('keeps an unknown non-root folder distinct from the real project root', async () => {
    state.folders.mockResolvedValueOnce([]);
    await renderPage();
    expect(await screen.findByText('Folder not found in the current list')).toBeInTheDocument();
    expect(screen.queryByText('Project root')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('reports related-file failure without hiding a standalone preview or rerunning peer reads', async () => {
    state.related.mockRejectedValueOnce(new Error('fixture related-file failure'));
    await renderPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load Related files');
    expect(screen.getByTestId('current-preview-url')).toHaveTextContent('/api/artifacts/artifact-1');
    fireEvent.click(screen.getByRole('button', { name: 'Retry Related files' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(state.related).toHaveBeenCalledTimes(2);
    expect(state.versions).toHaveBeenCalledTimes(1);
    expect(state.folders).toHaveBeenCalledTimes(1);
    expect(state.load).toHaveBeenCalledTimes(1);
  });

  it('does not start a resource-dependent preview using a failed empty resource fallback', async () => {
    state.load.mockResolvedValue({ ...artifact('artifact-1'), filename: 'report.tex', contentType: 'text/x-tex' });
    state.related.mockRejectedValueOnce(new Error('fixture resources failure'));
    await renderWithI18n(<ArtifactPreview />, 'en-US');
    expect(await screen.findByRole('heading', { name: 'report.tex' })).toBeInTheDocument();
    const preview = screen.getByRole('region', { name: 'File preview' });
    expect(await within(preview).findByRole('alert')).toHaveTextContent('Unable to load Related files');
    expect(screen.queryByTestId('latex-resource-keys')).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Download', exact: true })).toBeInTheDocument();
    state.related.mockResolvedValueOnce([{ ...artifact('figure'), filename: 'figure.png', contentType: 'image/png' }]);
    fireEvent.click(within(preview).getByRole('button', { name: 'Retry Related files' }));
    expect(await screen.findByTestId('latex-resource-keys')).toHaveTextContent('figure.png');
    expect(state.related).toHaveBeenCalledTimes(2);
    expect(state.load).toHaveBeenCalledTimes(1);
  });

  it('returns focus to the recovered folder information without an extra Tab stop', async () => {
    state.folders.mockRejectedValueOnce(new Error('fixture folders failure'));
    await renderPage();
    const retry = await screen.findByRole('button', { name: 'Retry Folder' });
    act(() => retry.focus());
    fireEvent.click(retry);
    const region = screen.getByRole('group', { name: 'Folder', exact: true });
    await waitFor(() => expect(region).toHaveFocus());
    expect(region).toHaveAttribute('tabindex', '-1');
    expect(region).toHaveTextContent('Folder artifact-1');
  });

  it('does not steal an external focus destination when folder recovery finishes', async () => {
    state.folders.mockRejectedValueOnce(new Error('fixture folders failure'));
    await renderPage();
    const pending = deferred<Array<{ folderId: string; name: string }>>();
    state.folders.mockReturnValueOnce(pending.promise);
    fireEvent.click(await screen.findByRole('button', { name: 'Retry Folder' }));
    const destination = screen.getByRole('button', { name: 'Export to cloud storage' });
    act(() => destination.focus());
    await act(async () => {
      pending.resolve([{ folderId: 'folder-artifact-1', name: 'Recovered folder' }]);
      await pending.promise;
    });
    expect(destination).toHaveFocus();
    expect(screen.getByText('Recovered folder')).toBeInTheDocument();
  });

  it('provides a same-page retry and restores reading focus after a failed file read', async () => {
    state.load.mockRejectedValueOnce(new Error('fixture read failure'));
    await renderWithI18n(<ArtifactPreview />, 'en-US');
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load file');
    expect(screen.getByRole('heading', { name: 'File preview' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to workspace' })).toHaveAttribute('href', '#/guid');
    state.load.mockRejectedValueOnce(new Error('fixture repeated read failure'));
    fireEvent.click(screen.getByRole('button', { name: 'Retry', exact: true }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load file');
    expect(state.load).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole('button', { name: 'Retry', exact: true }));
    const heading = await screen.findByRole('heading', { name: 'artifact-1.txt' });
    expect(heading).toHaveFocus();
    expect(state.load).toHaveBeenCalledTimes(3);
  });

  it.each(['', undefined])('keeps a missing file distinct from a failed read: %s', async (missingId) => {
    state.artifactId = missingId;
    await renderWithI18n(<ArtifactPreview />, 'zh-CN');
    expect(screen.getByRole('heading', { name: '文件预览' })).toBeInTheDocument();
    expect(screen.getByText('未找到文件')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '返回工作区' })).toHaveAttribute('href', '#/guid');
    expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(state.load).not.toHaveBeenCalled();
  });

  it('announces the loading page without presenting a false empty result', async () => {
    const pending = deferred<SynonBiomedProjectArtifact>();
    state.load.mockReturnValueOnce(pending.promise);
    const view = await renderWithI18n(<ArtifactPreview />, 'en-US');
    expect(await screen.findByRole('status')).toHaveTextContent('Please wait...');
    expect(screen.getByRole('heading', { name: 'File preview' })).toBeInTheDocument();
    expect(screen.queryByText('File not found')).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to workspace' })).toBeInTheDocument();
    view.unmount();
    await act(async () => {
      pending.resolve(artifact('artifact-1'));
      await pending.promise;
    });
  });

  it('does not replace a newer file when a previous retry finishes late', async () => {
    state.load.mockRejectedValueOnce(new Error('fixture first failure'));
    const view = await renderWithI18n(<ArtifactPreview />, 'en-US');
    const retry = await screen.findByRole('button', { name: 'Retry', exact: true });
    const pending = deferred<SynonBiomedProjectArtifact>();
    state.load.mockReturnValueOnce(pending.promise);
    fireEvent.click(retry);
    state.artifactId = 'artifact-2';
    view.rerender(<ArtifactPreview />);
    await screen.findByRole('heading', { name: 'artifact-2.txt' });
    await act(async () => {
      pending.resolve(artifact('artifact-1'));
      await pending.promise;
    });
    expect(screen.getByRole('heading', { name: 'artifact-2.txt' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'artifact-1.txt' })).not.toBeInTheDocument();
    expect(state.load).toHaveBeenCalledTimes(3);
  });

  it('keeps download as one native link rather than two nested interactive controls', async () => {
    await renderPage();
    const download = screen.getByRole('link', { name: 'Download', exact: true });
    expect(download).toHaveAttribute('href', '/api/artifacts/artifact-1');
    expect(download).toHaveAttribute('download', 'artifact-1.txt');
    expect(within(download).queryByRole('button')).not.toBeInTheDocument();
    expect(download.querySelector('button,[tabindex="0"]')).toBeNull();
  });

  it.each(['close', 'route'] as const)('does not navigate after an obsolete copy finishes: %s', async (dismiss) => {
    const pending = deferred<{ artifactId: string }>();
    state.copy.mockReturnValueOnce(pending.promise);
    const view = await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Copy file' }));
    const dialog = screen.getByRole('dialog', { name: 'Copy file' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy', exact: true }));
    expect(state.copy).toHaveBeenCalledWith({
      artifactId: 'artifact-1',
      newFilename: 'artifact-1-copy.txt',
      targetFolderId: null,
    });
    expect(within(dialog).getByRole('textbox', { name: 'Copied filename' })).toBeDisabled();
    expect(within(dialog).getByRole('combobox', { name: 'Target folder' })).toHaveAttribute('aria-disabled', 'true');
    expect(within(dialog).getByRole('button', { name: 'Copy', exact: true })).toBeDisabled();
    if (dismiss === 'close')
      fireEvent.click(within(dialog).getByText('Close', { selector: 'span' }).closest('button')!);
    else {
      state.artifactId = 'artifact-2';
      view.rerender(<ArtifactPreview />);
      await screen.findByRole('heading', { name: 'artifact-2.txt' });
    }
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Copy file' })).not.toBeInTheDocument());
    await act(async () => {
      pending.resolve({ artifactId: 'artifact-copy' });
      await pending.promise;
    });
    expect(state.navigate).not.toHaveBeenCalled();
    expect(state.copy).toHaveBeenCalledTimes(1);
  });

  it('does not apply a previous file move result to the current file folder', async () => {
    const pending = deferred<{ artifactId: string }>();
    state.move.mockReturnValueOnce(pending.promise);
    const view = await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Move to folder' }));
    fireEvent.click(
      within(screen.getByRole('dialog', { name: 'Move to folder' })).getByRole('button', { name: 'Move', exact: true })
    );
    expect(state.move).toHaveBeenCalledWith({ artifactId: 'artifact-1', folderId: 'folder-artifact-1' });
    state.artifactId = 'artifact-2';
    view.rerender(<ArtifactPreview />);
    await screen.findByRole('heading', { name: 'artifact-2.txt' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Move to folder' })).not.toBeInTheDocument());
    await act(async () => {
      pending.resolve({ artifactId: 'artifact-1' });
      await pending.promise;
    });
    expect(screen.getByText('Folder artifact-2')).toBeInTheDocument();
    expect(screen.queryByText('Folder artifact-1')).not.toBeInTheDocument();
  });

  it('does not let a cancelled export catalogue replace a newly opened catalogue', async () => {
    const pending = deferred<Array<{ id: string; name: string; connected: boolean; defaultBucket: string }>>();
    state.credentials
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce([
        { id: 'credential-new', name: 'New storage', connected: true, defaultBucket: 'bucket-new' },
      ]);
    state.buckets.mockResolvedValue(['bucket-new']);
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Export to cloud storage' }));
    fireEvent.click(
      within(screen.getByRole('dialog', { name: 'Export to cloud storage' })).getByRole('button', { name: 'Cancel' })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Export to cloud storage' }));
    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Cloud storage credential' })).toHaveTextContent('New storage')
    );
    await act(async () => {
      pending.resolve([{ id: 'credential-old', name: 'Old storage', connected: true, defaultBucket: 'bucket-old' }]);
      await pending.promise;
    });
    expect(screen.getByRole('combobox', { name: 'Cloud storage credential' })).toHaveTextContent('New storage');
    expect(state.buckets).not.toHaveBeenCalledWith('credential-old');
  });

  it('keeps a failed catalogue visibly failed rather than claiming no connected credentials', async () => {
    state.credentials.mockRejectedValueOnce(new Error('fixture catalogue failure'));
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Export to cloud storage' }));
    const dialog = screen.getByRole('dialog', { name: 'Export to cloud storage' });
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Failed to load cloud storage credentials');
    expect(
      within(dialog).queryByText('No connected cloud storage credentials. Add and connect one in Settings first.')
    ).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));
    await waitFor(() =>
      expect(within(dialog).getByRole('combobox', { name: 'Cloud storage credential' })).toHaveTextContent(
        'Primary storage'
      )
    );
    expect(state.credentials).toHaveBeenCalledTimes(2);
  });

  it.each([
    ['Annotate', 'Add selection annotation'],
    ['Refine', 'Refine selection'],
  ])('returns to the source preview after closing the transient %s toolbar dialog', async (actionName, dialogName) => {
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Select fixture passage' }));
    const opener = within(screen.getByRole('toolbar')).getByRole('button', { name: actionName });
    act(() => opener.focus());
    fireEvent.click(opener);
    const dialog = await screen.findByRole('dialog', { name: dialogName });
    fireEvent.click(within(dialog).getByLabelText('Close'));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole('region', { name: 'File preview' })).toHaveFocus());
    expect(state.apply).not.toHaveBeenCalled();
  });

  it.each([
    ['Annotate', 'Add selection annotation'],
    ['Refine', 'Refine selection'],
  ])('supports the keyboard-generated click for %s', async (actionName, dialogName) => {
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Select fixture passage' }));
    fireEvent.click(within(screen.getByRole('toolbar')).getByRole('button', { name: actionName }));
    expect(screen.getByRole('dialog', { name: dialogName })).toBeInTheDocument();
    expect(state.suggest).not.toHaveBeenCalled();
    expect(state.apply).not.toHaveBeenCalled();
  });

  it('does not overwrite another artifact when a saved-version display read finishes late', async () => {
    const pending = deferred<SynonBiomedProjectArtifact>();
    const view = await renderPage();
    await openRefinement();
    state.load.mockImplementationOnce(() => pending.promise);
    fireEvent.click(screen.getByRole('button', { name: 'Apply as new version' }));
    await waitFor(() => expect(state.load).toHaveBeenCalledTimes(2));
    state.artifactId = 'artifact-2';
    view.rerender(<ArtifactPreview />);
    await screen.findByRole('heading', { name: 'artifact-2.txt' });
    await act(async () => {
      pending.resolve(artifact('artifact-1', 2));
      await pending.promise;
    });
    expect(screen.getByRole('heading', { name: 'artifact-2.txt' })).toBeInTheDocument();
    expect(screen.getByTestId('current-preview-url')).toHaveTextContent('/api/artifacts/artifact-2');
    expect(state.apply).toHaveBeenCalledTimes(1);
  });

  it('keeps a user-selected historical version when a prior saved-version read finishes', async () => {
    const pending = deferred<SynonBiomedProjectArtifact>();
    await renderPage();
    await openRefinement();
    state.load.mockImplementationOnce(() => pending.promise);
    fireEvent.click(screen.getByRole('button', { name: 'Apply as new version' }));
    await waitFor(() => expect(state.load).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole('dialog').querySelector<HTMLButtonElement>('button[aria-label="Close"]')!);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('tab', { name: 'Versions' }));
    fireEvent.click(await screen.findByRole('button', { name: /Version 1/ }));
    await act(async () => {
      pending.resolve(artifact('artifact-1', 2));
      await pending.promise;
    });
    expect(screen.getByTestId('current-preview-url')).toHaveTextContent('/api/artifacts/versions/artifact-1-v1');
    expect(state.apply).toHaveBeenCalledTimes(1);
  });
});
