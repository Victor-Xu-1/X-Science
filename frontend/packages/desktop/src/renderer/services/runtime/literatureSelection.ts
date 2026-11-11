import { httpRequest } from '@/common/adapter/httpBridge';
import {
  appendLiteratureScopeQuery,
  decodeLiteratureSelection,
  decodeLiteratureRestorePreview,
  type LiteratureRestorePreview,
  type LiteratureSelection,
  type LiteratureScopeInput,
} from '@/common/adapter/literatureSelection';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

/** Resolve a complete membership projection before any batch writes; do not walk a live page cursor. */
export async function loadCompleteLiteratureSelection(
  scope: LiteratureScopeInput,
  signal: AbortSignal,
  options: { order?: 'title-asc' } = {}
): Promise<LiteratureSelection> {
  if (signal.aborted) throw new DOMException('Library selection visit ended', 'AbortError');
  const account = getRendererAccountScopeToken();
  const query = new URLSearchParams({ all_item_ids: 'true' });
  appendLiteratureScopeQuery(query, scope);
  if (options.order !== undefined) query.set('order', options.order);
  const response = await httpRequest<unknown>('GET', `/api/literature/items?${query}`, undefined, {
    signal,
    timeoutMs: 15_000,
  });
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Library selection visit ended', 'AbortError');
  return decodeLiteratureSelection(response);
}

export async function loadLiteratureRestorePreview(
  scope: LiteratureScopeInput,
  signal: AbortSignal
): Promise<LiteratureRestorePreview> {
  if (scope.lifecycle !== 'deleted') throw new Error('Library restore preview requires Trash');
  if (signal.aborted) throw new DOMException('Library restore visit ended', 'AbortError');
  const account = getRendererAccountScopeToken();
  const query = new URLSearchParams({ all_item_ids: 'true', restore_preview: 'true' });
  appendLiteratureScopeQuery(query, scope);
  const response = await httpRequest<unknown>('GET', `/api/literature/items?${query}`, undefined, {
    signal,
    timeoutMs: 15_000,
  });
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Library restore visit ended', 'AbortError');
  return decodeLiteratureRestorePreview(response);
}
