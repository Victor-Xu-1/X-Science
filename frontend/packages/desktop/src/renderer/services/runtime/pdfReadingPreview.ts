import type { PdfReadingContext, PdfReadingPosition, PdfReadingSource } from '@/common/adapter/pdfReadingContext';
import { getXScienceArtifactVersionContentUrl } from '../xScienceArtifacts';

export function composerPdfPreview(
  item: { artifactId: string; versionId: string; label: string; projectId?: string; contentType?: string },
  projectId?: string
) {
  if (!item.label.toLowerCase().endsWith('.pdf') && item.contentType?.split(';')[0].toLowerCase() !== 'application/pdf')
    return undefined;
  const content = getXScienceArtifactVersionContentUrl(item.versionId);
  return {
    content,
    metadata: {
      artifactId: item.artifactId,
      versionId: item.versionId,
      projectId: item.projectId ?? projectId,
      contentUrl: content,
      file_name: item.label,
      editable: false,
    },
  };
}

/** Temporary UI port borrowed from the live composer. It owns no server state. */
export type PdfReadingPreviewOwner = {
  ownerId: string;
  frameId: string;
  projectId?: string;
  context: PdfReadingContext | null;
  pending: boolean;
  draftSources?: PdfReadingSource[];
  link: (source: PdfReadingSource, detail?: { name: string; position?: PdfReadingPosition }) => Promise<void>;
  unlink: (bindingId: string) => Promise<void>;
  unlinkDraft?: (source: PdfReadingSource) => Promise<void>;
  reportPosition: (source: PdfReadingSource, position: PdfReadingPosition) => void;
};
