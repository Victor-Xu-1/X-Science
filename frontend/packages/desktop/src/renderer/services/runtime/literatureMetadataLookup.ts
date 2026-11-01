import { httpRequest } from '@/common/adapter/httpBridge';
import { decodeLiteratureMetadataDraft } from '@/common/adapter/literatureMetadataLookup';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

export async function lookupLiteratureDoi(reference: string, signal: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const input = reference.trim();
  const raw = await httpRequest<unknown>(
    'POST',
    '/api/literature/metadata-lookup',
    { reference: input },
    {
      signal,
      timeoutMs: 20_000,
    }
  );
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Lookup scope changed', 'AbortError');
  return decodeLiteratureMetadataDraft(raw, input);
}
