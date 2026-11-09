import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureStudyBatch,
  type LiteratureStudyBatchCommand,
  type LiteratureStudyBatchRow,
} from './literatureStudyBatchContract';
import { decodeLiteratureStudy } from '@/common/adapter/literatureStudySnapshot';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { loadLiteratureItem } from '@/renderer/services/runtime/literatureLibrary';

export async function requestStudyBatch(signal: AbortSignal, command?: LiteratureStudyBatchCommand) {
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<unknown>(command ? 'POST' : 'GET', '/api/literature/study-batch', command, {
    signal,
    timeoutMs: command?.action === 'advance' ? 190_000 : 15_000,
  });
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Library account changed', 'AbortError');
  const view = decodeLiteratureStudyBatch(raw);
  if (command && view.batch?.id !== (command.action === 'start' ? command.operation_id : command.batch_id))
    throw new Error('Library batch response belongs to a different operation');
  return view;
}

/** Result expansion uses the original catalogue and exact saved result reference. */
export async function readStudyBatchResult(
  batchId: string,
  index: number,
  row: LiteratureStudyBatchRow,
  signal: AbortSignal
) {
  const account = getRendererAccountScopeToken();
  const item = await loadLiteratureItem(row.item_id, signal);
  const raw = await httpRequest<unknown>(
    'GET',
    `/api/literature/study-batch/${encodeURIComponent(batchId)}/rows/${index}`,
    undefined,
    { signal, timeoutMs: 15_000 }
  );
  if (signal.aborted || account !== getRendererAccountScopeToken())
    throw new DOMException('Library account changed', 'AbortError');
  const view = decodeLiteratureStudy(raw, item);
  if (!view.snapshot || view.source_key !== row.request.expected_source_key || view.revision !== row.result_revision)
    throw new Error('Saved overview no longer matches this batch result');
  return { item, view };
}
