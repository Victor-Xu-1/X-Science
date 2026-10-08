/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React from 'react';
import { Button, Empty, Input, Select, Spin } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import type { SynonBiomedProjectArtifact, SynonBiomedProjectFolder } from '@/renderer/services/synonBiomedGateway';
import {
  ARTIFACT_ROOT_FOLDER,
  useArtifactFileActionEditor,
  type ArtifactFileAction,
  type ArtifactFileActionResult,
} from './useArtifactFileActionEditor';

const titleKey = { copy: 'copyFile', move: 'moveToFolder', export: 'exportToCloud' } as const;
const failureKey = { copy: 'copyFailed', move: 'moveFailed', export: 'exportFailed' } as const;

export function ArtifactFileActionModal({
  action,
  artifact,
  folders,
  onClose,
  onCompleted,
}: {
  action: ArtifactFileAction;
  artifact: SynonBiomedProjectArtifact;
  folders: SynonBiomedProjectFolder[];
  onClose: () => void;
  onCompleted: (result: ArtifactFileActionResult) => void;
}) {
  const { t } = useTranslation();
  const editor = useArtifactFileActionEditor(action, artifact, t('preview.artifact.copySuffix'), onCompleted);
  const cloud = editor.cloud;
  const close = () => {
    editor.close();
    onClose();
  };
  return (
    <Modal
      title={t(`preview.artifact.${titleKey[action]}`)}
      visible
      onCancel={close}
      onOk={() => void editor.submit()}
      confirmLoading={editor.pending}
      okButtonProps={{ disabled: !editor.canSubmit }}
      okText={t(`preview.artifact.${action}`)}
      cancelText={t(editor.pending ? 'common.close' : 'common.cancel')}
      unmountOnExit
    >
      <div className='flex flex-col gap-14px' aria-busy={editor.pending}>
        {editor.failed && (
          <p role='alert' className='m-0 text-12px text-[rgb(var(--danger-6))]'>
            {t(`preview.artifact.${failureKey[action]}`)}
          </p>
        )}
        {action === 'copy' && (
          <label className='flex flex-col gap-6px text-12px text-t-secondary'>
            {t('preview.artifact.filename')}
            <Input
              aria-label={t('preview.artifact.copiedFilename')}
              value={editor.filename}
              onChange={editor.setFilename}
              disabled={editor.pending}
            />
          </label>
        )}
        {action !== 'export' ? (
          <FolderSelect
            value={editor.folderId}
            folders={folders}
            onChange={editor.setFolderId}
            disabled={editor.pending}
          />
        ) : (
          <>
            {cloud.status === 'loading' || cloud.status === 'idle' ? (
              <div role='status' className='flex items-center gap-8px text-12px text-t-secondary'>
                <Spin size={14} />
                {t('common.loading')}
              </div>
            ) : cloud.status === 'failed' ? (
              <ReadFailure
                message={t('preview.artifact.credentialsLoadFailed')}
                onRetry={() => void cloud.retryCatalogue()}
                disabled={editor.pending}
              />
            ) : cloud.credentials.length === 0 ? (
              <Empty description={t('preview.artifact.noCloudCredentials')} />
            ) : (
              <>
                <label className='flex flex-col gap-6px text-12px text-t-secondary'>
                  {t('preview.artifact.cloudCredential')}
                  <Select
                    aria-label={t('preview.artifact.cloudCredential')}
                    value={cloud.credentialId}
                    onChange={(id) => void cloud.selectCredential(id)}
                    disabled={editor.pending}
                  >
                    {cloud.credentials.map((credential) => (
                      <Select.Option key={credential.id} value={credential.id}>
                        {credential.name}
                      </Select.Option>
                    ))}
                  </Select>
                </label>
                <label className='flex flex-col gap-6px text-12px text-t-secondary'>
                  {t('preview.artifact.bucket')}
                  <Select
                    aria-label={t('preview.artifact.exportBucket')}
                    value={cloud.bucket}
                    onChange={cloud.setBucket}
                    loading={cloud.bucketLoading}
                    disabled={editor.pending || cloud.bucketLoading || cloud.bucketFailed}
                  >
                    {cloud.buckets.map((bucket) => (
                      <Select.Option key={bucket} value={bucket}>
                        {bucket}
                      </Select.Option>
                    ))}
                  </Select>
                </label>
                {cloud.bucketFailed && (
                  <ReadFailure
                    message={t('preview.artifact.bucketLoadFailed')}
                    onRetry={() => void cloud.retryBuckets()}
                    disabled={editor.pending}
                  />
                )}
              </>
            )}
            <label className='flex flex-col gap-6px text-12px text-t-secondary'>
              {t('preview.artifact.objectPath')}
              <Input
                aria-label={t('preview.artifact.cloudObjectPath')}
                value={editor.objectKey}
                onChange={editor.setObjectKey}
                placeholder='folder/file.ext'
                disabled={editor.pending}
              />
            </label>
          </>
        )}
      </div>
    </Modal>
  );
}

function ReadFailure({ message, onRetry, disabled }: { message: string; onRetry: () => void; disabled: boolean }) {
  const { t } = useTranslation();
  return (
    <div className='flex items-center justify-between gap-12px'>
      <p role='alert' className='m-0 text-12px text-[rgb(var(--danger-6))]'>
        {message}
      </p>
      <Button size='small' onClick={onRetry} disabled={disabled}>
        {t('common.retry')}
      </Button>
    </div>
  );
}

function FolderSelect({
  value,
  folders,
  onChange,
  disabled,
}: {
  value: string;
  folders: SynonBiomedProjectFolder[];
  onChange: (id: string) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const label = t('preview.artifact.targetFolder');
  return (
    <label className='flex flex-col gap-6px text-12px text-t-secondary'>
      {label}
      <Select aria-label={label} value={value} onChange={onChange} disabled={disabled}>
        <Select.Option key={ARTIFACT_ROOT_FOLDER} value={ARTIFACT_ROOT_FOLDER}>
          {t('preview.artifact.projectRoot')}
        </Select.Option>
        {folders.map((folder) => (
          <Select.Option key={folder.folderId} value={folder.folderId}>
            {folder.name}
          </Select.Option>
        ))}
      </Select>
    </label>
  );
}
