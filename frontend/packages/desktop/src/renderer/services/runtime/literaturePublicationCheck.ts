import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteraturePublicationCheck,
  decodeLiteraturePublicationCommand,
  decodeLiteraturePublicationReceipt,
  type LiteraturePublicationCommand,
} from '@/common/adapter/literaturePublicationCheck';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

const current = (account: string) => {
  if (account !== getRendererAccountScopeToken()) throw new Error('Literature publication account changed');
};
export async function readLiteraturePublicationCheck(itemId: string, revision: number, signal?: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const query = new URLSearchParams({ expected_metadata_revision: String(revision) });
  const raw = await httpRequest<unknown>(
    'GET',
    `/api/literature/items/${encodeURIComponent(itemId)}/publication-check?${query}`,
    undefined,
    { signal, timeoutMs: 15_000 }
  );
  current(account);
  const result = decodeLiteraturePublicationCheck(raw);
  if (result.item_id !== itemId || result.metadata_revision !== revision)
    throw new Error('Literature publication identity changed');
  return result;
}
/** Called only by the existing durable mutation owner, including original-request retries. */
export async function runLiteraturePublicationCommand(input: LiteraturePublicationCommand, signal?: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const command = decodeLiteraturePublicationCommand(input);
  const raw = await httpRequest<unknown>(
    'POST',
    `/api/literature/items/${encodeURIComponent(command.item_id)}/publication-check`,
    { operation_id: command.operation_id, expected_metadata_revision: command.expected_metadata_revision },
    { signal, timeoutMs: 30_000 }
  );
  current(account);
  const receipt = decodeLiteraturePublicationReceipt(raw, command.operation_id);
  if (
    receipt.result.item_id !== command.item_id ||
    receipt.result.metadata_revision !== command.expected_metadata_revision
  )
    throw new Error('Literature publication acknowledgement changed');
  return receipt;
}
