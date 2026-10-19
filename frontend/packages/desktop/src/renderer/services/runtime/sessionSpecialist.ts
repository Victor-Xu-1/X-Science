import { httpRequest } from '@/common/adapter/httpBridge';
import { uuid } from '@/common/utils';
import { assertSpecialistIdentity, decodeSpecialistMutation, decodeSpecialistSnapshot } from './sessionSpecialistCodec';
import {
  type SessionSpecialistCommand,
  type SessionSpecialistIdentity,
  type SessionSpecialistSnapshot,
  type SpecialistScope,
} from './sessionSpecialistTypes';

const endpoint = (identity: SessionSpecialistIdentity) =>
  `/api/frames/${encodeURIComponent(identity.frameId)}/specialist`;
export async function loadSessionSpecialist(
  identity: SessionSpecialistIdentity,
  expected?: SpecialistScope,
  signal?: AbortSignal
) {
  const snapshot = decodeSpecialistSnapshot(
    await httpRequest<unknown>('GET', endpoint(identity), undefined, { signal, timeoutMs: 8_000 })
  );
  assertSpecialistIdentity(snapshot, identity, expected);
  return snapshot;
}
export function freezeSpecialistCommand(
  snapshot: SessionSpecialistSnapshot,
  name: string | null
): SessionSpecialistCommand {
  if (snapshot.revision >= Number.MAX_SAFE_INTEGER || (name !== null && (!name || name !== name.trim())))
    throw new Error('Invalid specialist selection');
  return Object.freeze({
    schema: 'x-science.session-specialist-command.v1',
    command_id: uuid(),
    scope: Object.freeze({ ...snapshot.scope }),
    expected_revision: snapshot.revision,
    branch: Object.freeze({ ...snapshot.branch }),
    desired_agent: name ?? '',
    ...(name === null ? { use_default: true as const } : {}),
  });
}
export async function changeSessionSpecialist(identity: SessionSpecialistIdentity, command: SessionSpecialistCommand) {
  return decodeSpecialistMutation(
    await httpRequest<unknown>('POST', endpoint(identity), command, { timeoutMs: 8_000 }),
    command,
    identity
  );
}
