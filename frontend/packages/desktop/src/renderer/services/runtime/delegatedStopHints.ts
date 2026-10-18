import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '../rendererAccountScope';
import { decodeDelegatedStopRequest } from './delegatedStopContract';
import type { DelegatedStopIdentity, DelegatedStopRequest } from './delegatedStopTypes';

export const DELEGATED_STOP_HINT_LIMIT = 16;
function key(identity: DelegatedStopIdentity): string {
  return (
    'xscience.delegated-stop-hints.v1:' +
    JSON.stringify([identity.ownerId, identity.parentId, identity.frameIncarnationId])
  );
}
function current(identity: DelegatedStopIdentity, token: string): boolean {
  return getRendererAccountOwnerId() === identity.ownerId && getRendererAccountScopeToken() === token;
}
export function readDelegatedStopHints(identity: DelegatedStopIdentity, token: string) {
  if (!current(identity, token)) return { requests: [], limited: false };
  try {
    const raw = sessionStorage.getItem(key(identity));
    if (!raw) return { requests: [], limited: false };
    if (raw.length > 128 * 1024) throw new Error('Stop restore hints exceed the bound');
    const value: unknown = JSON.parse(raw);
    if (!Array.isArray(value) || value.length > DELEGATED_STOP_HINT_LIMIT)
      throw new Error('Invalid Stop restore hints');
    const requests = value.map(decodeDelegatedStopRequest);
    if (
      requests.some(
        (request) =>
          request.scope.parent_frame_id !== identity.parentId ||
          request.scope.parent_frame_incarnation_id !== identity.frameIncarnationId
      ) ||
      new Set(requests.map((request) => request.request_id)).size !== requests.length
    )
      throw new Error('Stop hint scope mismatch');
    return { requests, limited: false };
  } catch {
    return { requests: [], limited: true };
  }
}
export function saveDelegatedStopHints(
  identity: DelegatedStopIdentity,
  token: string,
  requests: DelegatedStopRequest[]
): boolean {
  if (!current(identity, token)) return false;
  try {
    if (requests.length > DELEGATED_STOP_HINT_LIMIT) return false;
    const raw = JSON.stringify(requests);
    if (raw.length > 128 * 1024) return false;
    if (requests.length) sessionStorage.setItem(key(identity), raw);
    else sessionStorage.removeItem(key(identity));
    return true;
  } catch {
    return false;
  }
}
