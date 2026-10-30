import { httpRequest } from '@/common/adapter/httpBridge';
import { decodeLiteratureBibtex } from '@/common/adapter/literatureBibtex';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

/** The original authenticated catalogue supplies text; no mutation receipt or provider is involved. */
export async function loadLiteratureBibtex(itemIds: readonly string[], signal: AbortSignal): Promise<string> {
  if (
    !itemIds.length ||
    new Set(itemIds).size !== itemIds.length ||
    itemIds.some((id) => !id || id.trim() !== id || id.length > 512)
  )
    throw new Error('Invalid Library export selection');
  const account = getRendererAccountScopeToken();
  const current = () => {
    signal.throwIfAborted();
    if (account !== getRendererAccountScopeToken()) throw new DOMException('Library account changed', 'AbortError');
  };
  // Respect the existing 64 KiB body boundary without capping the complete explicit selection.
  const batches: string[][] = [];
  let batch: string[] = [];
  for (const id of itemIds) {
    const next = [...batch, id];
    if (
      batch.length &&
      (next.length > 200 || new TextEncoder().encode(JSON.stringify({ item_ids: next })).length > 64 * 1024)
    ) {
      batches.push(batch);
      batch = [];
    }
    batch.push(id);
  }
  if (batch.length) batches.push(batch);
  const parts: string[] = [];
  for (const ids of batches) {
    current();
    const raw = await httpRequest<unknown>(
      'POST',
      '/api/literature/export-bibtex',
      { item_ids: ids },
      { signal, timeoutMs: 30_000 }
    );
    current();
    parts.push(decodeLiteratureBibtex(raw, ids).bibtex.trim());
  }
  current();
  return parts.join('\n\n') + '\n';
}
