import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodePdfReadingContext,
  decodePdfReadingScope,
  decodePdfReadingSource,
  samePdfReadingScope,
  type PdfReadingContext,
  type PdfReadingMutation,
  type PdfReadingSource,
  type PdfReadingCandidate,
} from '@/common/adapter/pdfReadingContext';
import { getRendererAccountScopeToken } from '../rendererAccountScope';
import { loadXScienceProjectArtifacts } from '../xScienceGateway';

const endpoint = (frameId: string) => `/api/conversations/${encodeURIComponent(frameId)}/reading-context`;
const currentAccount = (token: string) => {
  if (token !== getRendererAccountScopeToken()) throw new Error('PDF reading account changed');
};
export async function loadPdfReadingContext(frameId: string, signal?: AbortSignal): Promise<PdfReadingContext> {
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<unknown>('GET', endpoint(frameId), undefined, { signal, timeoutMs: 15_000 });
  currentAccount(account);
  const result = decodePdfReadingContext(raw);
  if (result.scope.frame_id !== frameId) throw new Error('PDF reading frame changed');
  return result;
}
export async function mutatePdfReadingContext(
  frameId: string,
  request: PdfReadingMutation
): Promise<PdfReadingContext> {
  const account = getRendererAccountScopeToken();
  const kind = request.schema === 'x-science.pdf-reading-link.v1' ? 'link' : 'unlink';
  const raw = await httpRequest<unknown>('POST', `${endpoint(frameId)}/${kind}`, request, { timeoutMs: 30_000 });
  currentAccount(account);
  const result = decodePdfReadingContext(raw);
  if (
    result.scope.frame_id !== frameId ||
    result.scope.frame_incarnation_id !== request.expected_frame_incarnation_id ||
    result.scope.root_frame_id !== request.expected_root_frame_id ||
    result.scope.root_frame_incarnation_id !== request.expected_root_frame_incarnation_id ||
    result.revision < request.expected_revision ||
    result.revision > request.expected_revision + 1 ||
    (result.changed && result.revision !== request.expected_revision + 1)
  )
    throw new Error('PDF reading acknowledgement changed');
  if (
    kind === 'link'
      ? request.sources?.some(
          (source) =>
            !result.bindings.some(
              (binding) => binding.artifact_id === source.artifact_id && binding.version_id === source.version_id
            )
        )
      : result.bindings.some((binding) => binding.binding_id === request.binding_id)
  )
    throw new Error('PDF reading effect was not acknowledged');
  return result;
}
export async function inspectPdfReadingCandidates(frameId: string, sources: PdfReadingSource[], signal?: AbortSignal) {
  if (sources.length > 100) throw new Error('Too many PDF reading candidates');
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<Record<string, unknown>>(
    'POST',
    `${endpoint(frameId)}/candidates`,
    { schema: 'x-science.pdf-reading-candidates.v1', sources },
    { signal, timeoutMs: 30_000 }
  );
  currentAccount(account);
  if (
    raw.schema !== 'x-science.pdf-reading-candidates.v1' ||
    !Array.isArray(raw.candidates) ||
    raw.candidates.length !== sources.length ||
    !Number.isSafeInteger(raw.revision) ||
    Number(raw.revision) < 0
  )
    throw new Error('Invalid PDF candidates');
  const scope = decodePdfReadingScope(raw.scope);
  if (scope.frame_id !== frameId) throw new Error('PDF candidate frame changed');
  const seen = new Set<string>();
  const candidates = raw.candidates.map((candidate): PdfReadingCandidate => {
    if (!candidate || typeof candidate !== 'object' || typeof candidate.eligible !== 'boolean')
      throw new Error('Invalid PDF candidate');
    const source = decodePdfReadingSource(candidate.source);
    const key = JSON.stringify(source);
    if (seen.has(key)) throw new Error('Duplicate PDF candidate');
    seen.add(key);
    if (!sources.some((item) => item.artifact_id === source.artifact_id && item.version_id === source.version_id))
      throw new Error('Unrequested PDF source');
    if (
      candidate.eligible &&
      (!Number.isSafeInteger(candidate.page_count) ||
        candidate.page_count < 2 ||
        !Number.isSafeInteger(candidate.size_bytes) ||
        candidate.size_bytes < 1 ||
        candidate.size_bytes > 50 * 1024 * 1024 ||
        typeof candidate.checksum !== 'string' ||
        !/^[a-f0-9]{64}$/.test(candidate.checksum))
    )
      throw new Error('Invalid eligible PDF');
    if (!candidate.eligible && typeof candidate.reason !== 'string') throw new Error('Missing PDF diagnostic');
    return {
      source,
      eligible: candidate.eligible,
      reason: candidate.reason,
      page_count: candidate.page_count,
      size_bytes: candidate.size_bytes,
      checksum: candidate.checksum,
    };
  });
  return { scope, candidates };
}
export type PdfReadingOption = { source: PdfReadingSource; name: string; pageCount: number };
export async function loadProjectReadingOptions(context: PdfReadingContext, signal: AbortSignal) {
  const artifacts = await loadXScienceProjectArtifacts(context.scope.project_id, { signal });
  const files = [
    ...new Map(
      artifacts
        .filter(
          (item) =>
            item.versionId &&
            (!item.projectId || item.projectId === context.scope.project_id) &&
            (item.filename.toLowerCase().endsWith('.pdf') ||
              item.contentType?.split(';')[0].toLowerCase() === 'application/pdf')
        )
        .map((item) => [item.versionId, item])
    ).values(),
  ];
  const options: PdfReadingOption[] = [];
  let unavailable = 0;
  for (let offset = 0; offset < files.length; offset += 100) {
    const batch = files.slice(offset, offset + 100);
    const result = await inspectPdfReadingCandidates(
      context.scope.frame_id,
      batch.map((file) => ({ artifact_id: file.artifactId, version_id: file.versionId! })),
      signal
    );
    if (!samePdfReadingScope(result.scope, context.scope)) throw new Error('PDF candidate scope changed');
    for (const item of result.candidates) {
      if (item.eligible)
        options.push({
          source: item.source,
          name: batch.find((file) => file.versionId === item.source.version_id)!.filename,
          pageCount: item.page_count!,
        });
      else if (!['single_page', 'too_large', 'not_pdf'].includes(item.reason ?? '')) unavailable++;
    }
  }
  return { options, unavailable };
}
