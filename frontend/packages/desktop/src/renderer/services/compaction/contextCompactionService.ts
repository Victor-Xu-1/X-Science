import { httpRequest } from '@/common/adapter/httpBridge';
import { decodeCompactionReceipt, decodeCompactionSnapshot } from './contextCompactionContract';
import type { CompactionIdentity, CompactionRequest } from './contextCompactionTypes';

const base = (identity: CompactionIdentity) =>
  `/api/conversations/${encodeURIComponent(identity.frameId)}/context-compactions`;
const byRequest = (identity: CompactionIdentity, request: CompactionRequest) =>
  `${base(identity)}/by-request/${encodeURIComponent(request.request_id)}`;
const options = (signal?: AbortSignal) => ({ signal, timeoutMs: 15000, silentStatuses: [404, 409] });
export async function readContextCompaction(identity: CompactionIdentity, signal?: AbortSignal) {
  return decodeCompactionSnapshot(await httpRequest('GET', base(identity), undefined, options(signal)), identity);
}
export async function lookupContextCompaction(
  identity: CompactionIdentity,
  request: CompactionRequest,
  signal?: AbortSignal
) {
  const scope = request.expected;
  const query = new URLSearchParams({
    expected_frame_incarnation_id: scope.frame_incarnation_id,
    expected_root_frame_id: scope.root_frame_id,
    expected_root_frame_incarnation_id: scope.root_frame_incarnation_id,
    expected_stream_uid: scope.stream_uid,
    expected_stream_epoch: String(scope.stream_epoch),
  });
  return decodeCompactionReceipt(
    await httpRequest('GET', `${byRequest(identity, request)}?${query}`, undefined, options(signal)),
    identity,
    request
  );
}
export async function submitContextCompaction(
  identity: CompactionIdentity,
  request: CompactionRequest,
  signal?: AbortSignal
) {
  return decodeCompactionReceipt(
    await httpRequest('POST', base(identity), request, options(signal)),
    identity,
    request
  );
}
export async function cancelContextCompaction(
  identity: CompactionIdentity,
  request: CompactionRequest,
  signal?: AbortSignal
) {
  return decodeCompactionReceipt(
    await httpRequest('DELETE', byRequest(identity, request), request, options(signal)),
    identity,
    request
  );
}
