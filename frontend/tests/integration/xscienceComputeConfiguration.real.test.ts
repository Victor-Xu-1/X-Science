import { loadXScienceBioNemoSettings, loadXScienceModalSettings } from '@/renderer/services/xScienceCompute';
import { describe, expect, it } from 'vitest';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science real compute configuration gateway', () => {
  it('loads the native v1.1 Modal and BioNeMo configuration contracts', async () => {
    const authenticatedFetch = await createXScienceTestFetch(gatewayBaseUrl);
    const fetchImpl: typeof fetch = (input, init) => {
      const endpoint = typeof input === 'string' && input.startsWith('/') ? `${gatewayBaseUrl}${input}` : input;
      return authenticatedFetch(endpoint, init);
    };

    const [modal, bioNemo] = await Promise.all([
      loadXScienceModalSettings(fetchImpl),
      loadXScienceBioNemoSettings(fetchImpl),
    ]);

    expect(modal).toMatchObject({
      provider: 'modal',
      enabled: expect.any(Boolean),
      profiles: expect.any(Array),
      tomlMissing: expect.any(Boolean),
      hasStoredCredential: expect.any(Boolean),
    });
    expect(['unrestricted', 'allowlist', 'blocked', null]).toContain(modal.egressPolicy?.mode ?? null);
    expect(bioNemo).toMatchObject({
      enabled: expect.any(Boolean),
      mode: expect.stringMatching(/^(hosted|local)$/),
      hostedHost: expect.any(String),
    });
  });
});
