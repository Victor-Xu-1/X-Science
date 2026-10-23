import { httpRequest } from '@/common/adapter/httpBridge';
import { decodeRelatedResult, type RelatedRequest } from '@/common/adapter/literatureRelated';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

export async function loadRelatedLiterature(request: RelatedRequest, signal: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<unknown>('POST', '/api/literature/related', request, { signal, timeoutMs: 35_000 });
  if (signal.aborted || account !== getRendererAccountScopeToken()) throw new Error('Literature query owner changed');
  return decodeRelatedResult(raw, request);
}
