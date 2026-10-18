import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  decodeSessionGrantsSnapshot,
  freezeSessionGrantRevoke,
  loadSessionPermissionGrants,
  revokeSessionPermissionGrants,
} from '@/renderer/services/runtime/sessionPermissionGrants';
import {
  sessionGrantA,
  sessionGrantB,
  sessionGrantC,
  sessionGrantIdentity,
  sessionGrantScope,
  sessionGrantsSnapshot,
  sessionGrantRevokeBody,
  sessionGrantRevokeResponse,
} from './sessionPermissionGrantsFixtures';

const { httpRequest } = vi.hoisted(() => ({ httpRequest: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest }));
const path = '/api/conversations/grant-child/permission-grants';

describe('sessionPermissionGrants typed transport', () => {
  beforeEach(() => {
    httpRequest.mockReset();
  });
  it('bootstraps real child and root incarnations without guessing a root incarnation', async () => {
    httpRequest.mockResolvedValue(sessionGrantsSnapshot());
    const snapshot = await loadSessionPermissionGrants(sessionGrantIdentity);
    expect(httpRequest.mock.calls[0].slice(0, 2)).toEqual(['GET', path]);
    expect(snapshot.scope).toEqual(sessionGrantScope);
    expect(snapshot.scope.frame_id).not.toBe(snapshot.scope.root_frame_id);
  });
  it('refreshes with exactly the original three scope fields and fences returned scope', async () => {
    httpRequest.mockResolvedValue(sessionGrantsSnapshot());
    await loadSessionPermissionGrants(sessionGrantIdentity, sessionGrantScope);
    const url = new URL(httpRequest.mock.calls[0][1], 'https://example.invalid');
    expect(Object.fromEntries(url.searchParams)).toEqual({
      expected_frame_incarnation_id: sessionGrantScope.frame_incarnation_id,
      expected_root_frame_id: sessionGrantScope.root_frame_id,
      expected_root_frame_incarnation_id: sessionGrantScope.root_frame_incarnation_id,
    });
    httpRequest.mockResolvedValue({
      ...sessionGrantsSnapshot(),
      scope: { ...sessionGrantScope, root_frame_incarnation_id: 'new-root' },
    });
    await expect(loadSessionPermissionGrants(sessionGrantIdentity, sessionGrantScope)).rejects.toThrow();
  });
  it.each(['frame_id', 'frame_incarnation_id', 'root_frame_id'] as const)(
    'rejects bootstrap with mismatched %s',
    async (field) => {
      httpRequest.mockResolvedValue({ ...sessionGrantsSnapshot(), scope: { ...sessionGrantScope, [field]: 'other' } });
      await expect(loadSessionPermissionGrants(sessionGrantIdentity)).rejects.toThrow();
    }
  );
  it.each([
    null,
    { ...sessionGrantsSnapshot(), schema: 'legacy' },
    { ...sessionGrantsSnapshot(), grants: [{ ...sessionGrantA, scope: 'project' }] },
    { ...sessionGrantsSnapshot(), grants: [{ ...sessionGrantA, tier: 'deny' }] },
    { ...sessionGrantsSnapshot(), grants: [sessionGrantA, sessionGrantA] },
    { ...sessionGrantsSnapshot(), grants: [sessionGrantA, { ...sessionGrantA, grant_id: 'other' }] },
    { ...sessionGrantsSnapshot(), legacy_unbound_count: -1 },
    { ...sessionGrantsSnapshot(), incomplete_reason: 'limit' },
    { ...sessionGrantsSnapshot(), grants: Array.from({ length: 1001 }, () => sessionGrantA) },
  ])('rejects malformed, inherited, duplicate, or oversized snapshots', (value) => {
    expect(() => decodeSessionGrantsSnapshot(value)).toThrow();
  });
  it('freezes only observed IDs in the chosen category without changing opaque IDs', () => {
    const otherInCategory = { ...sessionGrantB, category_key: sessionGrantA.category_key };
    const snapshot = sessionGrantsSnapshot([sessionGrantA, otherInCategory, sessionGrantC]);
    const body = freezeSessionGrantRevoke(snapshot, sessionGrantA.category_key);
    expect(body).toEqual(sessionGrantRevokeBody([sessionGrantA, otherInCategory]));
    expect(Object.isFrozen(body)).toBe(true);
    expect(Object.isFrozen(body.grants)).toBe(true);
    expect(Object.isFrozen(body.grants[0])).toBe(true);
    snapshot.grants.push({ ...sessionGrantC, grant_id: 'later' });
    expect(body.grants).toHaveLength(2);
  });
  it('refuses full clear for incomplete lists but allows exact visible category revocation', () => {
    const snapshot = { ...sessionGrantsSnapshot(), incomplete: true };
    expect(() => freezeSessionGrantRevoke(snapshot)).toThrow();
    expect(freezeSessionGrantRevoke(snapshot, sessionGrantA.category_key).grants).toHaveLength(1);
  });
  it('preserves mixed changed and revoked results without retrying changed generations', async () => {
    const renewed = { ...sessionGrantA, grant_id: 'renewed' };
    const response = sessionGrantRevokeResponse([sessionGrantA, sessionGrantB], [renewed]);
    response.results[0].status = 'changed';
    httpRequest.mockResolvedValue(response);
    const result = await revokeSessionPermissionGrants(
      sessionGrantIdentity,
      sessionGrantScope,
      freezeSessionGrantRevoke(sessionGrantsSnapshot([sessionGrantA, sessionGrantB]))
    );
    expect(result.results.map((row) => row.status)).toEqual(['changed', 'revoked']);
    expect(result.snapshot.grants).toEqual([renewed]);
    expect(httpRequest).toHaveBeenCalledTimes(1);
    expect(httpRequest.mock.calls[0][1]).toBe(path + '/revoke');
  });
  it.each(['wrong-result', 'duplicate-result', 'old-id-survives', 'scope-changed', 'broader-policy'])(
    'rejects inconsistent mutation receipt: %s',
    async (variant) => {
      const response = sessionGrantRevokeResponse();
      if (variant === 'wrong-result') response.results[0].grant_id = 'not-requested';
      if (variant === 'duplicate-result') response.results.push(response.results[0]);
      if (variant === 'old-id-survives') response.snapshot = sessionGrantsSnapshot();
      if (variant === 'scope-changed') response.scope.root_frame_incarnation_id = 'other';
      if (variant === 'broader-policy') response.broader_policies_unchanged = false;
      httpRequest.mockResolvedValue(response);
      await expect(
        revokeSessionPermissionGrants(
          sessionGrantIdentity,
          sessionGrantScope,
          freezeSessionGrantRevoke(sessionGrantsSnapshot())
        )
      ).rejects.toThrow();
    }
  );
  it('does not automatically resend a mutation after a lost response', async () => {
    httpRequest.mockRejectedValue(new Error('lost acknowledgement'));
    await expect(
      revokeSessionPermissionGrants(
        sessionGrantIdentity,
        sessionGrantScope,
        freezeSessionGrantRevoke(sessionGrantsSnapshot())
      )
    ).rejects.toThrow();
    expect(httpRequest).toHaveBeenCalledTimes(1);
  });
});
