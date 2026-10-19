import { httpRequest, isBackendHttpError } from '@/common/adapter/httpBridge';
import { uuid } from '@/common/utils';
import { sessionGrantIdentifier } from './sessionPermissionGrants';

export type DelegationPolicy = 'allow' | 'deny';
const POLICY_REQUEST_TIMEOUT_MS = 8_000;
export type SessionDelegationIdentity = {
  ownerId: string;
  frameId: string;
  frameIncarnationId: string;
  rootFrameId: string;
};
export type SessionDelegationScope = {
  root_frame_id: string;
  root_incarnation_id: string;
};
export type SessionDelegationSnapshot = {
  schema: 'x-science.session-delegation-policy.v1';
  scope: SessionDelegationScope;
  policy: DelegationPolicy;
  revision: number;
  has_live_delegated_attempts: boolean;
};
export type SessionDelegationCommand = Readonly<{
  schema: 'x-science.session-delegation-command.v1';
  command_id: string;
  scope: Readonly<SessionDelegationScope>;
  expected_revision: number;
  desired_policy: DelegationPolicy;
}>;
export type SessionDelegationMutation = {
  schema: 'x-science.session-delegation-mutation.v1';
  receipt: {
    schema: 'x-science.session-delegation-receipt.v1';
    command: SessionDelegationCommand;
    applied_revision: number;
    applied_policy: DelegationPolicy;
    applied_at: string;
  };
  snapshot: SessionDelegationSnapshot;
  replayed: boolean;
};
const invalid = (): never => {
  throw new Error('Invalid session delegation response');
};
const object = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : invalid();
const policy = (value: unknown): DelegationPolicy => (value === 'allow' || value === 'deny' ? value : invalid());
const revision = (value: unknown): number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : invalid();
function scope(value: unknown): SessionDelegationScope {
  const row = object(value);
  if (!sessionGrantIdentifier(row.root_frame_id) || !sessionGrantIdentifier(row.root_incarnation_id)) invalid();
  return {
    root_frame_id: row.root_frame_id,
    root_incarnation_id: row.root_incarnation_id,
  } as SessionDelegationScope;
}
export const sameSessionDelegationScope = (left: SessionDelegationScope, right: SessionDelegationScope): boolean =>
  left.root_frame_id === right.root_frame_id && left.root_incarnation_id === right.root_incarnation_id;
export function sessionDelegationIdentity(
  ownerId: string,
  frameId: string,
  extra: unknown
): SessionDelegationIdentity | null {
  if (!extra || typeof extra !== 'object') return null;
  const row = extra as Record<string, unknown>;
  if (![ownerId, frameId, row.frame_incarnation_id, row.root_frame_id].every(sessionGrantIdentifier)) return null;
  return {
    ownerId,
    frameId,
    frameIncarnationId: row.frame_incarnation_id,
    rootFrameId: row.root_frame_id,
  } as SessionDelegationIdentity;
}
export function decodeSessionDelegationSnapshot(value: unknown): SessionDelegationSnapshot {
  const row = object(value);
  if (row.schema !== 'x-science.session-delegation-policy.v1' || typeof row.has_live_delegated_attempts !== 'boolean')
    invalid();
  return {
    schema: 'x-science.session-delegation-policy.v1',
    scope: scope(row.scope),
    policy: policy(row.policy),
    revision: revision(row.revision),
    has_live_delegated_attempts: row.has_live_delegated_attempts as boolean,
  };
}
function assertIdentity(
  snapshot: SessionDelegationSnapshot,
  identity: SessionDelegationIdentity,
  expected?: SessionDelegationScope
): void {
  if (
    snapshot.scope.root_frame_id !== identity.rootFrameId ||
    (identity.frameId === identity.rootFrameId && snapshot.scope.root_incarnation_id !== identity.frameIncarnationId) ||
    (expected && !sameSessionDelegationScope(snapshot.scope, expected))
  )
    throw new SessionDelegationScopeMismatch();
}
export class SessionDelegationScopeMismatch extends Error {
  constructor() {
    super('Session delegation scope changed');
  }
}
export function freezeSessionDelegationCommand(
  snapshot: SessionDelegationSnapshot,
  desired: DelegationPolicy
): SessionDelegationCommand {
  if (snapshot.revision >= Number.MAX_SAFE_INTEGER) invalid();
  return Object.freeze({
    schema: 'x-science.session-delegation-command.v1',
    command_id: uuid(),
    scope: Object.freeze({ ...snapshot.scope }),
    expected_revision: snapshot.revision,
    desired_policy: desired,
  });
}
const endpoint = (identity: SessionDelegationIdentity) =>
  `/api/frames/${encodeURIComponent(identity.frameId)}/delegation-policy`;
export async function loadSessionDelegationPolicy(
  identity: SessionDelegationIdentity,
  expected?: SessionDelegationScope,
  signal?: AbortSignal
): Promise<SessionDelegationSnapshot> {
  const snapshot = decodeSessionDelegationSnapshot(
    await httpRequest<unknown>('GET', endpoint(identity), undefined, { signal, timeoutMs: POLICY_REQUEST_TIMEOUT_MS })
  );
  assertIdentity(snapshot, identity, expected);
  return snapshot;
}
export class SessionDelegationRevisionConflict extends Error {
  constructor(readonly snapshot: SessionDelegationSnapshot) {
    super('Session delegation policy changed');
  }
}
export async function changeSessionDelegationPolicy(
  identity: SessionDelegationIdentity,
  command: SessionDelegationCommand
): Promise<SessionDelegationMutation> {
  if (command.scope.root_frame_id !== identity.rootFrameId) invalid();
  let value: unknown;
  try {
    value = await httpRequest<unknown>('POST', endpoint(identity), command, { timeoutMs: POLICY_REQUEST_TIMEOUT_MS });
  } catch (error) {
    if (isBackendHttpError(error) && error.code === 'SESSION_DELEGATION_REVISION_CONFLICT') {
      const snapshot = decodeSessionDelegationSnapshot(object(error.body).snapshot);
      assertIdentity(snapshot, identity, command.scope);
      throw new SessionDelegationRevisionConflict(snapshot);
    }
    throw error;
  }
  const row = object(value),
    receipt = object(row.receipt),
    echoed = object(receipt.command);
  if (
    row.schema !== 'x-science.session-delegation-mutation.v1' ||
    receipt.schema !== 'x-science.session-delegation-receipt.v1' ||
    typeof row.replayed !== 'boolean' ||
    echoed.schema !== command.schema ||
    echoed.command_id !== command.command_id ||
    echoed.expected_revision !== command.expected_revision ||
    echoed.desired_policy !== command.desired_policy ||
    !sameSessionDelegationScope(scope(echoed.scope), command.scope) ||
    receipt.applied_revision !== command.expected_revision + 1 ||
    receipt.applied_policy !== command.desired_policy ||
    typeof receipt.applied_at !== 'string' ||
    !Number.isFinite(Date.parse(receipt.applied_at))
  )
    invalid();
  const snapshot = decodeSessionDelegationSnapshot(row.snapshot);
  assertIdentity(snapshot, identity, command.scope);
  if (snapshot.revision < (receipt.applied_revision as number)) invalid();
  if (snapshot.revision === receipt.applied_revision && snapshot.policy !== receipt.applied_policy) invalid();
  return {
    schema: 'x-science.session-delegation-mutation.v1',
    snapshot,
    replayed: row.replayed as boolean,
    receipt: {
      schema: 'x-science.session-delegation-receipt.v1',
      command,
      applied_revision: receipt.applied_revision as number,
      applied_policy: command.desired_policy,
      applied_at: receipt.applied_at as string,
    },
  };
}
