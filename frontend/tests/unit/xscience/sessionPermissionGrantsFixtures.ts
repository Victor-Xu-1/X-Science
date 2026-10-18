export const sessionGrantScope = {
  frame_id: 'grant-child',
  frame_incarnation_id: 'grant-child-incarnation',
  root_frame_id: 'grant-root',
  root_frame_incarnation_id: 'grant-root-incarnation',
  project_id: 'grant-project',
};

export const sessionGrantIdentity = {
  ownerId: 'grant-owner',
  frameId: sessionGrantScope.frame_id,
  frameIncarnationId: sessionGrantScope.frame_incarnation_id,
  rootFrameId: sessionGrantScope.root_frame_id,
};
export const sessionGrantInvalidation = {
  conversation_id: sessionGrantScope.root_frame_id,
  action: 'updated' as const,
  source: 'xscience',
  permission_grants_changed: true as const,
  root_frame_id: sessionGrantScope.root_frame_id,
  root_frame_incarnation_id: sessionGrantScope.root_frame_incarnation_id,
};

export const sessionGrantA = {
  grant_id: 'opaque-grant-generation-a',
  kind: 'local_exec' as const,
  key: 'python',
  category_key: 'local_exec:python',
  label: 'Python execution',
  scope: 'conversation' as const,
  tier: 'allow' as const,
};

export const sessionGrantB = {
  ...sessionGrantA,
  grant_id: 'opaque-grant-generation-b',
  key: 'r',
  category_key: 'local_exec:r',
  label: 'R execution',
};

export const sessionGrantC = {
  ...sessionGrantA,
  grant_id: 'opaque-grant-generation-c',
  key: 'repl',
  category_key: 'local_exec:repl',
  label: 'Newly approved execution',
};

export function sessionGrantsSnapshot(grants = [sessionGrantA]) {
  return {
    schema: 'x-science.session-permission-grants.v1' as const,
    scope: { ...sessionGrantScope },
    grants,
    legacy_unbound_count: 0,
    stale_grant_count: 0,
    incomplete: false,
  };
}

export function sessionGrantRevokeBody(grants = [sessionGrantA]) {
  return {
    schema: 'x-science.session-permission-revoke.v1',
    expected_frame_incarnation_id: sessionGrantScope.frame_incarnation_id,
    expected_root_frame_id: sessionGrantScope.root_frame_id,
    expected_root_frame_incarnation_id: sessionGrantScope.root_frame_incarnation_id,
    grants: grants.map(({ grant_id, kind, key }) => ({ grant_id, kind, key })),
  };
}

export function sessionGrantRevokeResponse(grants = [sessionGrantA], remaining = [] as typeof grants) {
  return {
    schema: 'x-science.session-permission-revoke.v1',
    scope: { ...sessionGrantScope },
    results: grants.map(({ grant_id }) => ({ grant_id, status: 'revoked' })),
    snapshot: sessionGrantsSnapshot(remaining),
    broader_policies_unchanged: true,
  };
}
