import { loadXScienceKernels } from '@/renderer/services/xScienceNotebook';
import { afterEach, describe, expect, it } from 'vitest';
import { createRealConversationFixture, type RealConversationFixture } from './xscienceRealConversationFixture';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';
let fixture: RealConversationFixture | null = null;

afterEach(async () => {
  await fixture?.dispose();
  fixture = null;
});

describe('X-Science notebook gateway', () => {
  it('returns the real v1.1 kernel inventory for a newly created frame', async () => {
    fixture = await createRealConversationFixture({ gatewayBaseUrl });

    await expect(
      loadXScienceKernels(fixture.conversationId, {
        baseUrl: gatewayBaseUrl,
        fetchImpl: fixture.fetchImpl,
      })
    ).resolves.toEqual({ kernels: [], hasHistory: false, machine: null });
  });
});
