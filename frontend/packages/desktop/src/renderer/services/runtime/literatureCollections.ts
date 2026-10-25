import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureCollectionCommand,
  decodeLiteratureCollectionMembership,
  decodeLiteratureCollectionPage,
  decodeLiteratureCollectionReceipt,
  type LiteratureCollectionCommand,
} from '@/common/adapter/literatureCollections';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

const requireAccount = (account: string) => {
  if (account !== getRendererAccountScopeToken()) throw new Error('Literature Library account changed');
};

export async function loadLiteratureCollections(signal?: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<unknown>('GET', '/api/literature/collections', undefined, {
    signal,
    timeoutMs: 15_000,
  });
  requireAccount(account);
  return decodeLiteratureCollectionPage(raw);
}

export async function loadLiteratureItemCollections(itemId: string, signal?: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<unknown>(
    'GET',
    `/api/literature/items/${encodeURIComponent(itemId)}/collections`,
    undefined,
    { signal, timeoutMs: 15_000 }
  );
  requireAccount(account);
  return decodeLiteratureCollectionMembership(raw, itemId);
}

export async function runLiteratureCollectionCommand(input: LiteratureCollectionCommand) {
  const account = getRendererAccountScopeToken();
  const command = decodeLiteratureCollectionCommand(input);
  const { kind, ...body } = command;
  let path = '/api/literature/collections';
  if ('collection_id' in command) path += `/${encodeURIComponent(command.collection_id)}`;
  if (command.kind === 'collection-membership') path += `/items/${encodeURIComponent(command.item_id)}`;
  if ('collection_id' in body) delete (body as Partial<typeof body>).collection_id;
  if ('item_id' in body) delete (body as Partial<typeof body>).item_id;
  const method =
    kind === 'collection-create'
      ? 'POST'
      : kind === 'collection-update'
        ? 'PATCH'
        : kind === 'collection-delete'
          ? 'DELETE'
          : 'PUT';
  const raw = await httpRequest<unknown>(method, path, body, { timeoutMs: 30_000 });
  requireAccount(account);
  return decodeLiteratureCollectionReceipt(raw, command);
}
