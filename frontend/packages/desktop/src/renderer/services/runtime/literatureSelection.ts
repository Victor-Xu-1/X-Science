import { httpRequest } from '@/common/adapter/httpBridge';
import {
  appendLiteratureScopeQuery,
  decodeLiteratureSelection,
  type LiteratureSelection,
  type LiteratureScopeInput,
} from '@/common/adapter/literatureSelection';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

/** Resolve a complete membership projection before any batch writes; do not walk a live page cursor. */
export async function loadCompleteLiteratureSelection(
  scope: LiteratureScopeInput,
  signal: AbortSignal
): Promise<LiteratureSelection> {
  if (signal.aborted) throw new DOMException('Library selection visit ended', 'AbortError');
  const account = getRendererAccountScopeToken();
  const query = new URLSearchParams({ all_item_ids: 'true' });
  appendLiteratureScopeQuery(query, scope);
  const response = await httpRequest<unknown>('GET', `/api/literature/items?${query}`, undefined, {
    signal,
    timeoutMs: 15_000,
  });
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Library selection visit ended', 'AbortError');
  return decodeLiteratureSelection(response);
}
