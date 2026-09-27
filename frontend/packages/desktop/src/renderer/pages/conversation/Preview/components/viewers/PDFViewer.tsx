/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { SynonBiomedPdfArtifactViewer } from '@/renderer/pages/artifact/SynonBiomedPdfArtifactViewer';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { buildPdfSrc } from '../../previewUrls';

interface PDFPreviewProps {
  /** Source identity only; local files must be transported by the authenticated file API. */
  file_path?: string;
  fileName?: string;
  /** Authenticated content URL, data URL, or blob URL. */
  content?: string;
  hideToolbar?: boolean;
}

const PDFPreview: React.FC<PDFPreviewProps> = ({ file_path, fileName, content, hideToolbar = false }) => {
  const { t } = useTranslation();
  const source = buildPdfSrc(file_path, content);
  const filename = fileName || file_path?.replace(/\\/g, '/').split('/').at(-1) || t('preview.pdf.title');

  if (!source) {
    return (
      <div className='size-full flex-center bg-1 px-24px' role='alert'>
        <div className='text-center'>
          <div className='mb-8px text-16px text-t-error'>
            {t(file_path || content ? 'preview.pdf.loadFailed' : 'preview.pdf.pathMissing')}
          </div>
          <div className='text-12px text-t-secondary'>{t('preview.pdf.unableDisplay')}</div>
        </div>
      </div>
    );
  }

  // The same parser, worker and paged canvas serve both the workbench and
  // artifact detail. A native iframe load event cannot establish PDF rendering.
  return <SynonBiomedPdfArtifactViewer filename={filename} contentUrl={source} hideToolbar={hideToolbar} />;
};

export default PDFPreview;
