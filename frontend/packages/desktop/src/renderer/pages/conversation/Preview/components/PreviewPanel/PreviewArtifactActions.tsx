import AccessibleActionDialog from '@/renderer/components/common/AccessibleActionDialog';
import { insertArtifactReferenceIntoActiveComposer } from '@/renderer/components/chat/SendBox/composerReferenceBridge';
import {
  copyXScienceArtifact,
  deleteXScienceArtifact,
  moveXScienceArtifact,
  renameXScienceArtifact,
  setXScienceArtifactPriority,
  type XScienceArtifactPriority,
} from '@/renderer/services/xScienceArtifacts';
import {
  loadXScienceArtifact,
  loadXScienceProjectFolders,
  type XScienceProjectArtifact,
  type XScienceProjectFolder,
} from '@/renderer/services/xScienceGateway';
import {
  exportXScienceArtifactToCloud,
  loadXScienceCloudBuckets,
  loadXScienceStorageSettings,
  type XScienceCloudCredential,
} from '@/renderer/services/xScienceWorkspaceSettings';
import { downloadFileFromUrl } from '@/renderer/utils/file/download';
import { copyText } from '@/renderer/utils/ui/clipboard';
import { Dropdown, Empty, Input, Message, Select } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import {
  AddOne,
  CloudStorage,
  Copy,
  Delete,
  DownloadOne,
  Edit,
  FileText,
  History,
  MessageOne,
  MoveOne,
  PreviewClose,
  Star,
  Trace,
} from '@icon-park/react';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { usePreviewArtifactActionScope } from './usePreviewArtifactActionScope';

const ROOT_FOLDER_VALUE = '__project_root__';

type PreviewArtifactActionsProps = {
  artifactId: string;
  versionId?: string;
  openingId?: string;
  fileName: string;
  onRenamed: (fileName: string) => void;
  onDeleted: () => void;
};

export const PreviewArtifactActions: React.FC<PreviewArtifactActionsProps> = ({
  artifactId,
  versionId,
  openingId,
  fileName,
  onRenamed,
  onDeleted,
}) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [messageApi, messageContextHolder] = Message.useMessage();
  const messageApiRef = useRef(messageApi);
  messageApiRef.current = messageApi;
  const identity = JSON.stringify([openingId ?? null, artifactId, versionId ?? null]);
  const { begin, invalidate } = usePreviewArtifactActionScope(identity);
  const [menuVisible, setMenuVisible] = useState(false);
  const [artifact, setArtifact] = useState<XScienceProjectArtifact | null>(null);
  const [loadingArtifact, setLoadingArtifact] = useState(true);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [renameVisible, setRenameVisible] = useState(false);
  const [renameValue, setRenameValue] = useState(fileName);
  const [copyVisible, setCopyVisible] = useState(false);
  const [copyValue, setCopyValue] = useState(fileName);
  const [moveVisible, setMoveVisible] = useState(false);
  const [folders, setFolders] = useState<XScienceProjectFolder[]>([]);
  const [moveFolderId, setMoveFolderId] = useState(ROOT_FOLDER_VALUE);
  const [deleteVisible, setDeleteVisible] = useState(false);
  const [cloudVisible, setCloudVisible] = useState(false);
  const [cloudCredentials, setCloudCredentials] = useState<XScienceCloudCredential[]>([]);
  const [cloudCredentialId, setCloudCredentialId] = useState('');
  const [cloudBuckets, setCloudBuckets] = useState<string[]>([]);
  const [cloudBucket, setCloudBucket] = useState('');
  const [cloudKey, setCloudKey] = useState(fileName);
  const currentArtifact = artifact?.artifactId === artifactId ? artifact : null;
  // Undefined means a logical/current artifact. An explicit empty selection is
  // unavailable, never permission to substitute the latest immutable version.
  const exactVersionId = versionId ?? currentArtifact?.versionId;

  const refreshArtifact = useCallback(async () => {
    const request = begin('artifact-read');
    if (!request) return;
    setLoadingArtifact(true);
    setArtifact(null);
    try {
      const next = await loadXScienceArtifact(artifactId, { signal: request.signal });
      if (!request.isCurrent()) return;
      if (next.artifactId !== artifactId) throw new Error('preview_artifact_identity_mismatch');
      setArtifact(next);
    } catch {
      if (request.isCurrent()) messageApiRef.current.error(t('preview.artifact.actions.loadFailed'));
    } finally {
      if (request.isCurrent()) setLoadingArtifact(false);
      request.finish();
    }
  }, [artifactId, identity, begin, t]);

  useEffect(() => {
    void refreshArtifact();
    return () => invalidate('artifact-read');
  }, [refreshArtifact, invalidate]);

  useEffect(() => {
    setRenameValue(fileName);
    setCopyValue(fileName);
    setCloudKey(fileName);
  }, [fileName]);

  const runPriorityAction = async (priority: XScienceArtifactPriority) => {
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('priority');
    try {
      await setXScienceArtifactPriority({ artifactId, priority });
      if (!request.isCurrent()) return;
      setArtifact((current) => (current ? { ...current, priority } : current));
      messageApi.success(t('preview.artifact.actions.priorityUpdated'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.actions.priorityFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const openArtifactPage = (tab?: 'lineage' | 'versions') => {
    if (versionId !== undefined && !exactVersionId) {
      messageApi.error(t('preview.artifact.actions.linkCopyFailed'));
      return;
    }
    const parameters = new URLSearchParams();
    if (tab) parameters.set('tab', tab);
    if (exactVersionId) parameters.set('version', exactVersionId);
    const queryString = parameters.toString();
    const query = queryString ? `?${queryString}` : '';
    void navigate(`/artifacts/${encodeURIComponent(artifactId)}${query}`);
  };

  const handleViewInContext = () => {
    const frameId = currentArtifact?.rootFrameId ?? currentArtifact?.creatingFrameId ?? currentArtifact?.frameId;
    if (!frameId) return;
    void navigate(`/conversation/${encodeURIComponent(frameId)}`);
  };

  const handleAddToChat = () => {
    const conversationId = currentConversationId();
    if (!conversationId || !exactVersionId) {
      messageApi.error(t('preview.artifact.actions.addToChatUnavailable'));
      return;
    }
    const inserted = insertArtifactReferenceIntoActiveComposer({
      filename: fileName,
      artifactId,
      versionId: exactVersionId,
      conversationId,
    });
    if (!inserted) {
      messageApi.error(t('preview.artifact.actions.addToChatUnavailable'));
      return;
    }
    messageApi.success(t('preview.artifact.actions.addedToChat'));
  };

  const handleCopyLink = async () => {
    if (!exactVersionId) {
      messageApi.error(t('preview.artifact.actions.linkCopyFailed'));
      return;
    }
    const request = begin('clipboard');
    if (!request) return;
    const link = `${window.location.origin}/#/artifacts/${encodeURIComponent(artifactId)}?version=${encodeURIComponent(exactVersionId)}`;
    try {
      await copyText(link);
      if (request.isCurrent()) messageApi.success(t('preview.artifact.actions.linkCopied'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.actions.linkCopyFailed'));
    } finally {
      request.finish();
    }
  };

  const handleRename = async () => {
    const filename = renameValue.trim();
    if (!filename) return;
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('rename');
    try {
      await renameXScienceArtifact({ artifactId, filename });
      if (!request.isCurrent()) return;
      setArtifact((current) => (current ? { ...current, filename } : current));
      onRenamed(filename);
      setRenameVisible(false);
      messageApi.success(t('preview.artifact.actions.renamed'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.actions.renameFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const handleCopyArtifact = async () => {
    const newFilename = copyValue.trim();
    if (!newFilename) return;
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('copy');
    try {
      await copyXScienceArtifact({ artifactId, newFilename });
      if (!request.isCurrent()) return;
      setCopyVisible(false);
      messageApi.success(t('preview.artifact.copySucceeded'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.copyFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const openMove = async () => {
    const request = begin('dialog-read');
    if (!request) return;
    setMoveVisible(true);
    const projectId = currentArtifact?.projectId;
    if (!projectId) {
      request.finish();
      return;
    }
    setBusyAction('folders');
    try {
      const next = await loadXScienceProjectFolders(projectId, { signal: request.signal });
      if (!request.isCurrent()) return;
      setFolders(next);
      setMoveFolderId(currentArtifact?.folderId ?? ROOT_FOLDER_VALUE);
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.actions.folderLoadFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const handleMove = async () => {
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('move');
    try {
      await moveXScienceArtifact({
        artifactId,
        folderId: moveFolderId === ROOT_FOLDER_VALUE ? null : moveFolderId,
      });
      if (!request.isCurrent()) return;
      setArtifact((current) =>
        current ? { ...current, folderId: moveFolderId === ROOT_FOLDER_VALUE ? null : moveFolderId } : current
      );
      setMoveVisible(false);
      messageApi.success(t('preview.artifact.moveSucceeded'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.moveFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const handleMetadataExport = async () => {
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('metadata');
    try {
      await downloadFileFromUrl(
        `/api/artifacts/${encodeURIComponent(artifactId)}?include_metadata=true`,
        `${artifact?.filename ?? fileName}.metadata.json`
      );
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.actions.metadataExportFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const loadCloudCredential = async (credentialId: string, credentials = cloudCredentials) => {
    const request = begin('cloud-buckets');
    if (!request) return;
    setCloudCredentialId(credentialId);
    setCloudBuckets([]);
    setCloudBucket('');
    if (!credentialId) {
      request.finish();
      return;
    }
    try {
      const buckets = await loadXScienceCloudBuckets(credentialId, { signal: request.signal });
      if (!request.isCurrent()) return;
      const credential = credentials.find((item) => item.id === credentialId);
      setCloudBuckets(buckets);
      setCloudBucket(credential?.defaultBucket || buckets[0] || '');
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.bucketLoadFailed'));
    } finally {
      request.finish();
    }
  };

  const openCloud = async () => {
    const request = begin('dialog-read');
    if (!request) return;
    setCloudVisible(true);
    setBusyAction('cloud-load');
    try {
      const storage = await loadXScienceStorageSettings({ signal: request.signal });
      if (!request.isCurrent()) return;
      const connected = storage.cloudCredentials.filter((item) => item.connected);
      setCloudCredentials(connected);
      const initial = connected[0]?.id ?? '';
      if (initial) await loadCloudCredential(initial, connected);
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.credentialsLoadFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const handleCloudExport = async () => {
    if (!cloudCredentialId || !cloudBucket || !cloudKey.trim()) return;
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('cloud-export');
    try {
      await exportXScienceArtifactToCloud(cloudCredentialId, {
        artifactId,
        bucket: cloudBucket,
        key: cloudKey.trim(),
      });
      if (!request.isCurrent()) return;
      setCloudVisible(false);
      messageApi.success(t('preview.artifact.exportSucceeded'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.exportFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const handleDelete = async () => {
    const request = begin('mutation', true);
    if (!request) return;
    setBusyAction('delete');
    try {
      await deleteXScienceArtifact(artifactId);
      if (!request.isCurrent()) return;
      setDeleteVisible(false);
      onDeleted();
      messageApi.success(t('preview.artifact.actions.deleted'));
    } catch {
      if (request.isCurrent()) messageApi.error(t('preview.artifact.actions.deleteFailed'));
    } finally {
      if (request.isCurrent()) setBusyAction(null);
      request.finish();
    }
  };

  const closeReadDialog = (close: () => void) => {
    invalidate('dialog-read');
    invalidate('cloud-buckets');
    setBusyAction((current) => (current === 'folders' || current === 'cloud-load' ? null : current));
    close();
  };
  const priority = currentArtifact?.priority;
  const contextFrameId = currentArtifact?.rootFrameId ?? currentArtifact?.creatingFrameId ?? currentArtifact?.frameId;
  const disabled = loadingArtifact || !currentArtifact || busyAction !== null;
  const actionDialogVisible = renameVisible || copyVisible || moveVisible || deleteVisible || cloudVisible;

  const handleMenuVisibilityChange = (visible: boolean) => {
    if (!actionDialogVisible) setMenuVisible(visible);
  };

  return (
    <>
      {messageContextHolder}
      <Dropdown
        trigger='click'
        position='br'
        popupVisible={menuVisible && !actionDialogVisible}
        onVisibleChange={handleMenuVisibilityChange}
        droplist={
          <div
            role='menu'
            aria-label={t('preview.artifact.actions.more')}
            className='app-overlay-menu isolate grid w-408px max-w-[calc(100vw-24px)] grid-cols-2 gap-2px overflow-hidden'
            data-testid='preview-artifact-actions-menu'
          >
            <ActionMenuItem
              icon={<Star theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                void runPriorityAction(priority === 'user_starred' ? 'user_no_priority' : 'user_starred');
              }}
            >
              {priority === 'user_starred' ? t('preview.artifact.actions.unstar') : t('preview.artifact.actions.star')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<PreviewClose theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                void runPriorityAction(priority === 'user_hidden' ? 'user_no_priority' : 'user_hidden');
              }}
            >
              {priority === 'user_hidden' ? t('preview.artifact.actions.unhide') : t('preview.artifact.actions.hide')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<MessageOne theme='outline' size={16} />}
              disabled={!contextFrameId}
              onClick={() => {
                setMenuVisible(false);
                handleViewInContext();
              }}
            >
              {t('preview.artifact.actions.viewInContext')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<Trace theme='outline' size={16} />}
              onClick={() => {
                setMenuVisible(false);
                openArtifactPage('lineage');
              }}
            >
              {t('preview.artifact.actions.provenance')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<History theme='outline' size={16} />}
              onClick={() => {
                setMenuVisible(false);
                openArtifactPage('versions');
              }}
            >
              {t('preview.artifact.actions.versionHistory')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<FileText theme='outline' size={16} />}
              onClick={() => {
                setMenuVisible(false);
                openArtifactPage();
              }}
            >
              {t('preview.artifact.actions.details')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<Copy theme='outline' size={16} />}
              onClick={() => {
                setMenuVisible(false);
                void handleCopyLink();
              }}
            >
              {t('preview.artifact.actions.copyLink')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<AddOne theme='outline' size={16} />}
              onClick={() => {
                setMenuVisible(false);
                handleAddToChat();
              }}
            >
              {t('preview.addToChat')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<Copy theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                setCopyValue(fileName);
                setCopyVisible(true);
              }}
            >
              {t('preview.artifact.copyFile')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<MoveOne theme='outline' size={16} />}
              disabled={disabled || !currentArtifact?.projectId}
              onClick={() => {
                setMenuVisible(false);
                void openMove();
              }}
            >
              {t('preview.artifact.moveToFolder')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<Edit theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                setRenameValue(fileName);
                setRenameVisible(true);
              }}
            >
              {t('preview.artifact.actions.rename')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<DownloadOne theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                void handleMetadataExport();
              }}
            >
              {t('preview.artifact.actions.exportMetadata')}
            </ActionMenuItem>
            <ActionMenuItem
              icon={<CloudStorage theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                void openCloud();
              }}
            >
              {t('preview.artifact.exportToCloud')}
            </ActionMenuItem>
            <ActionMenuItem
              danger
              icon={<Delete theme='outline' size={16} />}
              disabled={disabled}
              onClick={() => {
                setMenuVisible(false);
                setDeleteVisible(true);
              }}
            >
              {t('preview.artifact.actions.delete')}
            </ActionMenuItem>
          </div>
        }
      >
        <button
          type='button'
          aria-label={t('preview.artifact.actions.more')}
          title={t('preview.artifact.actions.more')}
          data-testid='preview-artifact-actions-trigger'
          className='size-30px inline-flex shrink-0 items-center justify-center border-0 rd-6px bg-transparent text-t-secondary hover:bg-fill-2 hover:text-t-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand'
        >
          <MoreIcon />
        </button>
      </Dropdown>

      <Modal
        title={t('preview.artifact.actions.rename')}
        visible={renameVisible}
        onCancel={() => setRenameVisible(false)}
        onOk={() => void handleRename()}
        confirmLoading={busyAction === 'rename'}
        okButtonProps={{ disabled: !renameValue.trim() }}
        style={{ width: 500, borderRadius: 14 }}
        unmountOnExit
      >
        <ActionModalBody hint={t('preview.artifact.actions.renameHint')}>
          <label className='text-13px font-500 text-t-primary' htmlFor='preview-artifact-rename-input'>
            {t('preview.artifact.actions.fileName')}
          </label>
          <Input
            id='preview-artifact-rename-input'
            aria-label={t('preview.artifact.actions.fileName')}
            size='large'
            value={renameValue}
            onChange={setRenameValue}
          />
        </ActionModalBody>
      </Modal>

      <Modal
        title={t('preview.artifact.copyFile')}
        visible={copyVisible}
        onCancel={() => setCopyVisible(false)}
        onOk={() => void handleCopyArtifact()}
        confirmLoading={busyAction === 'copy'}
        okButtonProps={{ disabled: !copyValue.trim() }}
        style={{ width: 500, borderRadius: 14 }}
        unmountOnExit
      >
        <ActionModalBody hint={t('preview.artifact.actions.copyHint')}>
          <label className='text-13px font-500 text-t-primary' htmlFor='preview-artifact-copy-input'>
            {t('preview.artifact.copiedFilename')}
          </label>
          <Input
            id='preview-artifact-copy-input'
            aria-label={t('preview.artifact.copiedFilename')}
            size='large'
            value={copyValue}
            onChange={setCopyValue}
          />
        </ActionModalBody>
      </Modal>

      <Modal
        title={t('preview.artifact.moveToFolder')}
        visible={moveVisible}
        onCancel={() => closeReadDialog(() => setMoveVisible(false))}
        onOk={() => void handleMove()}
        confirmLoading={busyAction === 'move' || busyAction === 'folders'}
        style={{ width: 500, borderRadius: 14 }}
        unmountOnExit
      >
        <ActionModalBody hint={t('preview.artifact.actions.moveHint')}>
          <label className='text-13px font-500 text-t-primary'>{t('preview.artifact.targetFolder')}</label>
          <Select
            className='w-full'
            size='large'
            aria-label={t('preview.artifact.targetFolder')}
            value={moveFolderId}
            onChange={setMoveFolderId}
          >
            <Select.Option value={ROOT_FOLDER_VALUE}>{t('preview.artifact.projectRoot')}</Select.Option>
            {folders.map((folder) => (
              <Select.Option key={folder.folderId} value={folder.folderId}>
                {folder.name}
              </Select.Option>
            ))}
          </Select>
        </ActionModalBody>
      </Modal>

      <Modal
        title={t('preview.artifact.exportToCloud')}
        visible={cloudVisible}
        onCancel={() => closeReadDialog(() => setCloudVisible(false))}
        onOk={() => void handleCloudExport()}
        confirmLoading={busyAction === 'cloud-load' || busyAction === 'cloud-export'}
        okButtonProps={{ disabled: !cloudCredentialId || !cloudBucket || !cloudKey.trim() }}
        style={{ width: 520, borderRadius: 14 }}
        unmountOnExit
      >
        <ActionModalBody hint={t('preview.artifact.actions.cloudHint')}>
          {cloudCredentials.length === 0 && busyAction !== 'cloud-load' ? (
            <div className='rd-12px bg-fill-1 px-12px py-18px'>
              <Empty description={t('preview.artifact.noCloudCredentials')} />
            </div>
          ) : (
            <div className='flex flex-col gap-12px'>
              <label className='text-13px font-500 text-t-primary'>{t('preview.artifact.cloudCredential')}</label>
              <Select
                className='w-full'
                size='large'
                aria-label={t('preview.artifact.cloudCredential')}
                value={cloudCredentialId}
                onChange={(value) => void loadCloudCredential(value)}
              >
                {cloudCredentials.map((credential) => (
                  <Select.Option key={credential.id} value={credential.id}>
                    {credential.name}
                  </Select.Option>
                ))}
              </Select>
              <label className='text-13px font-500 text-t-primary'>{t('preview.artifact.exportBucket')}</label>
              <Select
                className='w-full'
                size='large'
                aria-label={t('preview.artifact.exportBucket')}
                value={cloudBucket}
                onChange={setCloudBucket}
              >
                {cloudBuckets.map((bucket) => (
                  <Select.Option key={bucket} value={bucket}>
                    {bucket}
                  </Select.Option>
                ))}
              </Select>
              <label className='text-13px font-500 text-t-primary' htmlFor='preview-artifact-cloud-key-input'>
                {t('preview.artifact.cloudObjectPath')}
              </label>
              <Input
                id='preview-artifact-cloud-key-input'
                aria-label={t('preview.artifact.cloudObjectPath')}
                size='large'
                value={cloudKey}
                onChange={setCloudKey}
              />
            </div>
          )}
        </ActionModalBody>
      </Modal>

      <AccessibleActionDialog
        title={t('preview.artifact.actions.deleteTitle')}
        visible={deleteVisible}
        confirmText={t('preview.artifact.actions.delete')}
        cancelText={t('common.cancel')}
        danger
        busy={busyAction === 'delete'}
        initialFocus='cancel'
        onCancel={() => setDeleteVisible(false)}
        onConfirm={handleDelete}
      >
        <p className='m-0 text-13px leading-20px text-t-secondary'>
          {t('preview.artifact.actions.deleteConfirm', { name: artifact?.filename ?? fileName })}
        </p>
      </AccessibleActionDialog>
    </>
  );
};

const currentConversationId = (): string | null => {
  const match = window.location.hash.match(/^#\/conversation\/([^/?#]+)/);
  if (!match?.[1]) return null;
  try {
    const decoded = decodeURIComponent(match[1]);
    return /^[A-Za-z0-9_-]{1,256}$/.test(decoded) ? decoded : null;
  } catch {
    return null;
  }
};

type ActionMenuItemProps = {
  children: React.ReactNode;
  icon: React.ReactNode;
  disabled?: boolean;
  danger?: boolean;
  onClick: () => void;
};

const ActionModalBody: React.FC<{ children: React.ReactNode; hint: string }> = ({ children, hint }) => (
  <div className='flex flex-col gap-10px py-4px'>
    <p className='m-0 rd-10px bg-fill-1 px-12px py-10px text-12px leading-19px text-t-secondary'>{hint}</p>
    {children}
  </div>
);

const ActionMenuItem: React.FC<ActionMenuItemProps> = ({
  children,
  icon,
  disabled = false,
  danger = false,
  onClick,
}) => (
  <button
    type='button'
    role='menuitem'
    disabled={disabled}
    onClick={onClick}
    className={`h-36px min-w-0 flex items-center gap-9px border-0 rd-8px bg-transparent px-10px text-left text-13px leading-18px transition-colors focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand disabled:cursor-not-allowed disabled:opacity-45 ${
      danger ? 'text-danger-6 hover:bg-danger-1' : 'text-t-primary hover:bg-fill-2'
    }`}
  >
    <span className='size-18px flex shrink-0 items-center justify-center text-t-secondary' aria-hidden='true'>
      {icon}
    </span>
    <span className='min-w-0 truncate'>{children}</span>
  </button>
);

const MoreIcon = () => (
  <svg width='17' height='17' viewBox='0 0 24 24' fill='currentColor' aria-hidden='true'>
    <circle cx='12' cy='5' r='1.8' />
    <circle cx='12' cy='12' r='1.8' />
    <circle cx='12' cy='19' r='1.8' />
  </svg>
);

export default PreviewArtifactActions;
