import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const listLocalHistoryMock = vi.fn();
const saveLocalHistoryMock = vi.fn();
const saveContentMock = vi.fn(async () => true);
const getLocalHistoryContentMock = vi.fn();
const loadArtifactVersionsMock = vi.fn(async (_artifactId: string) => [
  {
    versionId: 'version-2',
    versionNumber: 2,
    artifactId: 'artifact-1',
    frameId: 'frame-1',
    agentName: 'OPERON',
    language: 'markdown',
    contentType: 'text/markdown',
    sizeBytes: 128,
    createdAt: '2026-07-11T02:00:00.000Z',
    filePath: '/state/v2-report.md',
    parentVersionId: 'version-1',
  },
]);
const loadVersionTextMock = vi.fn(async (_versionId: string) => '# version 2');
const createArtifactVersionMock = vi.fn(
  async (_input: import('@/renderer/services/xScienceArtifacts').XScienceTextArtifactVersionInput) => ({
    artifactId: 'artifact-1',
    versionId: 'version-3',
    filename: 'report.md',
    folderId: null,
    raw: {},
  })
);
const messageMocks = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }));
const translateMock = vi.hoisted(
  () => (key: string, options?: { version?: number }) =>
    key === 'preview.versionLabel' ? `版本 ${options?.version ?? ''}` : key
);

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: translateMock }),
}));

vi.mock('@arco-design/web-react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...original,
    Message: {
      ...original.Message,
      useMessage: () => [messageMocks, null],
    },
  };
});

vi.mock('@/common', () => ({
  ipcBridge: {
    previewHistory: {
      list: { invoke: (...args: unknown[]) => listLocalHistoryMock(...args) },
      save: { invoke: (...args: unknown[]) => saveLocalHistoryMock(...args) },
      getContent: { invoke: (...args: unknown[]) => getLocalHistoryContentMock(...args) },
    },
  },
}));

vi.mock('@/renderer/services/xScienceArtifacts', () => ({
  loadXScienceArtifactVersions: (...args: Parameters<typeof loadArtifactVersionsMock>) =>
    loadArtifactVersionsMock(...args),
  loadXScienceArtifactVersionText: (...args: Parameters<typeof loadVersionTextMock>) => loadVersionTextMock(...args),
  getXScienceArtifactVersionContentUrl: (versionId: string) => `/api/artifacts/versions/${versionId}`,
  createXScienceTextArtifactVersion: (...args: Parameters<typeof createArtifactVersionMock>) =>
    createArtifactVersionMock(...args),
}));

import { usePreviewHistory } from '@/renderer/pages/conversation/Preview/hooks/usePreviewHistory';

describe('X-Science preview history integration', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('loads and selects backend artifact versions through the existing XScience preview history hook', async () => {
    const updateContent = vi.fn();
    const markContentSaved = vi.fn();
    const activeTab = {
      content_type: 'markdown',
      content: '# current',
      title: 'report.md',
      metadata: {
        file_name: 'report.md',
        artifactId: 'artifact-1',
        versionId: 'version-1',
        contentUrl: '/api/artifacts/artifact-1',
      },
    };
    const { result } = renderHook(() =>
      usePreviewHistory({
        activeTab,
        updateContent,
        markContentSaved,
        saveContent: saveContentMock,
      })
    );

    await waitFor(() => expect(result.current.historyVersions).toHaveLength(1));
    expect(loadArtifactVersionsMock).toHaveBeenCalledWith('artifact-1');
    expect(listLocalHistoryMock).not.toHaveBeenCalled();
    expect(result.current.historyVersions[0]).toMatchObject({
      id: 'version-2',
      label: '版本 2',
      file_path: '/api/artifacts/versions/version-2',
    });

    await act(async () => {
      await result.current.handleSnapshotSelect(result.current.historyVersions[0]!);
    });

    expect(loadVersionTextMock).toHaveBeenCalledWith('version-2');
    expect(updateContent).toHaveBeenCalledWith('# version 2');
    expect(markContentSaved).toHaveBeenCalledWith({
      versionId: 'version-2',
      contentUrl: '/api/artifacts/versions/version-2',
    });
    expect(getLocalHistoryContentMock).not.toHaveBeenCalled();
  });

  it('delegates remote saving to the shared PreviewContext owner without a competing write', async () => {
    const markContentSaved = vi.fn();
    const activeTab = {
      content_type: 'markdown',
      content: '# edited report',
      title: 'report.md',
      metadata: {
        file_name: 'report.md',
        artifactId: 'artifact-1',
        versionId: 'version-2',
        contentUrl: '/api/artifacts/versions/version-2',
      },
    };
    const { result } = renderHook(() =>
      usePreviewHistory({
        activeTab,
        updateContent: vi.fn(),
        markContentSaved,
        saveContent: saveContentMock,
      })
    );

    await waitFor(() => expect(result.current.historyVersions).toHaveLength(1));
    let saved = false;
    await act(async () => {
      saved = await result.current.handleSaveSnapshot();
    });

    expect(saved).toBe(true);
    expect(saveContentMock).toHaveBeenCalledTimes(1);
    expect(createArtifactVersionMock).not.toHaveBeenCalled();
    expect(markContentSaved).not.toHaveBeenCalled();
  });

  it('ignores a late history selection after the displayed artifact changes', async () => {
    let resolve!: (value: string) => void;
    loadVersionTextMock.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      })
    );
    const updateContent = vi.fn(),
      markContentSaved = vi.fn();
    const activeTab = {
      content_type: 'markdown',
      content: 'first',
      title: 'first.md',
      metadata: { artifactId: 'artifact-1', versionId: 'version-1' },
    };
    const hook = renderHook(
      ({ activeTab }) =>
        usePreviewHistory({ activeTab, updateContent, markContentSaved, saveContent: saveContentMock }),
      { initialProps: { activeTab } }
    );
    await waitFor(() => expect(hook.result.current.historyVersions).toHaveLength(1));
    let pending!: Promise<void>;
    act(() => {
      pending = hook.result.current.handleSnapshotSelect(hook.result.current.historyVersions[0]!);
    });
    hook.rerender({ activeTab: { ...activeTab, metadata: { artifactId: 'artifact-2', versionId: 'other-version' } } });
    await act(async () => {
      resolve('old history');
      await pending;
    });
    expect(updateContent).not.toHaveBeenCalled();
    expect(markContentSaved).not.toHaveBeenCalled();
  });
});
