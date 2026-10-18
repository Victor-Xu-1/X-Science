import { afterEach, describe, expect, it } from 'vitest';
import { getContextCompactionController } from '@/renderer/services/compaction/contextCompactionRegistry';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { manualCompactionIdentity as identity } from './manualContextCompactionFixtures';
import { retainCompactionCapacity } from './contextCompactionCapacityTestFixture';

afterEach(() => {
  setRendererAccountOwner(null);
});
describe('C11 bounded maintenance registry', () => {
  it('keeps every unresolved original request and admits the same blocked scope only after real reconciliation frees capacity', async () => {
    setRendererAccountOwner(identity.ownerId);
    const capacity = await retainCompactionCapacity(identity.ownerId);
    try {
      expect(getContextCompactionController(identity)).toBeNull();
      for (const session of capacity.sessions) {
        expect(session.controller.retained).toBe(true);
        expect(getContextCompactionController(session.identity)).toBe(session.controller);
        expect(session.controller.getSnapshot().uncertain).toBe('submit');
      }
      await capacity.settleOne();
      const controller = getContextCompactionController(identity);
      expect(controller).not.toBeNull();
      expect(getContextCompactionController(identity)).toBe(controller);
      expect(capacity.submit).toHaveBeenCalledTimes(32);
      expect(capacity.sessions.slice(1).every((session) => session.controller.retained)).toBe(true);
    } finally {
      capacity.dispose();
    }
  });
});
