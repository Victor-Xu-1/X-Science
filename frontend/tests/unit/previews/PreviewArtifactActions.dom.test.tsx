import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  t: (key: string) => key,
  navigate: vi.fn(),
  loadArtifact: vi.fn(),
  loadFolders: vi.fn(),
  setPriority: vi.fn(),
  rename: vi.fn(),
  remove: vi.fn(),
  copyArtifact: vi.fn(),
  moveArtifact: vi.fn(),
  loadStorage: vi.fn(),
  loadBuckets: vi.fn(),
  exportCloud: vi.fn(),
  copyText: vi.fn(),
  download: vi.fn(),
  addToChat: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock('react-router', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceArtifact: mocks.loadArtifact,
  loadXScienceProjectFolders: mocks.loadFolders,
}));
vi.mock('@/renderer/services/xScienceArtifacts', () => ({
  setXScienceArtifactPriority: mocks.setPriority,
  renameXScienceArtifact: mocks.rename,
  deleteXScienceArtifact: mocks.remove,
  copyXScienceArtifact: mocks.copyArtifact,
  moveXScienceArtifact: mocks.moveArtifact,
}));
vi.mock('@/renderer/services/xScienceWorkspaceSettings', () => ({
  loadXScienceStorageSettings: mocks.loadStorage,
  loadXScienceCloudBuckets: mocks.loadBuckets,
  exportXScienceArtifactToCloud: mocks.exportCloud,
}));
vi.mock('@/renderer/utils/ui/clipboard', () => ({ copyText: mocks.copyText }));
vi.mock('@/renderer/utils/file/download', () => ({ downloadFileFromUrl: mocks.download }));
vi.mock('@/renderer/components/chat/SendBox/composerReferenceBridge', () => ({
  insertArtifactReferenceIntoActiveComposer: mocks.addToChat,
}));
vi.mock('@/renderer/components/common/AccessibleActionDialog', () => ({
  default: ({ visible, title, children, onConfirm }: any) =>
    visible ? (
      <div role='alertdialog' aria-label={title}>
        {children}
        <button type='button' data-testid='delete-confirm' onClick={() => void onConfirm()}>
          confirm
        </button>
      </div>
    ) : null,
}));
vi.mock('@arco-design/web-react', () => {
  const Menu = ({ children, ...props }: any) => <div {...props}>{children}</div>;
  Menu.Item = ({ children, onClick, disabled }: any) => (
    <button type='button' disabled={disabled} onClick={onClick}>
      {children}
    </button>
  );
  const Modal = ({ visible, title, children, onOk, onCancel }: any) =>
    visible ? (
      <div role='dialog' aria-label={title}>
        {children}
        <button type='button' data-testid='modal-ok' onClick={onOk}>
          ok
        </button>
        <button type='button' data-testid='modal-cancel' onClick={onCancel}>
          cancel
        </button>
      </div>
    ) : null;
  const Input = ({ value, onChange, ...props }: any) => (
    <input {...props} value={value} onChange={(event) => onChange(event.target.value)} />
  );
  const Select = ({ value, onChange, children, ...props }: any) => (
    <select {...props} value={value} onChange={(event) => onChange(event.target.value)}>
      {children}
    </select>
  );
  Select.Option = ({ value, children }: any) => <option value={value}>{children}</option>;
  return {
    Dropdown: ({ children, droplist, popupVisible, onVisibleChange }: any) => (
      <>
        {React.cloneElement(children, {
          onClick: () => onVisibleChange?.(!popupVisible),
        })}
        {popupVisible ? droplist : null}
      </>
    ),
    Empty: ({ description }: any) => <div>{description}</div>,
    Input,
    Menu,
    Modal,
    Select,
    Message: { useMessage: () => [{ success: mocks.success, error: mocks.error }, null] },
  };
});

import PreviewArtifactActions from '@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewArtifactActions';

const clickAndFlush = async (element: HTMLElement) => {
  await act(async () => {
    fireEvent.click(element);
    await Promise.resolve();
  });
};

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((accept, decline) => {
    resolve = accept;
    reject = decline;
  });
  return { promise, resolve, reject };
};

const artifact = {
  artifactId: 'artifact-1',
  versionId: 'version-1',
  versionNumber: 1,
  projectId: 'project-1',
  rootFrameId: 'frame-1',
  frameId: 'frame-1',
  creatingFrameId: 'frame-1',
  filename: 'report.md',
  contentType: 'text/markdown',
  sizeBytes: 10,
  createdAt: null,
  updatedAt: null,
  checksum: null,
  filePath: null,
  folderId: null,
  priority: null,
  isUserUpload: false,
  agentName: null,
  isIntermediate: false,
};

describe('PreviewArtifactActions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.location.hash = '#/conversation/frame-1';
    mocks.loadArtifact.mockResolvedValue(artifact);
    mocks.loadFolders.mockResolvedValue([{ folderId: 'folder-1', name: 'Results' }]);
    mocks.loadStorage.mockResolvedValue({ cloudCredentials: [{ id: 'cloud-1', name: 'Cloud', connected: true }] });
    mocks.loadBuckets.mockResolvedValue(['bucket-1']);
    mocks.setPriority.mockResolvedValue({});
    mocks.rename.mockResolvedValue({});
    mocks.remove.mockResolvedValue({});
    mocks.copyArtifact.mockResolvedValue({});
    mocks.moveArtifact.mockResolvedValue({});
    mocks.exportCloud.mockResolvedValue({});
    mocks.copyText.mockResolvedValue(undefined);
    mocks.download.mockResolvedValue(undefined);
    mocks.addToChat.mockReturnValue(true);
  });

  it('keeps the selected historical version and filename for references and links', async () => {
    mocks.loadArtifact.mockResolvedValue({ ...artifact, versionId: 'current-version', filename: 'current.md' });
    render(
      <PreviewArtifactActions
        artifactId='artifact-1'
        versionId='historical-version'
        fileName='historical.md'
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
      />
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.addToChat'));
    expect(mocks.addToChat).toHaveBeenCalledWith({
      artifactId: 'artifact-1',
      versionId: 'historical-version',
      filename: 'historical.md',
      conversationId: 'frame-1',
    });

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.copyLink'));
    expect(mocks.copyText).toHaveBeenCalledWith(
      `${window.location.origin}/#/artifacts/artifact-1?version=historical-version`
    );
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.actions.provenance'));
    expect(mocks.navigate).toHaveBeenCalledWith('/artifacts/artifact-1?tab=lineage&version=historical-version');
  });

  it('does not substitute the latest version for an explicitly empty selection', async () => {
    render(
      <PreviewArtifactActions
        artifactId='artifact-1'
        versionId=''
        fileName='report.md'
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
      />
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.addToChat'));
    expect(mocks.addToChat).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.copyLink'));
    expect(mocks.copyText).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith('preview.artifact.actions.linkCopyFailed');
  });

  it('ignores metadata from a replaced resource even when that read settles last', async () => {
    const oldRead = deferred<typeof artifact>();
    mocks.loadArtifact
      .mockImplementationOnce(() => oldRead.promise)
      .mockResolvedValue({
        ...artifact,
        artifactId: 'artifact-2',
        versionId: 'version-2',
        rootFrameId: 'frame-2',
        priority: 'user_starred',
      });
    const callbacks = { onRenamed: vi.fn(), onDeleted: vi.fn() };
    const { rerender } = render(
      <PreviewArtifactActions {...callbacks} artifactId='artifact-1' versionId='version-1' fileName='first.md' />
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(1));
    rerender(
      <PreviewArtifactActions {...callbacks} artifactId='artifact-2' versionId='version-2' fileName='second.md' />
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(2));
    await act(async () => oldRead.resolve(artifact));
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    expect(screen.getByText('preview.artifact.actions.unstar')).toBeInTheDocument();
    fireEvent.click(screen.getByText('preview.artifact.actions.viewInContext'));
    expect(mocks.navigate).toHaveBeenCalledWith('/conversation/frame-2');
    const oldSignal = mocks.loadArtifact.mock.calls[0][1]?.signal;
    expect(oldSignal).toBeInstanceOf(AbortSignal);
    expect(oldSignal.aborted).toBe(true);
  });

  it('does not show an old metadata error in a reopened preview with the same identity', async () => {
    const oldRead = deferred<typeof artifact>();
    mocks.loadArtifact.mockImplementationOnce(() => oldRead.promise).mockResolvedValue(artifact);
    const props = {
      artifactId: 'artifact-1',
      versionId: 'version-1',
      fileName: 'report.md',
      onRenamed: vi.fn(),
      onDeleted: vi.fn(),
    };
    const oldWindow = render(<PreviewArtifactActions {...props} />);
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(1));
    oldWindow.unmount();
    render(<PreviewArtifactActions {...props} />);
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(2));
    await act(async () => oldRead.reject(new Error('retired read failed')));
    expect(mocks.error).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.addToChat'));
    expect(mocks.addToChat).toHaveBeenCalledWith(expect.objectContaining({ versionId: 'version-1' }));
  });

  it('does not let a retired rename update or close a reopened same-version window', async () => {
    const pendingRename = deferred<Record<string, never>>();
    mocks.rename.mockReturnValue(pendingRename.promise);
    const oldRenamed = vi.fn();
    const props = { artifactId: 'artifact-1', versionId: 'version-1', fileName: 'report.md', onDeleted: vi.fn() };
    const oldWindow = render(<PreviewArtifactActions {...props} onRenamed={oldRenamed} />);
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await waitFor(() => expect(screen.getByText('preview.artifact.actions.rename')).toBeEnabled());
    fireEvent.click(screen.getByText('preview.artifact.actions.rename'));
    fireEvent.change(screen.getByLabelText('preview.artifact.actions.fileName'), { target: { value: 'old-write.md' } });
    await clickAndFlush(screen.getByTestId('modal-ok'));
    expect(mocks.rename).toHaveBeenCalledWith({ artifactId: 'artifact-1', filename: 'old-write.md' });
    oldWindow.unmount();

    const newRenamed = vi.fn();
    render(<PreviewArtifactActions {...props} onRenamed={newRenamed} />);
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await waitFor(() => expect(screen.getByText('preview.artifact.actions.rename')).toBeEnabled());
    fireEvent.click(screen.getByText('preview.artifact.actions.rename'));
    fireEvent.change(screen.getByLabelText('preview.artifact.actions.fileName'), { target: { value: 'new-draft.md' } });
    await act(async () => pendingRename.resolve({}));
    expect(oldRenamed).not.toHaveBeenCalled();
    expect(newRenamed).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalledWith('preview.artifact.actions.renamed');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByLabelText('preview.artifact.actions.fileName')).toHaveValue('new-draft.md');
  });

  it('does not reactivate a retired metadata request during StrictMode effect replay', async () => {
    const oldRead = deferred<typeof artifact>();
    mocks.loadArtifact.mockImplementationOnce(() => oldRead.promise).mockResolvedValue(artifact);
    render(
      <React.StrictMode>
        <PreviewArtifactActions
          artifactId='artifact-1'
          versionId='version-1'
          fileName='report.md'
          onRenamed={vi.fn()}
          onDeleted={vi.fn()}
        />
      </React.StrictMode>
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(2));
    await act(async () => oldRead.reject(new Error('obsolete activation failed')));
    expect(mocks.error).not.toHaveBeenCalled();
    expect(mocks.loadArtifact.mock.calls[0][1].signal.aborted).toBe(true);
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.addToChat'));
    expect(mocks.addToChat).toHaveBeenCalledWith(expect.objectContaining({ versionId: 'version-1' }));
  });

  it('keeps a cancelled folder read from replacing the choices of a reopened dialog', async () => {
    const oldFolders = deferred<Array<{ folderId: string; name: string }>>();
    mocks.loadFolders
      .mockImplementationOnce(() => oldFolders.promise)
      .mockResolvedValue([{ folderId: 'fresh-folder', name: 'Fresh folder' }]);
    render(
      <PreviewArtifactActions
        artifactId='artifact-1'
        versionId='version-1'
        fileName='report.md'
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
      />
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await waitFor(() => expect(screen.getByText('preview.artifact.moveToFolder')).toBeEnabled());
    await clickAndFlush(screen.getByText('preview.artifact.moveToFolder'));
    expect(mocks.loadFolders).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId('modal-cancel'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.moveToFolder'));
    expect(await screen.findByRole('option', { name: 'Fresh folder' })).toBeInTheDocument();
    await act(async () => oldFolders.resolve([{ folderId: 'old-folder', name: 'Retired folder' }]));
    expect(screen.getByRole('option', { name: 'Fresh folder' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Retired folder' })).not.toBeInTheDocument();
    expect(mocks.loadFolders.mock.calls[0][1].signal.aborted).toBe(true);
  });

  it('exposes every consolidated file action and wires non-destructive actions', async () => {
    render(
      <PreviewArtifactActions
        artifactId='artifact-1'
        versionId='version-1'
        fileName='report.md'
        onRenamed={vi.fn()}
        onDeleted={vi.fn()}
      />
    );
    await waitFor(() =>
      expect(mocks.loadArtifact).toHaveBeenCalledWith('artifact-1', { signal: expect.any(AbortSignal) })
    );
    expect(mocks.loadArtifact).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));

    const actionMenu = screen.getByRole('menu', { name: 'preview.artifact.actions.more' });
    const actionItems = within(actionMenu).getAllByRole('menuitem');
    expect(actionItems).toHaveLength(14);
    expect(actionMenu).toHaveClass('grid-cols-2');
    expect(actionMenu).toHaveClass('app-overlay-menu');
    for (const actionItem of actionItems) {
      expect(actionItem.querySelector('svg')).not.toBeNull();
    }

    expect(screen.getByText('preview.artifact.actions.star')).toBeInTheDocument();
    expect(screen.getByText('preview.artifact.actions.hide')).toBeInTheDocument();
    expect(screen.getByText('preview.artifact.actions.provenance')).toBeInTheDocument();
    expect(screen.getByText('preview.artifact.actions.versionHistory')).toBeInTheDocument();
    expect(screen.getByText('preview.artifact.actions.copyLink')).toBeInTheDocument();
    expect(screen.getByText('preview.artifact.actions.exportMetadata')).toBeInTheDocument();
    expect(screen.getByText('preview.artifact.exportToCloud')).toBeInTheDocument();

    await clickAndFlush(screen.getByText('preview.artifact.actions.star'));
    await waitFor(() =>
      expect(mocks.setPriority).toHaveBeenCalledWith({ artifactId: 'artifact-1', priority: 'user_starred' })
    );
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.unstar'));
    await waitFor(() =>
      expect(mocks.setPriority).toHaveBeenLastCalledWith({ artifactId: 'artifact-1', priority: 'user_no_priority' })
    );

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.hide'));
    await waitFor(() =>
      expect(mocks.setPriority).toHaveBeenLastCalledWith({ artifactId: 'artifact-1', priority: 'user_hidden' })
    );
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.unhide'));
    await waitFor(() =>
      expect(mocks.setPriority).toHaveBeenLastCalledWith({ artifactId: 'artifact-1', priority: 'user_no_priority' })
    );

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.copyLink'));
    await waitFor(() => expect(mocks.copyText).toHaveBeenCalledWith(expect.stringContaining('#/artifacts/artifact-1')));

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.actions.exportMetadata'));
    await waitFor(() =>
      expect(mocks.download).toHaveBeenCalledWith(
        '/api/artifacts/artifact-1?include_metadata=true',
        'report.md.metadata.json'
      )
    );

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.addToChat'));
    expect(mocks.addToChat).toHaveBeenCalledWith({
      filename: 'report.md',
      artifactId: 'artifact-1',
      versionId: 'version-1',
      conversationId: 'frame-1',
    });

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.actions.viewInContext'));
    expect(mocks.navigate).toHaveBeenCalledWith('/conversation/frame-1');
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.actions.provenance'));
    expect(mocks.navigate).toHaveBeenCalledWith('/artifacts/artifact-1?tab=lineage&version=version-1');
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.actions.versionHistory'));
    expect(mocks.navigate).toHaveBeenCalledWith('/artifacts/artifact-1?tab=versions&version=version-1');
    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.actions.details'));
    expect(mocks.navigate).toHaveBeenCalledWith('/artifacts/artifact-1?version=version-1');
  });

  it('wires rename, copy, move, cloud export and destructive confirmation', async () => {
    const onRenamed = vi.fn();
    const onDeleted = vi.fn();
    render(
      <PreviewArtifactActions
        artifactId='artifact-1'
        versionId='version-1'
        fileName='report.md'
        onRenamed={onRenamed}
        onDeleted={onDeleted}
      />
    );
    await waitFor(() => expect(mocks.loadArtifact).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await waitFor(() => expect(screen.getByText('preview.artifact.actions.rename')).toBeEnabled());
    fireEvent.click(screen.getByText('preview.artifact.actions.rename'));
    expect(screen.queryByRole('menu', { name: 'preview.artifact.actions.more' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('preview.artifact.actions.fileName'), { target: { value: 'renamed.md' } });
    await clickAndFlush(screen.getByTestId('modal-ok'));
    await waitFor(() =>
      expect(mocks.rename).toHaveBeenCalledWith({ artifactId: 'artifact-1', filename: 'renamed.md' })
    );
    expect(onRenamed).toHaveBeenCalledWith('renamed.md');

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.copyFile'));
    expect(screen.queryByRole('menu', { name: 'preview.artifact.actions.more' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('preview.artifact.copiedFilename'), { target: { value: 'copy.md' } });
    await clickAndFlush(screen.getByTestId('modal-ok'));
    await waitFor(() =>
      expect(mocks.copyArtifact).toHaveBeenCalledWith({ artifactId: 'artifact-1', newFilename: 'copy.md' })
    );

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.moveToFolder'));
    expect(screen.queryByRole('menu', { name: 'preview.artifact.actions.more' })).not.toBeInTheDocument();
    await waitFor(() =>
      expect(mocks.loadFolders).toHaveBeenCalledWith('project-1', { signal: expect.any(AbortSignal) })
    );
    fireEvent.change(screen.getByLabelText('preview.artifact.targetFolder'), { target: { value: 'folder-1' } });
    await clickAndFlush(screen.getByTestId('modal-ok'));
    await waitFor(() =>
      expect(mocks.moveArtifact).toHaveBeenCalledWith({ artifactId: 'artifact-1', folderId: 'folder-1' })
    );

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    await clickAndFlush(screen.getByText('preview.artifact.exportToCloud'));
    expect(screen.queryByRole('menu', { name: 'preview.artifact.actions.more' })).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.loadBuckets).toHaveBeenCalledWith('cloud-1', { signal: expect.any(AbortSignal) }));
    await clickAndFlush(screen.getByTestId('modal-ok'));
    await waitFor(() =>
      expect(mocks.exportCloud).toHaveBeenCalledWith('cloud-1', {
        artifactId: 'artifact-1',
        bucket: 'bucket-1',
        key: 'report.md',
      })
    );

    fireEvent.click(screen.getByTestId('preview-artifact-actions-trigger'));
    fireEvent.click(screen.getByText('preview.artifact.actions.delete'));
    expect(screen.queryByRole('menu', { name: 'preview.artifact.actions.more' })).not.toBeInTheDocument();
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    await clickAndFlush(screen.getByTestId('delete-confirm'));
    await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith('artifact-1'));
    expect(onDeleted).toHaveBeenCalled();
  });
});
