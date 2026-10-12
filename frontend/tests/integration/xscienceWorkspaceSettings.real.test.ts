import { describe, expect, it } from 'vitest';
import {
  loadXScienceContactEmail,
  loadXScienceNetworkSettings,
  loadXSciencePermissionGrants,
  loadXScienceSecrets,
  loadXScienceStorageSettings,
  loadXScienceUseIntent,
} from '@/renderer/services/xScienceWorkspaceSettings';
import { createXScienceTestFetch } from './xscienceTestAuth';

const backendBaseUrl = process.env.X_SCIENCE_BACKEND_URL ?? 'http://127.0.0.1:8766';
const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science workspace settings real integration', () => {
  it('reads network, permission, credential and storage state from the live v1.1-compatible backend', async () => {
    const options = {
      baseUrl: backendBaseUrl,
      fetchImpl: await createXScienceTestFetch(backendBaseUrl),
    };
    const [network, grants, secrets, storage, intent, contact] = await Promise.all([
      loadXScienceNetworkSettings(options),
      loadXSciencePermissionGrants(options),
      loadXScienceSecrets(options),
      loadXScienceStorageSettings(options),
      loadXScienceUseIntent(options),
      loadXScienceContactEmail(options),
    ]);

    expect(network.groups.length).toBeGreaterThanOrEqual(6);
    expect(network.groups.some((group) => group.id === 'pkg' && group.locked)).toBe(true);
    expect(Array.isArray(grants)).toBe(true);
    expect(grants.every((grant) => grant.kind.length > 0 && grant.key.length > 0)).toBe(true);
    expect(Array.isArray(secrets)).toBe(true);
    expect(storage.dataDirectory.current).toMatch(/^\//);
    expect(storage.diskUsage.availableBytes).toBeGreaterThan(0);
    expect(Array.isArray(storage.cloudCredentials)).toBe(true);
    expect(['commercial', 'noncommercial']).toContain(intent.intent);
    expect(contact.noticeVersion.length).toBeGreaterThan(0);
  }, 30_000);

  it('reads the same private settings through the authenticated production WebHost bridge', async () => {
    const fetchImpl = await createXScienceTestFetch(gatewayBaseUrl);
    const options = { baseUrl: gatewayBaseUrl, fetchImpl };
    const [network, storage, intent, contact] = await Promise.all([
      loadXScienceNetworkSettings(options),
      loadXScienceStorageSettings(options),
      loadXScienceUseIntent(options),
      loadXScienceContactEmail(options),
    ]);

    expect(network.groups.some((group) => group.id === 'pkg')).toBe(true);
    expect(storage.dataDirectory.current).toMatch(/^\//);
    expect(['commercial', 'noncommercial']).toContain(intent.intent);
    expect(contact.noticeVersion.length).toBeGreaterThan(0);
  }, 30_000);
});
