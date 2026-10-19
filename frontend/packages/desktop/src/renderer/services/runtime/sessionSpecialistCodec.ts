import { sessionGrantIdentifier } from './sessionPermissionGrants';
import {
  sameSpecialistBranch,
  sameSpecialistScope,
  sameSpecialistSelection,
  SpecialistScopeMismatch,
  type SessionSpecialistCommand,
  type SessionSpecialistIdentity,
  type SessionSpecialistMutation,
  type SessionSpecialistSnapshot,
  type SpecialistBranch,
  type SpecialistScope,
  type SpecialistSelection,
} from './sessionSpecialistTypes';
export { decodeSpecialistInputReference } from '@/common/adapter/specialistBinding';

const invalid = (): never => {
  throw new Error('Invalid session specialist response');
};
const object = (v: unknown): Record<string, unknown> =>
  v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : invalid();
const integer = (v: unknown): number => (typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 ? v : invalid());
const text = (v: unknown, empty = false): string =>
  typeof v === 'string' && v.length <= 1024 && ((empty && v === '') || sessionGrantIdentifier(v)) ? v : invalid();
function branch(value: unknown): SpecialistBranch {
  const row = object(value),
    id = text(row.id, true),
    generation = integer(row.generation);
  if ((id === '') !== (generation === 0)) invalid();
  return Object.freeze({ id, generation });
}
function scope(value: unknown): SpecialistScope {
  const row = object(value);
  return Object.freeze({
    frame_id: text(row.frame_id),
    frame_incarnation_id: text(row.frame_incarnation_id),
    root_frame_id: text(row.root_frame_id),
    root_incarnation_id: text(row.root_incarnation_id),
  });
}
function selection(value: unknown): SpecialistSelection {
  const row = object(value);
  if (typeof row.is_default !== 'boolean' || typeof row.display_name !== 'string' || row.display_name.length > 1024)
    invalid();
  const result = Object.freeze({
    agent_name: text(row.agent_name, true),
    selection_name: text(row.selection_name, true),
    assistant_id: text(row.assistant_id, true),
    display_name: row.display_name as string,
    is_default: row.is_default as boolean,
    snapshot_id: text(row.snapshot_id, true),
    revision: integer(row.revision),
  });
  if (
    result.revision > 0 &&
    (!result.snapshot_id || !result.agent_name || !result.selection_name || !result.assistant_id)
  )
    invalid();
  return result;
}
export function assertSpecialistIdentity(
  snapshot: SessionSpecialistSnapshot,
  identity: SessionSpecialistIdentity,
  expected?: SpecialistScope
): void {
  if (
    snapshot.scope.frame_id !== identity.frameId ||
    snapshot.scope.frame_incarnation_id !== identity.frameIncarnationId ||
    snapshot.scope.root_frame_id !== identity.rootFrameId ||
    (identity.frameId === identity.rootFrameId && snapshot.scope.root_incarnation_id !== identity.frameIncarnationId) ||
    (expected && !sameSpecialistScope(snapshot.scope, expected))
  )
    throw new SpecialistScopeMismatch();
}
export function decodeSpecialistSnapshot(value: unknown): SessionSpecialistSnapshot {
  const row = object(value);
  if (
    row.schema !== 'x-science.session-specialist.v1' ||
    !['legacy', 'prepared', 'applied'].includes(String(row.phase)) ||
    typeof row.has_live_attempt !== 'boolean' ||
    typeof row.applied_unavailable !== 'boolean' ||
    (row.preparation_error !== undefined && typeof row.preparation_error !== 'string')
  )
    invalid();
  const result: SessionSpecialistSnapshot = Object.freeze({
    schema: 'x-science.session-specialist.v1',
    scope: scope(row.scope),
    revision: integer(row.revision),
    branch: branch(row.branch),
    prepared_branch: branch(row.prepared_branch),
    desired: selection(row.desired),
    applied: selection(row.applied),
    phase: row.phase as SessionSpecialistSnapshot['phase'],
    has_live_attempt: row.has_live_attempt as boolean,
    applied_unavailable: row.applied_unavailable as boolean,
    ...(row.preparation_error ? { preparation_error: text(row.preparation_error) } : {}),
  });
  if (
    result.applied.revision > result.revision ||
    result.desired.revision !== result.revision ||
    (result.revision === 0 && result.phase !== 'legacy') ||
    (result.revision > 0 && !result.desired.snapshot_id) ||
    (result.phase === 'applied' && !sameSpecialistSelection(result.applied, result.desired))
  )
    invalid();
  return result;
}
function matchingCommand(value: unknown, command: SessionSpecialistCommand): void {
  const row = object(value);
  if (
    row.schema !== command.schema ||
    row.command_id !== command.command_id ||
    row.expected_revision !== command.expected_revision ||
    row.desired_agent !== command.desired_agent ||
    (row.use_default === true) !== (command.use_default === true) ||
    !sameSpecialistScope(scope(row.scope), command.scope) ||
    !sameSpecialistBranch(branch(row.branch), command.branch)
  )
    invalid();
}
export function decodeSpecialistMutation(
  value: unknown,
  command: SessionSpecialistCommand,
  identity: SessionSpecialistIdentity
): SessionSpecialistMutation {
  const row = object(value),
    receipt = object(row.receipt);
  matchingCommand(receipt.command, command);
  if (
    row.schema !== 'x-science.session-specialist-mutation.v1' ||
    typeof row.replayed !== 'boolean' ||
    receipt.accepted_revision !== command.expected_revision + 1 ||
    typeof receipt.accepted_at !== 'string' ||
    !Number.isFinite(Date.parse(receipt.accepted_at))
  )
    invalid();
  const snapshot = decodeSpecialistSnapshot(row.snapshot),
    id = text(receipt.snapshot_id);
  assertSpecialistIdentity(snapshot, identity, command.scope);
  if (
    snapshot.revision < command.expected_revision + 1 ||
    (snapshot.revision === command.expected_revision + 1 &&
      (snapshot.desired.snapshot_id !== id ||
        (command.use_default
          ? !snapshot.desired.is_default
          : snapshot.desired.selection_name !== command.desired_agent)))
  )
    invalid();
  return {
    schema: 'x-science.session-specialist-mutation.v1',
    snapshot,
    replayed: row.replayed as boolean,
    receipt: {
      command,
      accepted_revision: receipt.accepted_revision as number,
      snapshot_id: id,
      accepted_at: receipt.accepted_at as string,
    },
  };
}
