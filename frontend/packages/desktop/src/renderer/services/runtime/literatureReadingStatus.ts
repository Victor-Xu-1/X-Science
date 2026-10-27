import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureReadingStatusCommand,
  decodeLiteratureReadingStatusReceipt,
  type LiteratureReadingStatusCommand,
} from '@/common/adapter/literatureReadingStatus';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

/** The shared Library mutation owner alone submits writes and original-request retries. */
export async function runLiteratureReadingStatusCommand(input: LiteratureReadingStatusCommand, signal?: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const command = decodeLiteratureReadingStatusCommand(input);
  const response = await httpRequest<unknown>(
    'POST',
    '/api/literature/items/reading-status',
    { operation_id: command.operation_id, item_ids: command.item_ids, reading_status: command.reading_status },
    { signal, timeoutMs: 30_000 }
  );
  if (account !== getRendererAccountScopeToken()) throw new Error('Library reading-status account changed');
  return decodeLiteratureReadingStatusReceipt(response, command);
}
