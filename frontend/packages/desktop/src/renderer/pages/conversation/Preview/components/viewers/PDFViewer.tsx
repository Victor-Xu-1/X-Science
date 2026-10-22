/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { XSciencePdfArtifactViewer } from '@/renderer/pages/artifact/XSciencePdfArtifactViewer';
import React, { useCallback, useMemo, useState } from 'react';
import { pdfReadingSourceKey, type PdfReadingPosition } from '@/common/adapter/pdfReadingContext';
import { previewReadingSource } from '@/renderer/services/runtime/pdfReadingPreview';
import { useOptionalPreviewContext } from '../../context/PreviewContext';
import { PdfReadingPreviewAction } from './PdfReadingPreviewAction';
import { useTranslation } from 'react-i18next';
import { buildPdfSrc } from '../../previewUrls';

interface PDFPreviewProps {
  /** Source identity only; local files must be transported by the authenticated file API. */
  file_path?: string;
  fileName?: string;
  /** Authenticated content URL, data URL, or blob URL. */
  content?: string;
  hideToolbar?: boolean;
  artifactId?: string;
  versionId?: string;
  projectId?: string;
  readingSource?: unknown;
  targetPage?: number;
  revealId?: string;
}

const PDFPreview: React.FC<PDFPreviewProps> = ({
  file_path,
  fileName,
  content,
  hideToolbar = false,
  artifactId,
  versionId,
  projectId,
  readingSource,
  targetPage,
  revealId,
}) => {
  const { t } = useTranslation();
  const owner = useOptionalPreviewContext()?.pdfReadingOwner;
  const identity = useMemo(
    () => previewReadingSource(readingSource, artifactId, versionId),
    [readingSource, artifactId, versionId]
  );
  const sourceKey = identity ? pdfReadingSourceKey(identity) : undefined;
  const [observed, setObserved] = useState<{ key?: string; position: PdfReadingPosition }>();
  const report = useCallback(
    (position: PdfReadingPosition) => {
      setObserved({ key: sourceKey, position });
      if (identity && projectId === owner?.projectId) owner?.reportPosition(identity, position);
    },
    [identity, owner, projectId, sourceKey]
  );
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
  return (
    <div className='size-full min-h-0 flex flex-col'>
      <PdfReadingPreviewAction
        key={sourceKey ?? source}
        source={identity}
        projectId={projectId}
        name={filename}
        position={observed?.key === sourceKey ? observed?.position : undefined}
      />
      <div className='min-h-0 flex-1'>
        <XSciencePdfArtifactViewer
          filename={filename}
          contentUrl={source}
          hideToolbar={hideToolbar}
          onReadingPositionChange={report}
          targetPage={targetPage}
          revealId={revealId}
        />
      </div>
    </div>
  );
};

export default PDFPreview;
