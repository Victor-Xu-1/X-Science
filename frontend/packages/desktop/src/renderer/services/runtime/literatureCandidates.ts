import { httpRequest } from '@/common/adapter/httpBridge';
import {
  decodeLiteratureCandidates,
  decodeLiteratureCandidateCommand,
  decodeLiteratureCandidateReceipt,
  type LiteratureCandidateCommand,
} from '@/common/adapter/literatureCandidates';
import { getRendererAccountScopeToken } from '../rendererAccountScope';

export async function loadLiteratureCandidates(state: 'pending' | 'dismissed', signal: AbortSignal, cursor?: string) {
  const account = getRendererAccountScopeToken();
  const raw = await httpRequest<unknown>(
    'GET',
    `/api/literature/candidates?state=${state}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`,
    undefined,
    { signal, timeoutMs: 15000 }
  );
  if (signal.aborted || account !== getRendererAccountScopeToken()) throw new Error('Library account changed');
  const page = decodeLiteratureCandidates(raw, state);
  if (
    cursor &&
    (page.candidates.some((candidate) => candidate.id <= cursor) || (page.nextCursor && page.nextCursor <= cursor))
  )
    throw new Error('Invalid Library candidate cursor');
  if (page.nextCursor && page.candidates.at(-1)?.id !== page.nextCursor)
    throw new Error('Invalid Library candidate cursor');
  return page;
}
export async function runLiteratureCandidateCommand(input: LiteratureCandidateCommand, signal?: AbortSignal) {
  const account = getRendererAccountScopeToken();
  const command = decodeLiteratureCandidateCommand(input);
  const action = command.kind.slice('candidate-'.length);
  const path =
    command.kind === 'candidate-stage'
      ? '/api/literature/candidates'
      : `/api/literature/candidates/${encodeURIComponent(command.candidate_id)}/${action}`;
  const body =
    command.kind === 'candidate-stage'
      ? {
          operation_id: command.operation_id,
          provider: command.provider,
          external_id: command.external_id,
          title: command.title,
          metadata: command.metadata,
        }
      : { operation_id: command.operation_id };
  const raw = await httpRequest<unknown>('POST', path, body, { signal, timeoutMs: 30000 });
  if (signal?.aborted || account !== getRendererAccountScopeToken()) throw new Error('Library account changed');
  return decodeLiteratureCandidateReceipt(raw, command);
}
