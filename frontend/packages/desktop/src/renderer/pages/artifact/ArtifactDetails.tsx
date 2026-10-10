/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import React from 'react';
import { useTranslation } from 'react-i18next';
import { PRODUCT_NAME } from '@/common/config/productIdentity';
import type { SynonBiomedProjectArtifact, SynonBiomedProjectFolder } from '@/renderer/services/synonBiomedGateway';
import type { ArtifactReadState } from './useArtifactMetadataReads';
import { ArtifactMetadataState } from './ArtifactMetadataState';
import { useArtifactContextReads } from './useArtifactContextReads';
import { formatBytes, formatDate } from './artifactPresentation';

const ROWS = 'm-0 grid grid-cols-[92px_minmax(0,1fr)] gap-x-10px gap-y-12px text-12px';
const LINK = 'text-[rgb(var(--primary-6))] font-medium break-words whitespace-pre-wrap hover:underline';

export const ArtifactDetails: React.FC<{
  artifact: SynonBiomedProjectArtifact;
  folders: ArtifactReadState<SynonBiomedProjectFolder[]>;
  owner: string;
  onRetryFolders: () => void;
}> = ({ artifact, folders, owner, onRetryFolders }) => {
  const { t, i18n } = useTranslation();
  const taskId = artifact.rootFrameId || artifact.frameId;
  const context = useArtifactContextReads(artifact.projectId, taskId);
  const folder = folders.value.find((item) => item.folderId === artifact.folderId);
  const fallback = t('preview.artifact.notProvided');
  return (
    <div className='px-16px pb-18px'>
      <dl className={ROWS}>
        <Detail label={t('preview.artifact.details.size')} value={formatBytes(artifact.sizeBytes)} />
        <Detail
          label={t('preview.artifact.details.displayedVersion')}
          value={t('preview.artifact.versionLabel', { version: artifact.versionNumber || 1 })}
        />
        <dt className='text-t-tertiary'>{t('preview.artifact.details.folder')}</dt>
        <dd className='m-0 min-w-0 break-words text-t-primary'>
          <ArtifactMetadataState
            state={folders}
            label={t('preview.artifact.details.folder')}
            owner={owner}
            compact
            onRetry={onRetryFolders}
          >
            {!artifact.folderId
              ? t('preview.artifact.projectRoot')
              : (folder?.name ?? t('preview.artifact.metadata.folderMissing'))}
          </ArtifactMetadataState>
        </dd>
        <Detail label={t('preview.artifact.details.agent')} value={artifact.agentName ?? PRODUCT_NAME} />
        <dt className='text-t-tertiary'>{t('preview.artifact.details.project')}</dt>
        <dd className='m-0 min-w-0'>
          {artifact.projectId ? (
            <ArtifactMetadataState
              state={context.project}
              label={t('preview.artifact.details.project')}
              owner={context.owner}
              compact
              showKnownContent
              hasCachedValue={Boolean(context.project.value)}
              onRetry={() => context.retry('project')}
            >
              <a href={`#/projects/${encodeURIComponent(artifact.projectId)}`} className={LINK}>
                {context.project.value?.name ?? t('preview.artifact.details.openProject')}
              </a>
            </ArtifactMetadataState>
          ) : (
            t('preview.artifact.notLinked')
          )}
        </dd>
        <dt className='text-t-tertiary'>{t('preview.artifact.details.task')}</dt>
        <dd className='m-0 min-w-0'>
          {taskId ? (
            <ArtifactMetadataState
              state={context.task}
              label={t('preview.artifact.details.task')}
              owner={context.owner}
              compact
              showKnownContent
              hasCachedValue={Boolean(context.task.value)}
              onRetry={() => context.retry('task')}
            >
              <a href={`#/conversation/${encodeURIComponent(taskId)}`} className={LINK}>
                {context.task.value?.name ?? t('preview.artifact.details.openTask')}
              </a>
            </ArtifactMetadataState>
          ) : (
            t('preview.artifact.notLinked')
          )}
        </dd>
        <Detail
          label={t('preview.artifact.details.createdAt')}
          value={formatDate(artifact.createdAt, i18n.language, t('preview.artifact.unknown'))}
        />
      </dl>
      <details
        key={artifact.artifactId}
        data-testid='artifact-technical-details'
        className='mt-16px border-t border-solid border-[var(--color-border-2)]'
      >
        <summary className='cursor-pointer py-12px text-12px font-medium text-t-secondary'>
          {t('preview.artifact.details.technical')}
        </summary>
        <dl className={`${ROWS} pb-8px`}>
          <Detail label={t('preview.artifact.details.filename')} value={artifact.filename} />
          <Detail
            label={t('preview.artifact.details.contentType')}
            value={artifact.contentType ?? t('preview.artifact.unknown')}
            mono
          />
          <Detail label={t('preview.artifact.details.artifactId')} value={artifact.artifactId} mono />
          <Detail label={t('preview.artifact.details.versionId')} value={artifact.versionId ?? fallback} mono />
          <Detail
            label={t('preview.artifact.details.project')}
            value={artifact.projectId ?? t('preview.artifact.notLinked')}
            mono
          />
          <Detail
            label={t('preview.artifact.details.task')}
            value={artifact.frameId ?? artifact.rootFrameId ?? t('preview.artifact.notLinked')}
            mono
          />
          {artifact.rootFrameId && artifact.rootFrameId !== artifact.frameId && (
            <Detail label={t('preview.artifact.details.rootTaskId')} value={artifact.rootFrameId} mono />
          )}
          <Detail label={t('preview.artifact.details.checksum')} value={artifact.checksum ?? fallback} mono />
        </dl>
      </details>
    </div>
  );
};

const Detail: React.FC<{ label: string; value: string; mono?: boolean }> = ({ label, value, mono }) => (
  <>
    <dt className='text-t-tertiary'>{label}</dt>
    <dd className={`m-0 min-w-0 text-t-primary ${mono ? 'break-all font-mono text-11px' : 'break-words'}`}>{value}</dd>
  </>
);
