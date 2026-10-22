import {
  decodePdfReadingSource,
  pdfReadingSourceKey,
  type PdfReadingBinding,
  type PdfReadingContext,
  type PdfReadingPosition,
  type PdfReadingSource,
} from '@/common/adapter/pdfReadingContext';
import { getXScienceArtifactVersionContentUrl } from '../xScienceArtifacts';

export function pdfReadingContentUrl(source: PdfReadingSource, frameId?: string): string {
  return source.source_kind === 'literature-attachment-version'
    ? `/api/literature/attachment-versions/${encodeURIComponent(source.version_id)}/content${frameId ? `?frame_id=${encodeURIComponent(frameId)}` : ''}`
    : getXScienceArtifactVersionContentUrl(source.version_id);
}
export function boundPdfPreview(binding: PdfReadingBinding, context: Pick<PdfReadingContext, 'scope'>, page?: number) {
  const source = decodePdfReadingSource(binding);
  const content = pdfReadingContentUrl(source, context.scope.frame_id);
  return {
    content,
    metadata: {
      ...(source.source_kind === 'literature-attachment-version' ? {} : { artifactId: source.artifact_id }),
      versionId: source.version_id,
      pdfReadingSource: source,
      projectId: context.scope.project_id,
      rootFrameId: context.scope.root_frame_id,
      contentUrl: content,
      file_name: binding.name,
      editable: false,
      targetPdfPage: page,
    },
  };
}
export function previewReadingSource(
  value: unknown,
  artifactId?: string,
  versionId?: string
): PdfReadingSource | undefined {
  if (value === undefined)
    return artifactId && versionId ? { artifact_id: artifactId, version_id: versionId } : undefined;
  try {
    return decodePdfReadingSource(value);
  } catch {
    return undefined;
  }
}
export function activePdfReadingKey(metadata?: { pdfReadingSource?: unknown; versionId?: string }): string | undefined {
  const source = previewReadingSource(metadata?.pdfReadingSource);
  return source ? pdfReadingSourceKey(source) : metadata?.versionId;
}

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
