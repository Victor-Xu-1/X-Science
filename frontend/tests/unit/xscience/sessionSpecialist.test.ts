import { beforeEach, expect, it, vi } from 'vitest';
import {
  changeSessionSpecialist,
  freezeSpecialistCommand,
  loadSessionSpecialist,
} from '@/renderer/services/runtime/sessionSpecialist';
import {
  decodeSpecialistInputReference,
  decodeSpecialistSnapshot,
} from '@/renderer/services/runtime/sessionSpecialistCodec';
import {
  specialistIdentity as identity,
  specialistMutation,
  specialistScope,
  specialistSnapshot,
} from './sessionSpecialistFixtures';
const { httpRequest } = vi.hoisted(() => ({ httpRequest: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest,
}));
beforeEach(() => httpRequest.mockReset());
it('rejects a first GET from a replaced frame and a replaced root', async () => {
  httpRequest.mockResolvedValue(
    specialistSnapshot(0, 'AGENT_A', { scope: { ...specialistScope, frame_incarnation_id: 'other' } })
  );
  await expect(loadSessionSpecialist(identity)).rejects.toThrow();
  const root = { ...identity, frameId: 'root', frameIncarnationId: 'old-root' };
  httpRequest.mockResolvedValue(
    specialistSnapshot(0, 'AGENT_A', {
      scope: { ...specialistScope, frame_id: 'root', frame_incarnation_id: 'old-root' },
    })
  );
  await expect(loadSessionSpecialist(root)).rejects.toThrow();
});
it('keeps current branch separate from a frozen prepared branch including pre-stream Guid', () => {
  const value = specialistSnapshot(1, 'AGENT_B', {
    branch: { id: 'current', generation: 2 },
    prepared_branch: { id: '', generation: 0 },
  });
  const decoded = decodeSpecialistSnapshot(value);
  expect(decoded.branch.id).toBe('current');
  expect(decoded.prepared_branch).toEqual({ id: '', generation: 0 });
  expect(
    decodeSpecialistInputReference({
      scope: decoded.scope,
      branch: decoded.prepared_branch,
      revision: 1,
      snapshot_id: decoded.desired.snapshot_id,
    }).branch
  ).toEqual(decoded.prepared_branch);
});
it('uses an explicit default intent and rejects an equal-revision contradictory receipt', async () => {
  const command = freezeSpecialistCommand(specialistSnapshot(), null);
  expect(command).toMatchObject({ desired_agent: '', use_default: true });
  expect(Object.isFrozen(command.scope)).toBe(true);
  httpRequest.mockResolvedValue(specialistMutation(command));
  await expect(changeSessionSpecialist(identity, command)).rejects.toThrow();
});
it('accepts a newer current selection without replacing the original immutable receipt', async () => {
  httpRequest.mockResolvedValueOnce(specialistSnapshot());
  const loaded = await loadSessionSpecialist(identity),
    command = freezeSpecialistCommand(loaded, 'AGENT_B');
  httpRequest.mockResolvedValueOnce(specialistMutation(command, specialistSnapshot(2, 'AGENT_C')));
  const result = await changeSessionSpecialist(identity, command);
  expect(Object.isFrozen(command)).toBe(true);
  expect(Object.isFrozen(command.scope)).toBe(true);
  expect(result.receipt.command).toBe(command);
  expect(result.snapshot.desired.selection_name).toBe('AGENT_C');
});
it('rejects unsafe references and preserves legacy display without inventing a snapshot', () => {
  expect(() =>
    decodeSpecialistInputReference({
      scope: specialistScope,
      branch: { id: 'b', generation: 0 },
      revision: 1,
      snapshot_id: 's',
    })
  ).toThrow();
  const legacy = decodeSpecialistSnapshot(specialistSnapshot());
  expect(legacy.applied.selection_name).toBe('AGENT_A');
  expect(legacy.applied.snapshot_id).toBe('');
  expect(legacy.applied.is_default).toBe(false);
});
