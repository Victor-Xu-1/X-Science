import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureMetadataDraft,
  type LiteratureMetadataLookupScheme,
} from '@/common/adapter/literatureMetadataLookup';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

export async function lookupLiteratureMetadata(
  reference: string,
  scheme: LiteratureMetadataLookupScheme,
  signal: AbortSignal
) {
  const account = getRendererAccountScopeToken();
  const input = reference.trim();
  const raw = await httpRequest<unknown>(
    'POST',
    '/api/literature/metadata-lookup',
    scheme === 'pmid' ? { reference: input, scheme } : { reference: input },
    {
      signal,
      timeoutMs: 20_000,
    }
  );
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Lookup scope changed', 'AbortError');
  return decodeLiteratureMetadataDraft(raw, input, scheme);
}
