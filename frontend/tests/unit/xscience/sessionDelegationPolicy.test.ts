import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  loadSessionDelegationPolicy,
  freezeSessionDelegationCommand,
  changeSessionDelegationPolicy,
} from '@/renderer/services/runtime/sessionDelegationPolicy';

const { httpRequest } = vi.hoisted(() => ({ httpRequest: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest,
}));
const identity = {
  ownerId: 'owner',
  frameId: 'root-frame',
  frameIncarnationId: 'root-incarnation',
  rootFrameId: 'root-frame',
};
const snapshot = (overrides = {}) => ({
  schema: 'x-science.session-delegation-policy.v1' as const,
  scope: { root_frame_id: identity.rootFrameId, root_incarnation_id: identity.frameIncarnationId },
  policy: 'allow' as const,
  revision: 0,
  has_live_delegated_attempts: false,
  ...overrides,
});
const command = () => freezeSessionDelegationCommand(snapshot(), 'deny');
const mutation = (body = command(), current = snapshot({ revision: 1, policy: 'deny' })) => ({
  schema: 'x-science.session-delegation-mutation.v1',
  replayed: false,
  receipt: {
    schema: 'x-science.session-delegation-receipt.v1',
    command: body,
    applied_revision: 1,
    applied_policy: 'deny',
    applied_at: '2026-10-19T00:00:00Z',
  },
  snapshot: current,
});
describe('session delegation typed service', () => {
  beforeEach(() => httpRequest.mockReset());
  it('rejects a first root GET belonging to a different root incarnation', async () => {
    httpRequest.mockResolvedValue(
      snapshot({ scope: { root_frame_id: 'root-frame', root_incarnation_id: 'replacement-root' } })
    );
    await expect(loadSessionDelegationPolicy(identity)).rejects.toThrow();
  });
  it('rejects a mutation snapshot contradicting its receipt at the same revision', async () => {
    const body = command();
    httpRequest.mockResolvedValue(mutation(body, snapshot({ revision: 1, policy: 'allow' })));
    await expect(changeSessionDelegationPolicy(identity, body)).rejects.toThrow();
  });
  it('accepts the shared real root scope for a child frame without guessing its incarnation', async () => {
    httpRequest.mockResolvedValue(snapshot());
    const child = { ...identity, frameId: 'child-frame', frameIncarnationId: 'child-incarnation' };
    expect(await loadSessionDelegationPolicy(child)).toEqual(snapshot());
    expect(httpRequest.mock.calls[0][1]).toBe('/api/frames/child-frame/delegation-policy');
    expect(httpRequest.mock.calls[0][3]).toMatchObject({ timeoutMs: 8000 });
  });
  it('keeps the command immutable and accepts a newer current snapshot with a different policy', async () => {
    const body = command();
    httpRequest.mockResolvedValue(mutation(body, snapshot({ revision: 2, policy: 'allow' })));
    const result = await changeSessionDelegationPolicy(identity, body);
    expect(Object.isFrozen(body)).toBe(true);
    expect(Object.isFrozen(body.scope)).toBe(true);
    expect(result.receipt.command).toBe(body);
    expect(result.receipt.applied_policy).toBe('deny');
    expect(result.snapshot.policy).toBe('allow');
    expect(result.snapshot.revision).toBe(2);
  });
  it('rejects a different echoed command rather than treating its receipt as our result', async () => {
    const body = command(),
      response = mutation(body);
    response.receipt.command = { ...body, command_id: 'unrelated-command' };
    httpRequest.mockResolvedValue(response);
    await expect(changeSessionDelegationPolicy(identity, body)).rejects.toThrow();
  });
});
