import { requestXScienceJson, type XScienceGatewayOptions } from '../xScienceHttp';
import { notebookScopeQuery, notebookTargetQuery } from './manualNotebookContract';
import { decodeManualNotebookPending, decodeManualNotebookReceipt } from './manualNotebookReceipt';
import type { ManualNotebookRequest, ManualNotebookScope, ManualNotebookTarget } from './manualNotebookTypes';

const optionsWithTimeout = (options: XScienceGatewayOptions): XScienceGatewayOptions => ({
  timeoutMs: 15000,
  ...options,
});
const base = (frameId: string) => `/api/frames/${encodeURIComponent(frameId)}/kernel-executions`;
export async function submitManualNotebook(
  target: ManualNotebookTarget,
  request: ManualNotebookRequest,
  options: XScienceGatewayOptions = {}
) {
  const result = await requestXScienceJson<unknown>(
    base(target.frameId),
    { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(request) },
    optionsWithTimeout(options)
  );
  return decodeManualNotebookReceipt(result, target, request.request_id, request);
}
export async function lookupManualNotebook(
  target: ManualNotebookTarget,
  requestId: string,
  expected?: ManualNotebookRequest,
  options: XScienceGatewayOptions = {}
) {
  const result = await requestXScienceJson<unknown>(
    `${base(target.frameId)}/by-request/${encodeURIComponent(requestId)}?${notebookTargetQuery(target)}`,
    { cache: 'no-store' },
    optionsWithTimeout(options)
  );
  return decodeManualNotebookReceipt(result, target, requestId, expected);
}
export async function cancelManualNotebook(
  target: ManualNotebookTarget,
  request: ManualNotebookRequest,
  options: XScienceGatewayOptions = {}
) {
  const result = await requestXScienceJson<unknown>(
    `${base(target.frameId)}/by-request/${encodeURIComponent(request.request_id)}/cancel`,
    { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(request) },
    optionsWithTimeout(options)
  );
  return decodeManualNotebookReceipt(result, target, request.request_id, request);
}
export async function discoverManualNotebook(
  scope: ManualNotebookScope,
  cursor: string | null,
  options: XScienceGatewayOptions = {}
) {
  const query = notebookScopeQuery(scope);
  if (cursor) query.set('cursor', cursor);
  const result = await requestXScienceJson<unknown>(
    `${base(scope.frameId)}?${query}`,
    { cache: 'no-store' },
    optionsWithTimeout(options)
  );
  return decodeManualNotebookPending(result, scope);
}
