import { ScientificPreviewError } from '@/renderer/pages/conversation/Preview/components/viewers/scientificPreviewError';

export const MAX_PDF_PREVIEW_BYTES = 64 * 1024 * 1024;

/** Fetch and bound the decoded body, including responses without a reliable size header. */
export async function loadPdfDocumentBytes(source: string, signal: AbortSignal): Promise<Uint8Array> {
  const response = await fetch(source, { credentials: 'same-origin', signal });
  if (!response.ok) throw new ScientificPreviewError('request-failed', { status: response.status });
  if (Number(response.headers.get('content-length')) > MAX_PDF_PREVIEW_BYTES) {
    await response.body?.cancel();
    throw new ScientificPreviewError('too-large', { limit: '64 MB' });
  }
  if (!response.body) throw new ScientificPreviewError('empty-content');

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  let complete = false;
  try {
    while (true) {
      signal.throwIfAborted();
      // Sequential reads enforce the limit before another chunk can be retained.
      // oxlint-disable-next-line eslint/no-await-in-loop
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) {
        complete = true;
        break;
      }
      total += value.byteLength;
      if (total > MAX_PDF_PREVIEW_BYTES) throw new ScientificPreviewError('too-large', { limit: '64 MB' });
      chunks.push(value);
    }
  } finally {
    try {
      if (!complete) await reader.cancel();
    } finally {
      reader.releaseLock();
    }
  }
  if (total === 0) throw new ScientificPreviewError('empty-content');
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}
