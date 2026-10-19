import type {
  SessionSpecialistCommand,
  SessionSpecialistSnapshot,
  SpecialistSelection,
} from '@/renderer/services/runtime/sessionSpecialistTypes';
export const specialistIdentity = {
  ownerId: 'owner',
  frameId: 'frame',
  frameIncarnationId: 'inc',
  rootFrameId: 'root',
};
export const specialistScope = {
  frame_id: 'frame',
  frame_incarnation_id: 'inc',
  root_frame_id: 'root',
  root_incarnation_id: 'root-inc',
};
export const specialistSelection = (name = 'AGENT_A', revision = 0): SpecialistSelection => ({
  agent_name: `ENGINE_${name}`,
  selection_name: name,
  assistant_id: `assistant:${name}`,
  display_name: name,
  is_default: false,
  snapshot_id: revision ? `snapshot:${name}:${revision}` : '',
  revision,
});
const empty = {
  agent_name: '',
  selection_name: '',
  assistant_id: '',
  display_name: '',
  is_default: false,
  snapshot_id: '',
  revision: 0,
};
export const specialistSnapshot = (
  revision = 0,
  name = 'AGENT_A',
  patch: Partial<SessionSpecialistSnapshot> = {}
): SessionSpecialistSnapshot => ({
  schema: 'x-science.session-specialist.v1',
  scope: specialistScope,
  revision,
  branch: { id: 'branch-a', generation: 1 },
  prepared_branch: { id: 'branch-a', generation: 1 },
  desired: revision ? specialistSelection(name, revision) : empty,
  applied: specialistSelection('AGENT_A'),
  phase: revision ? 'prepared' : 'legacy',
  applied_unavailable: false,
  has_live_attempt: false,
  ...patch,
});
export const specialistMutation = (
  command: SessionSpecialistCommand,
  current = specialistSnapshot(command.expected_revision + 1, command.desired_agent || 'DEFAULT')
) => ({
  schema: 'x-science.session-specialist-mutation.v1',
  replayed: false,
  receipt: {
    command,
    accepted_revision: command.expected_revision + 1,
    snapshot_id: `snapshot:${command.desired_agent || 'DEFAULT'}:${command.expected_revision + 1}`,
    accepted_at: '2026-10-19T00:00:00Z',
  },
  snapshot: current,
});
