import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Message } from '@arco-design/web-react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import type { XScienceArtifactAnnotation } from '@/renderer/services/xScienceAnnotations';

const render = (ui: React.ReactElement) => renderWithI18n(ui);

const artifactRoute = vi.hoisted(() => ({ artifactId: 'artifact-1', search: '' }));

const deferredFixture = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((accept) => {
    resolve = accept;
  });
  return { promise, resolve };
};

beforeEach(() => {
  artifactRoute.artifactId = 'artifact-1';
  artifactRoute.search = '';
  loadArtifactMock.mockReset().mockImplementation(defaultArtifactLoader);
  loadVersionsMock.mockReset().mockImplementation(defaultVersionLoader);
  loadAnnotationsMock.mockReset().mockImplementation(defaultAnnotationLoader);
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {});
});
afterEach(async () => {
  cleanup();
  await Promise.resolve();
  vi.restoreAllMocks();
});

const loadArtifactMock = vi.fn(async () => ({
  artifactId: 'artifact-1',
  versionId: 'version-1',
  versionNumber: 1,
  projectId: 'proj_example',
  rootFrameId: 'frame-1',
  frameId: 'frame-1',
  filename: 'qc_metrics.png',
  contentType: 'image/png',
  sizeBytes: 2048,
  createdAt: null,
  updatedAt: null,
  isUserUpload: false,
  agentName: 'OPERON',
  isIntermediate: false,
  creatingFrameId: 'frame-1',
  checksum: 'sha256-example',
  filePath: '/state/artifacts/qc_metrics.png',
  folderId: 'folder-1',
  priority: 'normal',
}));

const loadVersionsMock = vi.fn(async () => [
  {
    versionId: 'version-1',
    versionNumber: 1,
    artifactId: 'artifact-1',
    frameId: 'frame-1',
    agentName: 'OPERON',
    language: null,
    contentType: 'image/png',
    sizeBytes: 2048,
    createdAt: '2026-07-11T01:00:00.000Z',
    filePath: '/state/artifacts/qc_metrics.png',
    parentVersionId: null,
  },
]);

const defaultArtifactLoader = loadArtifactMock.getMockImplementation()!;
const defaultVersionLoader = loadVersionsMock.getMockImplementation()!;

const loadAnnotationsMock = vi.fn(async (_artifactId: string, _versionId: string) => ({
  targetKey: 'fixture-version-target',
  currentChecksum: null,
  annotations: [] as XScienceArtifactAnnotation[],
}));
const defaultAnnotationLoader = loadAnnotationsMock.getMockImplementation()!;

const loadLineageMock = vi.fn(async () => ({
  artifactId: 'artifact-1',
  versionId: 'version-1',
  versionNumber: 1,
  filename: 'qc_metrics.png',
  code: null,
  codeDescription: '由质量控制流程生成',
  messages: null,
  environmentSnapshot: { python: '3.12' },
  language: null,
  interactions: null,
  hasCellSources: false,
  hasMessages: false,
  hasEnvironment: true,
  pending: false,
  dependencyMappings: null,
}));

const copyArtifactMock = vi.fn(async () => ({
  artifactId: 'artifact-copy',
  versionId: 'version-copy',
  filename: 'qc_metrics-copy.png',
}));

const moveArtifactMock = vi.fn(async () => ({ artifactId: 'artifact-1', folderId: 'folder-2' }));
const exportArtifactMock = vi.fn(async () => ({ exported: true }));
const loadCloudBucketsMock = vi.fn(async () => ['bucket-a']);
const loadCloudCredentialsMock = vi.fn(async () => [
  {
    id: 'cloud-1',
    provider: 's3',
    name: 'Research',
    credentialType: 'access_key',
    connected: true,
    defaultBucket: 'bucket-a',
    region: 'us-east-1',
  },
]);

const navigateMock = vi.fn();

vi.mock('@/common/config/configService', () => ({
  configService: {
    whenReady: vi.fn().mockResolvedValue(undefined),
    get: vi.fn(() => undefined),
    set: vi.fn().mockResolvedValue(undefined),
    setLocal: vi.fn(),
    setBatch: vi.fn().mockResolvedValue(undefined),
    remove: vi.fn().mockResolvedValue(undefined),
    subscribe: vi.fn(() => () => {}),
  },
}));

vi.mock('@/renderer/pages/conversation/Preview/components/editors/CodeEditor', () => ({
  default: ({ value }: { value: string }) => <pre data-testid='artifact-source-editor'>{value}</pre>,
}));

vi.mock('@/renderer/pages/artifact/XSciencePdfArtifactViewer', () => ({
  default: () => null,
}));

vi.mock('react-router', () => ({
  useNavigate: () => navigateMock,
  useParams: () => ({ artifactId: artifactRoute.artifactId }),
  useLocation: () => ({ search: artifactRoute.search }),
}));

vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceProject: async (projectId: string) => ({ projectId, name: 'Fixture project' }),
  loadXScienceLinkedTask: async (frameId: string) => ({ frameId, name: 'Fixture task', projectId: null }),
  loadXScienceArtifact: (...args: unknown[]) => loadArtifactMock(...args),
  loadXScienceProjectArtifacts: async () => [await loadArtifactMock()],
  getXScienceArtifactContentUrl: (artifactId: string) => `/api/artifacts/${artifactId}`,
  loadXScienceProjectFolders: async () => [
    {
      folderId: 'folder-1',
      projectId: 'proj_example',
      parentId: null,
      rootFrameId: null,
      name: '用户上传',
      sortOrder: -1000,
      artifactCount: 1,
      isConversationFolder: false,
      isUserUploadsFolder: true,
    },
    {
      folderId: 'folder-2',
      projectId: 'proj_example',
      parentId: null,
      rootFrameId: 'frame-1',
      name: '质量控制',
      sortOrder: 0,
      artifactCount: 3,
      isConversationFolder: true,
      isUserUploadsFolder: false,
    },
  ],
}));

vi.mock('@/renderer/services/xScienceArtifacts', () => ({
  loadXScienceArtifactVersions: (...args: unknown[]) => loadVersionsMock(...args),
  loadXScienceArtifactLineage: (...args: unknown[]) => loadLineageMock(...args),
  copyXScienceArtifact: (...args: unknown[]) => copyArtifactMock(...args),
  moveXScienceArtifact: (...args: unknown[]) => moveArtifactMock(...args),
  getXScienceArtifactVersionContentUrl: (versionId: string) => `/api/artifacts/versions/${versionId}`,
}));

vi.mock('@/renderer/services/xScienceWorkspaceSettings', () => ({
  loadXScienceCloudCredentials: (...args: unknown[]) => loadCloudCredentialsMock(...args),
  loadXScienceCloudBuckets: (...args: unknown[]) => loadCloudBucketsMock(...args),
  exportXScienceArtifactToCloud: (...args: unknown[]) => exportArtifactMock(...args),
}));

vi.mock('@/renderer/services/xScienceAnnotations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceAnnotations')>()),
  loadXScienceArtifactAnnotations: (artifactId: string, versionId: string) =>
    loadAnnotationsMock(artifactId, versionId),
}));

import ArtifactPreview from '@/renderer/pages/artifact/ArtifactPreview';
import AudioPreview from '@/renderer/pages/artifact/AudioPreview';
import VideoPreview from '@/renderer/pages/artifact/VideoPreview';

vi.mock('@/renderer/pages/conversation/Preview/components/viewers/XScienceStructureViewer', () => ({
  default: ({ contentUrl }: { contentUrl?: string }) => (
    <output data-testid='structure-version-source'>{contentUrl}</output>
  ),
}));

describe('ArtifactPreview', () => {
  it('restores a historical URL version without showing latest while its membership is loading', async () => {
    const original = await defaultArtifactLoader();
    const [historical] = await defaultVersionLoader();
    const versions = deferredFixture<Awaited<ReturnType<typeof defaultVersionLoader>>>();
    artifactRoute.search = '?version=version-1';
    loadArtifactMock.mockResolvedValue({ ...original, versionId: 'version-2', versionNumber: 2 });
    loadVersionsMock.mockImplementation(() => versions.promise);
    await render(<ArtifactPreview />);
    await waitFor(() => expect(loadVersionsMock).toHaveBeenCalledWith('artifact-1'));
    expect(screen.queryByRole('img', { name: original.filename })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '下载' })).not.toBeInTheDocument();
    await act(async () => versions.resolve([{ ...historical, versionId: 'version-2', versionNumber: 2 }, historical]));
    expect(await screen.findByRole('img', { name: original.filename })).toHaveAttribute(
      'src',
      '/api/artifacts/versions/version-1'
    );
  });

  it.each([
    { kind: 'absent', language: 'en-US', message: 'Version details unavailable' },
    { kind: 'foreign', language: 'zh-CN', message: '版本详情暂不可用' },
  ])('does not replace a $kind URL version with latest in $language', async ({ kind, language, message }) => {
    const [version] = await defaultVersionLoader();
    artifactRoute.search = '?version=unavailable-version';
    loadVersionsMock.mockResolvedValue(
      kind === 'foreign' ? [{ ...version, versionId: 'unavailable-version', artifactId: 'other-artifact' }] : []
    );
    await renderWithI18n(<ArtifactPreview />, language);
    expect(await screen.findByRole('alert')).toHaveTextContent(message);
    expect(screen.getByRole('alert')).toHaveTextContent('unavailable-version');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^(复制文件|Copy file)$/ })).not.toBeInTheDocument();
  });

  it('retries a failed version read without substituting latest or refetching a healthy version list', async () => {
    const original = await defaultArtifactLoader();
    artifactRoute.search = '?version=version-1';
    loadArtifactMock.mockResolvedValue({ ...original, versionId: 'version-2', versionNumber: 2 });
    loadVersionsMock.mockRejectedValueOnce(new Error('version list temporarily unavailable'));
    const view = await render(<ArtifactPreview />);
    expect(await screen.findByRole('alert')).toHaveTextContent('无法读取版本');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试读取版本' }));
    expect(await screen.findByRole('img', { name: original.filename })).toHaveAttribute(
      'src',
      '/api/artifacts/versions/version-1'
    );
    expect(loadVersionsMock).toHaveBeenCalledTimes(2);

    artifactRoute.search = '?version=version-2';
    // The current immutable version is already proven by the artifact metadata.
    // A route-only selection must reuse this owner's completed metadata reads.
    view.rerender(<ArtifactPreview />);
    await waitFor(() =>
      expect(screen.getByRole('img', { name: original.filename })).toHaveAttribute(
        'src',
        '/api/artifacts/versions/version-2'
      )
    );
    expect(loadVersionsMock).toHaveBeenCalledTimes(2);
  });

  it('keeps an explicit empty URL version unavailable rather than treating it as latest', async () => {
    artifactRoute.search = '?version=';
    await render(<ArtifactPreview />);
    expect(await screen.findByRole('alert')).toHaveTextContent('版本详情暂不可用');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '下载' })).not.toBeInTheDocument();
  });

  it('does not let a previous artifact membership read overwrite a new URL selection', async () => {
    const original = await defaultArtifactLoader();
    const [version] = await defaultVersionLoader();
    const oldVersions = deferredFixture<Awaited<ReturnType<typeof defaultVersionLoader>>>();
    artifactRoute.search = '?version=version-old';
    loadArtifactMock.mockImplementation(async (...args: unknown[]) =>
      args[0] === 'artifact-2'
        ? {
            ...original,
            artifactId: 'artifact-2',
            versionId: 'version-current-2',
            filename: 'second.png',
          }
        : { ...original, versionId: 'version-current-1' }
    );
    loadVersionsMock.mockImplementation((...args: unknown[]) =>
      args[0] === 'artifact-1'
        ? oldVersions.promise
        : Promise.resolve([{ ...version, artifactId: 'artifact-2', versionId: 'selected-2' }])
    );
    const view = await render(<ArtifactPreview />);
    await waitFor(() => expect(loadVersionsMock).toHaveBeenCalledWith('artifact-1'));
    artifactRoute.artifactId = 'artifact-2';
    artifactRoute.search = '?version=selected-2';
    view.rerender(<ArtifactPreview />);
    expect(await screen.findByRole('img', { name: 'second.png' })).toHaveAttribute(
      'src',
      '/api/artifacts/versions/selected-2'
    );
    await act(async () => oldVersions.resolve([{ ...version, versionId: 'version-old' }]));
    expect(screen.getByRole('img', { name: 'second.png' })).toHaveAttribute(
      'src',
      '/api/artifacts/versions/selected-2'
    );
    expect(screen.queryByRole('heading', { name: original.filename })).not.toBeInTheDocument();
  });

  it('isolates annotations and open actions when a URL selects another version of the same artifact', async () => {
    const original = await defaultArtifactLoader();
    const [version] = await defaultVersionLoader();
    const laterAnnotations = deferredFixture<Awaited<ReturnType<typeof defaultAnnotationLoader>>>();
    const annotation: XScienceArtifactAnnotation = {
      id: 'point-old',
      artifactId: original.artifactId,
      targetKey: 'fixture-version-1',
      label: 'A',
      contentChecksum: null,
      type: 'point',
      text: 'Old version note',
      xPercent: 50,
      yPercent: 50,
      startLine: null,
      startColumn: null,
      endLine: null,
      endColumn: null,
      selectionText: null,
      pageNumber: null,
      selectionPrefix: null,
      screenshotArtifactId: null,
      elementSelector: null,
      elementDescriptor: null,
      addressedAt: null,
      addressedInFrameId: null,
      createdAt: '2026-10-18T01:00:00.000Z',
    };
    artifactRoute.search = '?version=version-1';
    loadVersionsMock.mockResolvedValue([version, { ...version, versionId: 'version-2', versionNumber: 2 }]);
    loadAnnotationsMock.mockImplementation((_artifactId, versionId) =>
      versionId === 'version-2'
        ? laterAnnotations.promise
        : Promise.resolve({ targetKey: annotation.targetKey!, currentChecksum: null, annotations: [annotation] })
    );
    const loadImage = (image: HTMLElement) => {
      Object.defineProperties(image, {
        naturalWidth: { configurable: true, value: 200 },
        naturalHeight: { configurable: true, value: 100 },
      });
      fireEvent.load(image);
    };
    const view = await render(<ArtifactPreview />);
    loadImage(await screen.findByRole('img', { name: original.filename }));
    expect(await screen.findByTitle('Old version note')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '复制文件' }));
    expect(await screen.findByRole('dialog')).toBeInTheDocument();

    artifactRoute.search = '?version=version-2';
    view.rerender(<ArtifactPreview />);
    const currentImage = await screen.findByRole('img', { name: original.filename });
    expect(currentImage).toHaveAttribute('src', '/api/artifacts/versions/version-2');
    loadImage(currentImage);
    expect(screen.queryByTitle('Old version note')).not.toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await act(async () =>
      laterAnnotations.resolve({
        targetKey: 'fixture-version-2',
        currentChecksum: null,
        annotations: [{ ...annotation, id: 'point-new', label: 'B', text: 'New version note' }],
      })
    );
    expect(await screen.findByTitle('New version note')).toBeInTheDocument();
    expect(screen.queryByTitle('Old version note')).not.toBeInTheDocument();
  });

  it('binds structure rendering and derived snapshots to the displayed immutable version', async () => {
    const original = await loadArtifactMock();
    const [olderVersion] = await loadVersionsMock();
    loadArtifactMock.mockResolvedValueOnce({
      ...original,
      filename: 'complex.pdb',
      contentType: 'chemical/x-pdb',
      versionId: 'version-2',
      versionNumber: 2,
    });
    loadVersionsMock.mockResolvedValueOnce([
      { ...olderVersion, versionId: 'version-2', versionNumber: 2, contentType: 'chemical/x-pdb' },
      { ...olderVersion, contentType: 'chemical/x-pdb' },
    ]);
    await render(<ArtifactPreview />);
    expect(await screen.findByTestId('structure-version-source')).toHaveTextContent(
      '/api/artifacts/artifact-1/versions/version-2'
    );
    fireEvent.click(screen.getByRole('tab', { name: '版本' }));
    const olderVersionButton = await screen.findByRole('button', { name: /版本 1/ });
    await act(async () => fireEvent.click(olderVersionButton));
    expect(screen.getByTestId('structure-version-source')).toHaveTextContent(
      '/api/artifacts/artifact-1/versions/version-1'
    );
  });

  it('renders a X-Science image artifact using the backend content endpoint', async () => {
    await render(<ArtifactPreview />);

    expect(await screen.findByRole('heading', { name: 'qc_metrics.png' })).toBeInTheDocument();
    expect(await screen.findByRole('img', { name: 'qc_metrics.png' })).toHaveAttribute(
      'src',
      '/api/artifacts/artifact-1'
    );
    expect(loadArtifactMock).toHaveBeenCalledWith('artifact-1');
    expect(screen.getByRole('button', { name: '笔记' })).toBeInTheDocument();
    expect(await screen.findByRole('tab', { name: '详情' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '版本' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '来源' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '复制文件' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '移动到文件夹' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '导出到云存储' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: '版本' }));
    expect(await screen.findByRole('button', { name: /版本 1/ })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: '来源' }));
    expect(await screen.findByText('由质量控制流程生成')).toBeInTheDocument();
  });

  it('opens an archive detail as a native archive preview, not the external-original fallback', async () => {
    loadArtifactMock.mockResolvedValueOnce({
      ...(await loadArtifactMock()),
      filename: 'bundle.zip',
      contentType: 'application/zip',
    });
    const request = vi.fn(async () =>
      Response.json({
        filename: 'bundle.zip',
        containers: [],
        entries: [{ path: 'notes.md', name: 'notes.md', size: 42, directory: false, archive: false }],
      })
    );
    vi.stubGlobal('fetch', request);
    try {
      await render(<ArtifactPreview />);
      expect(await screen.findByTestId('archive-viewer')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /notes\.md/ })).toBeInTheDocument();
      expect(request).toHaveBeenCalledWith(expect.stringContaining('/versions/version-1/archive'), expect.any(Object));
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('exports the artifact to a connected cloud bucket', async () => {
    await render(<ArtifactPreview />);
    await screen.findByRole('heading', { name: 'qc_metrics.png' });

    fireEvent.click(screen.getByRole('button', { name: '导出到云存储' }));
    const dialog = await screen.findByRole('dialog', { name: '导出到云存储' });
    await waitFor(() => expect(loadCloudBucketsMock).toHaveBeenCalledWith('cloud-1'));
    expect(within(dialog).getByRole('combobox', { name: '导出 bucket' })).toHaveTextContent('bucket-a');
    fireEvent.change(within(dialog).getByRole('textbox', { name: '云对象路径' }), {
      target: { value: 'reports/qc_metrics.png' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: '导出' }));

    await waitFor(() =>
      expect(exportArtifactMock).toHaveBeenCalledWith('cloud-1', {
        artifactId: 'artifact-1',
        bucket: 'bucket-a',
        key: 'reports/qc_metrics.png',
      })
    );
  });

  it('loads JSON into the structured preview and preserves the exact source view', async () => {
    const source = ' \r\n{"status":"completed","hits":3}\r\n ';
    loadArtifactMock.mockResolvedValueOnce({
      ...(await loadArtifactMock()),
      filename: 'analysis.json',
      contentType: 'application/json',
      sizeBytes: source.length,
    });
    const previousClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    const copiedJson = vi.fn(async (_text: string) => {});
    const copiedNotice = vi.spyOn(Message, 'success').mockImplementation(() => undefined as never);
    const failedNotice = vi.spyOn(Message, 'error').mockImplementation(() => undefined as never);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: copiedJson },
    });
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      if (String(input) === '/api/artifacts/artifact-1') {
        return new Response(source, {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      }
      return new Response(JSON.stringify({ data: {} }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    });

    try {
      await render(<ArtifactPreview />);

      await screen.findByRole('heading', { name: 'analysis.json' });
      const preview = await screen.findByLabelText('analysis.json 内容', {}, { timeout: 5_000 });
      expect(within(preview).getByRole('tab', { name: '结构' })).toHaveAttribute('aria-selected', 'true');
      expect(preview).toHaveTextContent('statuscompleted');
      expect(preview).toHaveTextContent('hits3');

      const sourceTab = within(preview).getByRole('tab', { name: '源码' });
      fireEvent.click(sourceTab);
      await waitFor(() => {
        expect(sourceTab).toHaveAttribute('aria-selected', 'true');
        expect(within(preview).getByTestId('artifact-source-editor').textContent).toBe(source);
      });
      fireEvent.click(within(preview).getByRole('button', { name: '复制 JSON' }));
      await waitFor(() => {
        expect(copiedJson).toHaveBeenCalledExactlyOnceWith(source);
        expect(copiedNotice).toHaveBeenCalledTimes(1);
      });
      expect(failedNotice).not.toHaveBeenCalled();
      expect(fetchMock).toHaveBeenCalledWith('/api/artifacts/artifact-1', {
        credentials: 'same-origin',
        headers: {
          Accept: 'text/plain, text/markdown, application/json, text/csv, text/tab-separated-values, text/html',
        },
        signal: expect.any(AbortSignal),
      });
    } finally {
      fetchMock.mockRestore();
      if (previousClipboard) Object.defineProperty(navigator, 'clipboard', previousClipboard);
      else Reflect.deleteProperty(navigator, 'clipboard');
    }
  });

  it('renders artifact navigation and inspector chrome in English', async () => {
    await renderWithI18n(<ArtifactPreview />, 'en-US');

    expect(await screen.findByRole('heading', { name: 'qc_metrics.png' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Back to project' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Details' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Versions' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy file' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Move to folder' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Export to cloud storage' })).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: '详情' })).not.toBeInTheDocument();
  });

  it.each([
    { filename: 'assay-audio.wav', contentType: 'audio/wav', tagName: 'AUDIO' },
    { filename: 'assay-video.mp4', contentType: 'video/mp4', tagName: 'VIDEO' },
  ])('renders $contentType with loading, playback and bounded error states', async (media) => {
    loadArtifactMock.mockResolvedValueOnce({
      ...(await loadArtifactMock()),
      filename: media.filename,
      contentType: media.contentType,
    });

    await render(<ArtifactPreview />);

    const player = await screen.findByLabelText(media.filename);
    expect(player.tagName).toBe(media.tagName);
    expect(player).toHaveAttribute('src', '/api/artifacts/artifact-1');
    expect(player).toHaveAttribute('preload', 'metadata');
    expect(player).toHaveAttribute('controls');
    expect(screen.getByRole('status')).toBeInTheDocument();

    fireEvent.loadedMetadata(player);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(player).toHaveStyle({ display: 'block' });

    fireEvent.error(player);
    expect(await screen.findByRole('alert')).toHaveTextContent(
      media.tagName === 'AUDIO' ? '音频加载失败' : '视频加载失败'
    );
  });

  it.each([
    { Component: AudioPreview, filename: 'assay-audio.wav', tagName: 'AUDIO' },
    { Component: VideoPreview, filename: 'assay-video.mp4', tagName: 'VIDEO' },
  ])('resets and releases the $tagName player lifecycle', async ({ Component, filename, tagName }) => {
    const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const firstUrl = `/api/artifacts/${tagName.toLowerCase()}-1`;
    const secondUrl = `/api/artifacts/${tagName.toLowerCase()}-2`;
    const view = await render(<Component url={firstUrl} filename={filename} />);

    const firstPlayer = await screen.findByLabelText(filename);
    view.rerender(<Component url={secondUrl} filename={filename} />);
    await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument());
    const secondPlayer = await screen.findByLabelText(filename);
    expect(secondPlayer).not.toBe(firstPlayer);
    expect(secondPlayer).toHaveAttribute('src', secondUrl);
    if (tagName === 'VIDEO') expect(secondPlayer).toHaveAttribute('playsinline');

    fireEvent.error(firstPlayer);
    fireEvent.loadedMetadata(firstPlayer);
    fireEvent.canPlay(firstPlayer);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toBeInTheDocument();

    fireEvent.canPlay(secondPlayer);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    document.dispatchEvent(new Event('visibilitychange'));
    expect(pause).toHaveBeenCalledTimes(1);

    fireEvent.error(secondPlayer);
    const alert = await screen.findByRole('alert');
    expect(alert).not.toHaveTextContent(secondUrl);

    view.unmount();
    document.dispatchEvent(new Event('visibilitychange'));
    expect(pause).toHaveBeenCalledTimes(1);
  });
});
