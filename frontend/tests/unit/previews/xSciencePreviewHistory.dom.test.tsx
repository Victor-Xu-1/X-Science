import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const listLocalHistoryMock = vi.fn();
const saveLocalHistoryMock = vi.fn();
const saveContentMock = vi.fn(async () => true);
const loadArtifactVersionsMock = vi.fn(async (_artifactId: string) => []);
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
const translateMock = vi.hoisted(() => (key: string) => key);

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translateMock }) }));
vi.mock('@arco-design/web-react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@arco-design/web-react')>();
  return {
    ...original,
    Message: { ...original.Message, useMessage: () => [messageMocks, null] },
  };
});
vi.mock('@/common', () => ({
  ipcBridge: {
    previewHistory: {
      list: { invoke: (...args: unknown[]) => listLocalHistoryMock(...args) },
      save: { invoke: (...args: unknown[]) => saveLocalHistoryMock(...args) },
    },
  },
}));
vi.mock('@/renderer/services/xScienceArtifacts', () => ({
  loadXScienceArtifactVersions: (...args: Parameters<typeof loadArtifactVersionsMock>) =>
    loadArtifactVersionsMock(...args),
  createXScienceTextArtifactVersion: (...args: Parameters<typeof createArtifactVersionMock>) =>
    createArtifactVersionMock(...args),
}));

import { usePreviewHistory } from '@/renderer/pages/conversation/Preview/hooks/usePreviewHistory';

const previewForUnusedReadCheck = (target: 'artifact' | 'local') => ({
  id: 'read-check-tab',
  content_type: 'markdown',
  content: '# edited synthetic report',
  title: 'synthetic.md',
  metadata:
    target === 'artifact'
      ? { artifactId: 'artifact-1', versionId: 'version-1', file_name: 'synthetic.md' }
      : { workspace: '/synthetic', file_path: '/synthetic/synthetic.md', file_name: 'synthetic.md' },
});

describe('X-Science preview history integration', () => {
  afterEach(() => {
    vi.clearAllMocks();
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
        markContentSaved,
        saveContent: saveContentMock,
      })
    );

    let saved = false;
    await act(async () => {
      saved = await result.current.handleSaveSnapshot();
    });

    expect(saved).toBe(true);
    expect(saveContentMock).toHaveBeenCalledTimes(1);
    expect(createArtifactVersionMock).not.toHaveBeenCalled();
    expect(markContentSaved).not.toHaveBeenCalled();
  });
});

describe('unused preview history reads', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it.each(['artifact', 'local'] as const)('does not fetch unused history when a %s preview mounts', async (target) => {
    const { result } = renderHook(() =>
      usePreviewHistory({
        activeTab: previewForUnusedReadCheck(target),
        markContentSaved: vi.fn(),
        saveContent: saveContentMock,
      })
    );
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.historyTarget?.artifact_id).toBe(target === 'artifact' ? 'artifact-1' : undefined);
    expect(result.current.snapshotSaving).toBe(false);
    expect(loadArtifactVersionsMock).not.toHaveBeenCalled();
    expect(listLocalHistoryMock).not.toHaveBeenCalled();
  });

  it.each(['artifact', 'local'] as const)('does not fetch unused history after saving a %s preview', async (target) => {
    const markContentSaved = vi.fn();
    if (target === 'local') {
      saveLocalHistoryMock.mockResolvedValueOnce({
        id: 'local-snapshot',
        label: 'Synthetic saved snapshot',
        created_at: 1,
        size: 25,
        contentType: 'markdown',
      });
    }
    const { result } = renderHook(() =>
      usePreviewHistory({
        activeTab: previewForUnusedReadCheck(target),
        markContentSaved,
        saveContent: saveContentMock,
      })
    );
    await act(async () => {
      await Promise.resolve();
    });
    // Separate the post-save read from any existing mount read in the RED.
    loadArtifactVersionsMock.mockClear();
    listLocalHistoryMock.mockClear();
    await act(async () => {
      expect(await result.current.handleSaveSnapshot()).toBe(true);
    });
    expect(saveContentMock).toHaveBeenCalledTimes(target === 'artifact' ? 1 : 0);
    expect(saveLocalHistoryMock).toHaveBeenCalledTimes(target === 'local' ? 1 : 0);
    expect(markContentSaved).toHaveBeenCalledTimes(target === 'local' ? 1 : 0);
    expect(createArtifactVersionMock).not.toHaveBeenCalled();
    expect(messageMocks.success).toHaveBeenCalledWith('preview.snapshotSaved');
    expect(result.current.snapshotSaving).toBe(false);
    expect(loadArtifactVersionsMock).not.toHaveBeenCalled();
    expect(listLocalHistoryMock).not.toHaveBeenCalled();
  });
});
