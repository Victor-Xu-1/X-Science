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

const state = vi.hoisted(() => ({ artifactId: 'artifact-1', load: vi.fn(), suggest: vi.fn(), apply: vi.fn() }));
vi.mock('react-router', () => ({
  useParams: () => ({ artifactId: state.artifactId }),
  useLocation: () => ({ search: '' }),
  useNavigate: () => vi.fn(),
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
  loadSynonBiomedProjectArtifacts: async () => [],
  loadSynonBiomedProjectFolders: async () => [],
}));
vi.mock('@/renderer/services/synonBiomedArtifacts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/synonBiomedArtifacts')>()),
  loadSynonBiomedArtifactVersions: async (id: string) =>
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
    })),
  loadSynonBiomedArtifactLineage: async () => null,
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
    folderId: null,
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
  state.suggest.mockReset().mockResolvedValue('Revised passage');
  state.apply.mockReset().mockResolvedValue({
    artifactId: 'artifact-1',
    versionId: 'artifact-1-v2',
    versionNumber: 2,
    parentVersionId: 'artifact-1-v1',
    carriedAnnotations: [],
  });
});

describe('Artifact preview interaction ownership', () => {
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
