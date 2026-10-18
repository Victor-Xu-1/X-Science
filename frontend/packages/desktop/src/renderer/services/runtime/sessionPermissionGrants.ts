import { httpRequest } from '@/common/adapter/httpBridge';

export type SessionGrantIdentity = {
  ownerId: string;
  frameId: string;
  frameIncarnationId: string;
  rootFrameId: string;
};
export type SessionGrantScope = {
  frame_id: string;
  frame_incarnation_id: string;
  root_frame_id: string;
  root_frame_incarnation_id: string;
  project_id: string;
};
export type SessionPermissionGrant = {
  grant_id: string;
  kind: 'local_exec';
  key: string;
  category_key: string;
  label: string;
  scope: 'conversation';
  tier: 'allow';
};
export type SessionGrantsSnapshot = {
  schema: 'x-science.session-permission-grants.v1';
  scope: SessionGrantScope;
  grants: SessionPermissionGrant[];
  legacy_unbound_count: number;
  stale_grant_count: number;
  incomplete: boolean;
  incomplete_reason?: 'limit';
};
export type SessionGrantRevokeBody = {
  schema: 'x-science.session-permission-revoke.v1';
  expected_frame_incarnation_id: string;
  expected_root_frame_id: string;
  expected_root_frame_incarnation_id: string;
  grants: Array<Pick<SessionPermissionGrant, 'grant_id' | 'kind' | 'key'>>;
};
export type SessionGrantRevokeResult = {
  grant_id: string;
  status: 'revoked' | 'already_absent' | 'changed';
};
export type SessionGrantRevokeResponse = {
  scope: SessionGrantScope;
  results: SessionGrantRevokeResult[];
  snapshot: SessionGrantsSnapshot;
};

function invalid(): never {
  throw new Error('Invalid session grant response');
}
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid();
  return value as Record<string, unknown>;
}
export function sessionGrantIdentifier(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    value.length <= 4096 &&
    value.trim() === value &&
    ![...value].some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)
  );
}
function count(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) return invalid();
  return value;
}
function decodeScope(value: unknown): SessionGrantScope {
  const scope = record(value);
  const { frame_id, frame_incarnation_id, root_frame_id, root_frame_incarnation_id, project_id } = scope;
  if (
    ![frame_id, frame_incarnation_id, root_frame_id, root_frame_incarnation_id, project_id].every(
      sessionGrantIdentifier
    )
  )
    return invalid();
  return { frame_id, frame_incarnation_id, root_frame_id, root_frame_incarnation_id, project_id } as SessionGrantScope;
}
export function sameSessionGrantScope(left: SessionGrantScope, right: SessionGrantScope): boolean {
  return (
    left.frame_id === right.frame_id &&
    left.frame_incarnation_id === right.frame_incarnation_id &&
    left.root_frame_id === right.root_frame_id &&
    left.root_frame_incarnation_id === right.root_frame_incarnation_id &&
    left.project_id === right.project_id
  );
}
function assertIdentity(scope: SessionGrantScope, identity: SessionGrantIdentity): void {
  if (
    scope.frame_id !== identity.frameId ||
    scope.frame_incarnation_id !== identity.frameIncarnationId ||
    scope.root_frame_id !== identity.rootFrameId
  )
    invalid();
}
export function decodeSessionGrantsSnapshot(value: unknown): SessionGrantsSnapshot {
  const data = record(value);
  if (
    data.schema !== 'x-science.session-permission-grants.v1' ||
    !Array.isArray(data.grants) ||
    data.grants.length > 1000 ||
    typeof data.incomplete !== 'boolean' ||
    (data.incomplete_reason !== undefined && (data.incomplete_reason !== 'limit' || !data.incomplete))
  )
    return invalid();
  const ids = new Set<string>();
  const coordinates = new Set<string>();
  const grants = data.grants.map((item): SessionPermissionGrant => {
    const row = record(item);
    if (
      !sessionGrantIdentifier(row.grant_id) ||
      !sessionGrantIdentifier(row.key) ||
      !sessionGrantIdentifier(row.category_key) ||
      row.kind !== 'local_exec' ||
      row.scope !== 'conversation' ||
      row.tier !== 'allow' ||
      typeof row.label !== 'string' ||
      row.label.length > 4096
    )
      return invalid();
    const coordinate = JSON.stringify([row.kind, row.key]);
    if (ids.has(row.grant_id) || coordinates.has(coordinate)) return invalid();
    ids.add(row.grant_id);
    coordinates.add(coordinate);
    return {
      grant_id: row.grant_id,
      kind: row.kind,
      key: row.key,
      category_key: row.category_key,
      label: row.label,
      scope: row.scope,
      tier: row.tier,
    };
  });
  return {
    schema: 'x-science.session-permission-grants.v1',
    scope: decodeScope(data.scope),
    grants,
    legacy_unbound_count: count(data.legacy_unbound_count),
    stale_grant_count: count(data.stale_grant_count),
    incomplete: data.incomplete,
    ...(data.incomplete_reason === 'limit' ? { incomplete_reason: 'limit' as const } : {}),
  };
}
export function freezeSessionGrantRevoke(
  snapshot: SessionGrantsSnapshot,
  categoryKey?: string
): SessionGrantRevokeBody {
  if (categoryKey === undefined && snapshot.incomplete) throw new Error('Incomplete session grant list');
  const selected = snapshot.grants.filter((row) => categoryKey === undefined || row.category_key === categoryKey);
  if (!selected.length || selected.length > 1000) throw new Error('No observed session grants');
  const body: SessionGrantRevokeBody = {
    schema: 'x-science.session-permission-revoke.v1',
    expected_frame_incarnation_id: snapshot.scope.frame_incarnation_id,
    expected_root_frame_id: snapshot.scope.root_frame_id,
    expected_root_frame_incarnation_id: snapshot.scope.root_frame_incarnation_id,
    grants: selected.map(({ grant_id, kind, key }) => Object.freeze({ grant_id, kind, key })),
  };
  Object.freeze(body.grants);
  return Object.freeze(body);
}
const grantsPath = (frameId: string) => `/api/conversations/${encodeURIComponent(frameId)}/permission-grants`;

export async function loadSessionPermissionGrants(
  identity: SessionGrantIdentity,
  expected?: SessionGrantScope,
  signal?: AbortSignal
): Promise<SessionGrantsSnapshot> {
  const query = expected
    ? '?' +
      new URLSearchParams({
        expected_frame_incarnation_id: expected.frame_incarnation_id,
        expected_root_frame_id: expected.root_frame_id,
        expected_root_frame_incarnation_id: expected.root_frame_incarnation_id,
      })
    : '';
  const snapshot = decodeSessionGrantsSnapshot(
    await httpRequest<unknown>('GET', grantsPath(identity.frameId) + query, undefined, { signal })
  );
  assertIdentity(snapshot.scope, identity);
  if (expected && !sameSessionGrantScope(snapshot.scope, expected)) invalid();
  return snapshot;
}

export async function revokeSessionPermissionGrants(
  identity: SessionGrantIdentity,
  expected: SessionGrantScope,
  body: SessionGrantRevokeBody
): Promise<SessionGrantRevokeResponse> {
  assertIdentity(expected, identity);
  if (
    body.expected_frame_incarnation_id !== expected.frame_incarnation_id ||
    body.expected_root_frame_id !== expected.root_frame_id ||
    body.expected_root_frame_incarnation_id !== expected.root_frame_incarnation_id
  )
    return invalid();
  const data = record(await httpRequest<unknown>('POST', grantsPath(identity.frameId) + '/revoke', body));
  if (
    data.schema !== 'x-science.session-permission-revoke.v1' ||
    data.broader_policies_unchanged !== true ||
    !Array.isArray(data.results) ||
    data.results.length !== body.grants.length
  )
    return invalid();
  const scope = decodeScope(data.scope);
  const snapshot = decodeSessionGrantsSnapshot(data.snapshot);
  if (!sameSessionGrantScope(scope, expected) || !sameSessionGrantScope(snapshot.scope, expected)) return invalid();
  const remaining = new Set(body.grants.map((grant) => grant.grant_id));
  const results = data.results.map((value): SessionGrantRevokeResult => {
    const row = record(value);
    if (
      !sessionGrantIdentifier(row.grant_id) ||
      !remaining.delete(row.grant_id) ||
      (row.status !== 'revoked' && row.status !== 'already_absent' && row.status !== 'changed')
    )
      return invalid();
    if (snapshot.grants.some((grant) => grant.grant_id === row.grant_id)) return invalid();
    return { grant_id: row.grant_id, status: row.status as SessionGrantRevokeResult['status'] };
  });
  return { scope, snapshot, results };
}
