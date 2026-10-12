import { describe, expect, it } from 'vitest';
import { loadXScienceGovernance } from '@/renderer/services/xScienceGovernance';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science governance integration', () => {
  it('reads memory, approvals and provenance from the real backend', async () => {
    const snapshot = await loadXScienceGovernance({
      baseUrl: gatewayBaseUrl,
      fetchImpl: await createXScienceTestFetch(gatewayBaseUrl),
    });

    expect(snapshot.gateway.healthy).toBe(true);
    expect(snapshot.gateway.agentsRegistered).toBeGreaterThan(0);
    expect(typeof snapshot.memory.enabled).toBe('boolean');
    expect(snapshot.memory.totalRows).toBeGreaterThanOrEqual(0);
    expect(snapshot.approvals.total).toBeGreaterThanOrEqual(0);
    expect(snapshot.approvals.grants.every((grant) => grant.key.length > 0)).toBe(true);
    expect(snapshot.provenance.totalArtifacts).toBeGreaterThan(0);
    expect(snapshot.provenance.classes.some((item) => item.name === 'managed')).toBe(true);
    expect(snapshot.provenance.integrityIssues).toBeGreaterThanOrEqual(0);
  });
});
