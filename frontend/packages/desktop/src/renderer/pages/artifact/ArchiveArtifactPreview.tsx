/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import PreviewLoadingState from '@/renderer/components/media/PreviewLoadingState';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import {
  cachePreviewModule,
  LazyArchivePreview,
} from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewLoaders';
import './ArchiveArtifactPreview.css';

const PreviewBoard = React.lazy(
  cachePreviewModule(() => import('@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewBoard'))
);

type ArchiveArtifactPreviewProps = { filename: string; contentUrl: string };

const ArchiveReadingSurface: React.FC<ArchiveArtifactPreviewProps> = ({ filename, contentUrl }) => {
  const { t } = useTranslation();
  const { isOpen, tabs } = usePreviewContext();
  const showingEntries = isOpen && tabs.length > 0;
  return (
    <div className='artifact-archive' data-testid='artifact-archive-preview'>
      <div className='artifact-archive__layout' data-showing-entries={showingEntries || undefined}>
        <div className='artifact-archive__directory'>
          <Suspense fallback={<PreviewLoadingState label={t('preview.archive.loading')} />}>
            <LazyArchivePreview filename={filename} contentUrl={contentUrl} />
          </Suspense>
        </div>
        {showingEntries && (
          <div className='artifact-archive__entries'>
            <Suspense fallback={<PreviewLoadingState label={t('common.loading')} />}>
              <PreviewBoard embedded />
            </Suspense>
          </div>
        )}
      </div>
    </div>
  );
};

const ArchiveArtifactPreview: React.FC<ArchiveArtifactPreviewProps> = (props) => (
  <PreviewProvider key={props.contentUrl} scope='embedded'>
    <ArchiveReadingSurface {...props} />
  </PreviewProvider>
);

export default ArchiveArtifactPreview;
