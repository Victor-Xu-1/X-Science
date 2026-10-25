import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import PreviewLoadingState from '@/renderer/components/media/PreviewLoadingState';
import { cachePreviewModule } from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewLoaders';

const loadPdfViewer = cachePreviewModule(() => import('@/renderer/pages/artifact/XSciencePdfArtifactViewer'));
type PdfViewer = Awaited<ReturnType<typeof loadPdfViewer>>['default'];
type PreviewModule = { status: 'loading' } | { status: 'error' } | { status: 'ready'; Viewer: PdfViewer };

/** The shared PDF core keeps ownership of document bytes, parsing and cancellation. */
export function LibraryAttachmentPreview({ filename, contentUrl }: { filename: string; contentUrl: string }) {
  const { t } = useTranslation();
  const [generation, setGeneration] = useState(0);
  const [module, setModule] = useState<PreviewModule>({ status: 'loading' });
  useEffect(() => {
    let current = true;
    setModule({ status: 'loading' });
    const deadline = setTimeout(() => {
      current = false;
      setModule({ status: 'error' });
    }, 20_000);
    void loadPdfViewer().then(
      ({ default: Viewer }) => {
        if (current) setModule({ status: 'ready', Viewer });
        clearTimeout(deadline);
      },
      () => {
        if (current) setModule({ status: 'error' });
        clearTimeout(deadline);
      }
    );
    return () => {
      current = false;
      clearTimeout(deadline);
    };
  }, [generation]);

  if (module.status === 'loading') return <PreviewLoadingState label={t('common.loading')} />;
  if (module.status === 'error') {
    return (
      <div role='alert' className='library-workbench__notice'>
        <span>{t('preview.pdf.loadFailed')}</span>
        <button type='button' onClick={() => setGeneration((value) => value + 1)}>
          {t('common.retry')}
        </button>
      </div>
    );
  }
  return <module.Viewer filename={filename} contentUrl={contentUrl} />;
}
