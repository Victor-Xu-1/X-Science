import { httpRequest, isBackendHttpError } from '@/common/adapter/httpBridge';
import { decodeDelegatedStopReceipt, decodeDelegatedStopSnapshot } from './delegatedStopContract';
import type { DelegatedStopIssue, DelegatedStopRequest } from './delegatedStopTypes';

const path = (parentId: string): string => '/api/frames/' + encodeURIComponent(parentId) + '/delegated-stops';
export async function readDelegatedStopSnapshot(parentId: string, branchId: string | null) {
  const query = branchId ? '?branch_id=' + encodeURIComponent(branchId) : '';
  return decodeDelegatedStopSnapshot(
    await httpRequest<unknown>('GET', path(parentId) + query, undefined, {
      timeoutMs: 15000,
      silentStatuses: [404, 409, 503],
    })
  );
}
export async function submitDelegatedStop(request: DelegatedStopRequest) {
  return decodeDelegatedStopReceipt(
    await httpRequest<unknown>('POST', path(request.scope.parent_frame_id), request, {
      timeoutMs: 15000,
      silentStatuses: [400, 404, 409, 503],
    }),
    request
  );
}
export async function readDelegatedStopReceipt(request: DelegatedStopRequest) {
  return decodeDelegatedStopReceipt(
    await httpRequest<unknown>(
      'GET',
      path(request.scope.parent_frame_id) + '/' + encodeURIComponent(request.request_id),
      undefined,
      { timeoutMs: 15000, silentStatuses: [404, 409, 503] }
    ),
    request
  );
}
export function delegatedStopIssue(error: unknown): DelegatedStopIssue {
  if (!isBackendHttpError(error)) return 'readFailed';
  if (error.code === 'DELEGATED_STOP_REQUEST_CONFLICT') return 'conflict';
  if (error.status === 409) return 'stale';
  if (error.status === 404) return 'unavailable';
  if (error.status === 400) return 'invalid';
  return 'readFailed';
}
export function delegatedStopDefinitelyRejected(error: unknown): boolean {
  return isBackendHttpError(error) && [400, 409].includes(error.status);
}
