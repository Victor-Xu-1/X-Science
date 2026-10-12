import { describe, expect, it } from 'vitest';
import { loadXScienceLineageMessages } from '@/renderer/services/xScienceLineageMessages';
import { loadRealConversationSummaries } from './xscienceRealConversationFixture';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science lineage messages real gateway', () => {
  it('loads native message rows from the deployed WebHost mapping', async () => {
    const authenticatedFetch = await createXScienceTestFetch(gatewayBaseUrl);
    const fetchImpl = ((input: string | URL | Request, init?: RequestInit) =>
      authenticatedFetch(new URL(String(input), gatewayBaseUrl), init)) as typeof fetch;
    const conversations = await loadRealConversationSummaries(gatewayBaseUrl, authenticatedFetch);
    const candidates = await Promise.all(
      conversations.map(async (conversation) => {
        const result = await loadXScienceLineageMessages(conversation.id, {
          fetchImpl,
          limit: 20,
        }).catch(() => null);
        return result && result.items.length > 0 ? result : null;
      })
    );
    const result = candidates.find((candidate) => candidate !== null);

    expect(result, 'migrated backend must expose at least one conversation with native messages').toBeTruthy();
    if (!result) throw new Error('No migrated conversation contains native messages');
    expect(result.items.length).toBeGreaterThan(0);
    expect(result.items.some((item) => item.kind === 'text' || item.kind === 'tool')).toBe(true);
  });
});
