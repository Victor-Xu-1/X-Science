import { describe, expect, it, vi } from 'vitest';
import {
  authorizeXScienceMcpConnector,
  configureXScienceMcpConnectorAPIKey,
  loadXScienceMcpDirectoryHealth,
  loadXScienceMcpToolPermissions,
  reconcileXScienceMcpServers,
  setXScienceMcpConnectorEnabled,
  updateXScienceMcpToolPermission,
} from '@/renderer/services/xScienceCapabilities';

describe('X-Science MCP capability service', () => {
  it('rejects a tool permission discovery failure envelope instead of reporting an empty catalog', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            tools: [],
            skipApprovalsActive: false,
            error: 'private provider diagnostic',
          }),
          { status: 200 }
        )
    );
    await expect(
      loadXScienceMcpToolPermissions('bundled:structures-interactions', { fetchImpl })
    ).rejects.toMatchObject({ name: 'XScienceCapabilityError', message: 'MCP tool permissions are unavailable' });
  });

  it.each([null, {}, { tools: 'invalid' }])('rejects malformed tool permission catalogs: %j', async (payload) => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(payload), { status: 200 }));
    await expect(
      loadXScienceMcpToolPermissions('bundled:structures-interactions', { fetchImpl })
    ).rejects.toMatchObject({ name: 'XScienceCapabilityError', message: 'MCP tool permissions are unavailable' });
  });

  it('retains a truly empty successful tool permission catalog', async () => {
    const fetchImpl = vi.fn(
      async () => new Response(JSON.stringify({ tools: [], skipApprovalsActive: false }), { status: 200 })
    );
    await expect(loadXScienceMcpToolPermissions('bundled:structures-interactions', { fetchImpl })).resolves.toEqual({
      tools: [],
      skipApprovalsActive: false,
    });
  });

  it('normalizes real directory health payloads', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(JSON.stringify({ directoryHealth: { ok: true, detail: 'all connectors loaded' } }), {
          status: 200,
        })
    );

    await expect(loadXScienceMcpDirectoryHealth({ fetchImpl })).resolves.toEqual({
      ok: true,
      detail: 'all connectors loaded',
    });
  });

  it('sends connector enabled state to the encoded lifecycle endpoint', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ enabled: false }), { status: 200 }));

    await setXScienceMcpConnectorEnabled('bundled:pubmed', false, { fetchImpl });

    expect(fetchImpl).toHaveBeenCalledWith(
      '/api/mcp-servers/connectors/bundled%3Apubmed/enabled',
      expect.objectContaining({
        method: 'PUT',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: '{"enabled":false}',
      })
    );
  });

  it('requests backend reconciliation as a real mutation', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ reconciled: 24 }), { status: 200 }));

    await expect(reconcileXScienceMcpServers({ fetchImpl })).resolves.toEqual({ reconciled: 24 });
    expect(fetchImpl).toHaveBeenCalledWith('/api/mcp-servers/reconcile', expect.objectContaining({ method: 'POST' }));
  });

  it('returns an OAuth authorization URL from connector authorization', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(JSON.stringify({ authorizationUrl: 'https://provider.example/authorize' }), { status: 200 })
    );

    await expect(authorizeXScienceMcpConnector('remote:clinical', { fetchImpl })).resolves.toEqual({
      authorizationUrl: 'https://provider.example/authorize',
    });
  });

  it('sends an owner-provided API key only to the credential endpoint', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ apiKeyConfigured: true }), { status: 200 }));

    await configureXScienceMcpConnectorAPIKey('bundled:tamarind-bio', 'provider-secret', { fetchImpl });

    expect(fetchImpl).toHaveBeenCalledWith(
      '/api/mcp-servers/connectors/bundled%3Atamarind-bio/credential',
      expect.objectContaining({
        method: 'PUT',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: '{"apiKey":"provider-secret"}',
      })
    );
  });

  it('normalizes tool permission records and updates one tool state', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            tools: [{ toolName: 'search_articles', title: 'Search', readOnlyHint: true, state: 'allow' }],
            skipApprovalsActive: false,
          }),
          { status: 200 }
        )
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ success: true }), { status: 200 }));

    await expect(loadXScienceMcpToolPermissions('bundled:pubmed', { fetchImpl })).resolves.toEqual({
      tools: [{ toolName: 'search_articles', title: 'Search', description: '', readOnlyHint: true, state: 'allow' }],
      skipApprovalsActive: false,
    });
    await updateXScienceMcpToolPermission('bundled:pubmed', 'search_articles', 'deny', { fetchImpl });

    expect(fetchImpl).toHaveBeenLastCalledWith(
      '/api/mcp-servers/bundled%3Apubmed/tool-grants',
      expect.objectContaining({
        method: 'POST',
        body: '{"toolName":"search_articles","decision":"deny"}',
      })
    );
  });

  it('throws a typed HTTP error with backend detail', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(JSON.stringify({ detail: 'connector failed to start' }), {
          status: 409,
          headers: { 'content-type': 'application/json' },
        })
    );

    await expect(setXScienceMcpConnectorEnabled('broken', true, { fetchImpl })).rejects.toMatchObject({
      name: 'XScienceCapabilityError',
      status: 409,
      message: 'connector failed to start',
    });
  });
});
