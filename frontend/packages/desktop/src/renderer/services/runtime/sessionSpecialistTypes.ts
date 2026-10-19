import type { SessionDelegationIdentity } from './sessionDelegationPolicy';
import type { SpecialistBranch, SpecialistScope } from '@/common/adapter/specialistBinding';
export type {
  SpecialistBranch,
  SpecialistScope,
  SpecialistInputReference,
  SpecialistSendAuthority,
} from '@/common/adapter/specialistBinding';

export type SessionSpecialistIdentity = SessionDelegationIdentity;
export type SpecialistSelection = Readonly<{
  agent_name: string;
  selection_name: string;
  assistant_id: string;
  display_name: string;
  is_default: boolean;
  snapshot_id: string;
  revision: number;
}>;
export type SessionSpecialistSnapshot = Readonly<{
  schema: 'x-science.session-specialist.v1';
  scope: SpecialistScope;
  revision: number;
  branch: SpecialistBranch;
  prepared_branch: SpecialistBranch;
  desired: SpecialistSelection;
  applied: SpecialistSelection;
  phase: 'legacy' | 'prepared' | 'applied';
  has_live_attempt: boolean;
  preparation_error?: string;
  applied_unavailable: boolean;
}>;
export type SessionSpecialistCommand = Readonly<{
  schema: 'x-science.session-specialist-command.v1';
  command_id: string;
  scope: SpecialistScope;
  expected_revision: number;
  branch: SpecialistBranch;
  desired_agent: string;
  use_default?: true;
}>;
export type SessionSpecialistMutation = {
  schema: 'x-science.session-specialist-mutation.v1';
  receipt: {
    command: SessionSpecialistCommand;
    accepted_revision: number;
    snapshot_id: string;
    accepted_at: string;
  };
  snapshot: SessionSpecialistSnapshot;
  replayed: boolean;
};
export class SpecialistScopeMismatch extends Error {
  constructor() {
    super('Specialist conversation scope changed');
  }
}
export class SpecialistPreparationRequired extends Error {
  constructor() {
    super('Specialist selection requires confirmation');
  }
}
export type SpecialistFailure =
  | 'read'
  | 'uncertain'
  | 'conflict'
  | 'scope'
  | 'unavailable'
  | 'invalid'
  | 'branch'
  | null;

export const sameSpecialistBranch = (a: SpecialistBranch, b: SpecialistBranch) =>
  a.id === b.id && a.generation === b.generation;
export const sameSpecialistScope = (a: SpecialistScope, b: SpecialistScope) =>
  a.frame_id === b.frame_id &&
  a.frame_incarnation_id === b.frame_incarnation_id &&
  a.root_frame_id === b.root_frame_id &&
  a.root_incarnation_id === b.root_incarnation_id;
export const sameSpecialistSelection = (a: SpecialistSelection, b: SpecialistSelection) =>
  a.agent_name === b.agent_name &&
  a.selection_name === b.selection_name &&
  a.assistant_id === b.assistant_id &&
  a.is_default === b.is_default &&
  a.snapshot_id === b.snapshot_id &&
  a.revision === b.revision;
