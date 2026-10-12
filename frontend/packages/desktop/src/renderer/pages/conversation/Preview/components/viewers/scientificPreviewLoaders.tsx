import React from 'react';

export function cachePreviewModule<T>(load: () => Promise<T>): () => Promise<T> {
  let pending: Promise<T> | undefined;
  return () => {
    pending ??= load().catch((error) => {
      pending = undefined;
      throw error;
    });
    return pending;
  };
}

export const loadDiffPreview = cachePreviewModule(() => import('./DiffViewer'));
export const loadExcelPreview = cachePreviewModule(() => import('./ExcelViewer'));
export const loadHTMLRenderer = cachePreviewModule(() => import('../renderers/HTMLRenderer'));
export const loadImagePreview = cachePreviewModule(() => import('./ImageViewer'));
export const loadMarkdownPreview = cachePreviewModule(() => import('./MarkdownViewer'));
export const loadPDFPreview = cachePreviewModule(() => import('./PDFViewer'));
export const loadOfficeDocPreview = cachePreviewModule(() => import('./OfficeDocViewer'));
export const loadPptPreview = cachePreviewModule(() => import('./PptViewer'));
export const loadURLPreview = cachePreviewModule(() => import('./URLViewer'));
export const loadXScienceStructureViewer = cachePreviewModule(() => import('./XScienceStructureViewer'));
export const preloadXScienceStructureViewer = (): Promise<void> =>
  loadXScienceStructureViewer().then((): undefined => undefined);
export const loadXScienceTableViewer = cachePreviewModule(() => import('./XScienceTableViewer'));
export const loadXScienceMsaViewer = cachePreviewModule(() => import('./XScienceMsaViewer'));
export const loadXScienceGenomeViewer = cachePreviewModule(() => import('./XScienceGenomeViewer'));
export const loadXScienceSequenceViewer = cachePreviewModule(() => import('./XScienceSequenceViewer'));
export const loadXScienceNotebookViewer = cachePreviewModule(() => import('./XScienceNotebookViewer'));
export const loadXScienceMoleculeViewer = cachePreviewModule(() => import('./XScienceMoleculeViewer'));
export const loadXScienceMcpAppArtifactViewer = cachePreviewModule(
  () => import('./XScienceMcpAppArtifactViewer')
);
export const loadXScienceMcpAppViewer = cachePreviewModule(() => import('./XScienceMcpAppViewer'));
export const loadXScienceJsonViewer = cachePreviewModule(() => import('./XScienceJsonViewer'));
export const loadXScienceHdf5Viewer = cachePreviewModule(() => import('./XScienceHdf5Viewer'));
export const loadMediaPreview = cachePreviewModule(() => import('./MediaPreview'));
export const loadUnsupportedPreview = cachePreviewModule(() => import('./UnsupportedPreview'));
export const loadArchivePreview = cachePreviewModule(() => import('./ArchiveViewer'));

export const LazyDiffPreview = React.lazy(loadDiffPreview);
export const LazyExcelPreview = React.lazy(loadExcelPreview);
export const LazyHTMLRenderer = React.lazy(loadHTMLRenderer);
export const LazyImagePreview = React.lazy(loadImagePreview);
export const LazyMarkdownPreview = React.lazy(loadMarkdownPreview);
export const LazyPDFPreview = React.lazy(loadPDFPreview);
export const LazyOfficeDocPreview = React.lazy(loadOfficeDocPreview);
export const LazyPptPreview = React.lazy(loadPptPreview);
export const LazyURLPreview = React.lazy(loadURLPreview);
export const LazyXScienceStructureViewer = React.lazy(loadXScienceStructureViewer);
export const LazyXScienceTableViewer = React.lazy(loadXScienceTableViewer);
export const LazyXScienceMsaViewer = React.lazy(loadXScienceMsaViewer);
export const LazyXScienceGenomeViewer = React.lazy(loadXScienceGenomeViewer);
export const LazyXScienceSequenceViewer = React.lazy(loadXScienceSequenceViewer);
export const LazyXScienceNotebookViewer = React.lazy(loadXScienceNotebookViewer);
export const LazyXScienceMoleculeViewer = React.lazy(loadXScienceMoleculeViewer);
export const LazyXScienceMcpAppArtifactViewer = React.lazy(loadXScienceMcpAppArtifactViewer);
export const LazyXScienceMcpAppViewer = React.lazy(loadXScienceMcpAppViewer);
export const LazyXScienceJsonViewer = React.lazy(loadXScienceJsonViewer);
export const LazyXScienceHdf5Viewer = React.lazy(loadXScienceHdf5Viewer);
export const LazyMediaPreview = React.lazy(loadMediaPreview);
export const LazyUnsupportedPreview = React.lazy(loadUnsupportedPreview);
export const LazyArchivePreview = React.lazy(loadArchivePreview);
