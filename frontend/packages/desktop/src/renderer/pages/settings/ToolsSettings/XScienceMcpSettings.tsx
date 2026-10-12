import { Button, Empty, Message, Spin } from '@arco-design/web-react';
import Tabs from '@/renderer/components/base/WorkbenchTabs';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { Refresh } from '@icon-park/react';
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  attachXScienceMcpServerToAllAgents,
  authorizeXScienceMcpConnector,
  configureXScienceMcpConnectorAPIKey,
  createXScienceCustomMcpServer,
  deleteXScienceCustomMcpServer,
  detachXScienceMcpServerFromAllAgents,
  disconnectXScienceMcpConnector,
  loadXScienceCustomMcpServers,
  loadXScienceMcpDirectoryHealth,
  loadXScienceMcpServers,
  reconcileXScienceMcpServers,
  setXScienceMcpConnectorEnabled,
  updateXScienceCustomMcpServer,
  type XScienceCustomMcpServer,
  type XScienceCustomMcpServerInput,
  type XScienceMcpDirectoryHealth,
  type XScienceMcpServer,
} from '@/renderer/services/xScienceCapabilities';
import { McpConnectorEditorModal } from './McpConnectorEditorModal';
import { McpMarketplacePanel } from './McpMarketplacePanel';
import { McpOptionalPanel } from './McpOptionalPanel';
import { McpPermissionsModal } from './McpPermissionsModal';
import { resolveXScienceMcpDescription } from '@/renderer/services/mcp/xScienceMcpDescriptions';
import { McpConnectorCard } from './McpConnectorCard';
import { McpConnectorConfigurationModal } from './McpConnectorConfigurationModal';
import { McpLibraryToolbar, type ConnectorFilter, type ConnectorBrowseView } from './McpLibraryToolbar';
import { CONNECTOR_DOMAIN_IDS, resolveConnectorDomain, type ConnectorDomainId } from './connectorDomains';

const ignoreHandledMutationError = (_error: unknown): undefined => undefined;

type XScienceMcpSettingsContentProps = {
  /** When true, renders the merged-page one-line compact header. */
  compactHeader?: boolean;
};

export const XScienceMcpSettingsContent: React.FC<XScienceMcpSettingsContentProps> = ({ compactHeader = false }) => {
  const { t, i18n } = useTranslation();
  const [servers, setServers] = useState<XScienceMcpServer[]>([]);
  const [customServers, setCustomServers] = useState<XScienceCustomMcpServer[]>([]);
  const [directoryHealth, setDirectoryHealth] = useState<XScienceMcpDirectoryHealth | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [reconciling, setReconciling] = useState(false);
  const [mutatingServerId, setMutatingServerId] = useState<string | null>(null);
  const [permissionServer, setPermissionServer] = useState<XScienceMcpServer | null>(null);
  const [credentialServer, setCredentialServer] = useState<XScienceMcpServer | null>(null);
  const [editorVisible, setEditorVisible] = useState(false);
  const [editingServer, setEditingServer] = useState<XScienceCustomMcpServer | null>(null);
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<ConnectorFilter>('all');
  const [domainFilter, setDomainFilter] = useState<ConnectorDomainId>('all');
  const [browseView, setBrowseView] = useState<ConnectorBrowseView | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const loadGenerationRef = useRef(0);
  const initializationPollsRef = useRef(0);

  const customById = useMemo(() => new Map(customServers.map((server) => [server.id, server])), [customServers]);
  const domainOf = useCallback(
    (server: XScienceMcpServer) => resolveConnectorDomain(server, customById.has(server.id)),
    [customById]
  );
  const domainOptions = useMemo(() => {
    const counts = new Map<ConnectorDomainId, number>();
    for (const server of servers) {
      const domain = domainOf(server);
      counts.set(domain, (counts.get(domain) ?? 0) + 1);
    }
    return CONNECTOR_DOMAIN_IDS.filter((id) => id === 'all' || (counts.get(id) ?? 0) > 0).map((id) => ({
      id,
      count: id === 'all' ? servers.length : (counts.get(id) ?? 0),
    }));
  }, [domainOf, servers]);

  const loadServers = useCallback(async () => {
    const generation = ++loadGenerationRef.current;
    setLoading(true);
    setLoadError(false);
    const [serversResult, customResult, healthResult] = await Promise.allSettled([
      loadXScienceMcpServers(),
      loadXScienceCustomMcpServers(),
      loadXScienceMcpDirectoryHealth(),
    ]);
    if (generation !== loadGenerationRef.current) return;
    let failed = false;
    if (serversResult.status === 'fulfilled') setServers(serversResult.value);
    else {
      failed = true;
      console.error('Failed to load X-Science MCP connectors:', serversResult.reason);
    }

    if (customResult.status === 'fulfilled') setCustomServers(customResult.value);
    else {
      failed = true;
      console.error('Failed to load X-Science MCP custom connectors:', customResult.reason);
    }

    if (healthResult.status === 'fulfilled') setDirectoryHealth(healthResult.value);
    else {
      failed = true;
      setDirectoryHealth(null);
      console.error('Failed to load X-Science MCP directory health:', healthResult.reason);
    }
    if (failed) {
      setLoadError(true);
      Message.error(t('settings.xScienceMcpFetchError'));
    }
    setLoading(false);
  }, [t]);

  useEffect(() => {
    void loadServers();
    return () => {
      loadGenerationRef.current += 1;
    };
  }, [loadServers]);

  const hasInitializingBundledConnector = servers.some(
    (server) => server.source === 'bundled' && server.enabled && server.connectionStatus === 'connecting'
  );

  useEffect(() => {
    if (!hasInitializingBundledConnector) {
      initializationPollsRef.current = 0;
      return;
    }
    if (loading || loadError || initializationPollsRef.current >= 60) return;
    const timer = window.setTimeout(() => {
      initializationPollsRef.current += 1;
      void loadServers();
    }, 1000);
    return () => window.clearTimeout(timer);
  }, [hasInitializingBundledConnector, loadError, loadServers, loading]);

  const reconcile = useCallback(async () => {
    setReconciling(true);
    try {
      await reconcileXScienceMcpServers();
      Message.success(t('settings.xScienceMcpReconcileSuccess'));
      await loadServers();
    } catch (error) {
      console.error('Failed to reconcile X-Science MCP connectors:', error);
      Message.error(t('settings.xScienceMcpReconcileError'));
    } finally {
      setReconciling(false);
    }
  }, [loadServers, t]);

  const mutate = useCallback(
    async (serverId: string, action: () => Promise<unknown>, success?: string) => {
      setMutatingServerId(serverId);
      try {
        await action();
        if (success) Message.success(success);
        await loadServers();
      } catch (error) {
        console.error('Failed to update X-Science MCP connector.');
        Message.error(t('settings.xScienceMcpMutationError'));
        throw error;
      } finally {
        setMutatingServerId(null);
      }
    },
    [loadServers, t]
  );

  const saveCustomServer = useCallback(
    async (input: XScienceCustomMcpServerInput) => {
      const current = editingServer;
      await mutate(
        current?.id ?? 'new',
        () => (current ? updateXScienceCustomMcpServer(current.id, input) : createXScienceCustomMcpServer(input)),
        current ? t('settings.xScienceMcpUpdated') : t('settings.xScienceMcpCreated')
      );
      setEditorVisible(false);
      setEditingServer(null);
    },
    [editingServer, mutate, t]
  );

  const removeCustomServer = useCallback(
    (server: XScienceCustomMcpServer) => {
      Modal.confirm({
        title: t('settings.xScienceMcpDeleteTitle', { name: server.name }),
        content: t('settings.xScienceMcpDeleteDetail'),
        okButtonProps: { status: 'danger' },
        onOk: () =>
          mutate(server.id, () => deleteXScienceCustomMcpServer(server.id), t('settings.xScienceMcpDeleted')).catch(
            ignoreHandledMutationError
          ),
      });
    },
    [mutate, t]
  );

  const authorize = useCallback(
    async (server: XScienceMcpServer) => {
      const popup = window.open('about:blank', '_blank');
      if (!popup) {
        Message.error(t('settings.xScienceMcpPopupBlocked'));
        return false;
      }
      popup.opener = null;
      let started = false;
      try {
        await mutate(server.id, async () => {
          const authorization = await authorizeXScienceMcpConnector(server.id);
          if (!authorization.authorizationUrl) {
            popup.close();
            return;
          }
          popup.location.assign(normalizeAuthorizationUrl(authorization.authorizationUrl));
          started = true;
        });
        return started;
      } catch (error) {
        if (!popup.closed) popup.close();
        throw error;
      }
    },
    [mutate, t]
  );

  const currentCredentialServer = credentialServer
    ? (servers.find((server) => server.id === credentialServer.id) ?? credentialServer)
    : null;

  const visibleServers = useMemo(() => {
    const query = search.trim().toLowerCase();
    return servers.filter((server) => {
      const description = resolveXScienceMcpDescription(
        server.name,
        server.description,
        i18n.language,
        server.description_i18n
      );
      if (
        query &&
        !`${server.displayName} ${server.name} ${description} ${server.source}`.toLowerCase().includes(query)
      )
        return false;
      if (domainFilter !== 'all' && domainOf(server) !== domainFilter) return false;
      if (filter === 'connected') return server.enabled && server.connectionStatus === 'connected';
      if (filter === 'needs-attention')
        return !server.enabled || !server.health.ok || server.connectionStatus !== 'connected';
      if (filter === 'custom') return customById.has(server.id) || server.source === 'custom';
      return true;
    });
  }, [customById, domainFilter, domainOf, filter, i18n.language, search, servers]);

  const visiblePageServers = visibleServers;

  useLayoutEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = 0;
  }, [filter, search]);

  const enabledConnectorCount = servers.filter((server) => server.enabled).length;
  const connectedConnectorCount = servers.filter(
    (server) => server.enabled && server.connectionStatus === 'connected'
  ).length;
  const connectorHealthOK =
    enabledConnectorCount > 0 &&
    connectedConnectorCount === enabledConnectorCount &&
    directoryHealth !== null &&
    directoryHealth.ok;

  return (
    <div className='mcp-library' data-testid='x-science-mcp-settings'>
      <McpLibraryToolbar
        compactHeader={compactHeader}
        count={servers.length}
        visibleCount={visibleServers.length}
        customCount={customServers.length}
        search={search}
        onSearch={setSearch}
        filter={filter}
        onFilter={setFilter}
        domainFilter={domainFilter}
        onDomainFilter={setDomainFilter}
        domainOptions={domainOptions}
        onCreate={() => {
          setEditingServer(null);
          setEditorVisible(true);
        }}
        onBrowse={setBrowseView}
        onReconcile={() => void reconcile()}
        loading={loading}
        reconciling={reconciling}
        health={
          <div
            className='flex shrink-0 items-center gap-8px text-12px text-t-secondary sm:ml-auto'
            data-testid='x-science-mcp-directory-health'
          >
            <span
              className={`size-7px rounded-full ${
                directoryHealth === null || enabledConnectorCount === 0
                  ? 'bg-fill-4'
                  : connectorHealthOK
                    ? 'bg-success-6'
                    : directoryHealth?.ok === false
                      ? 'bg-danger-6'
                      : 'bg-warning-6'
              }`}
            />
            <span>
              {t('settings.xScienceMcpConnected')} {connectedConnectorCount}/{enabledConnectorCount}
            </span>
            {directoryHealth !== null && !directoryHealth.ok ? (
              <span>· {t('settings.xScienceMcpDirectoryUnhealthy')}</span>
            ) : null}
            <Button
              type='text'
              size='mini'
              icon={<Refresh size='13' />}
              loading={loading}
              data-testid='x-science-mcp-refresh'
              aria-label={t('common.refresh')}
              onClick={() => void loadServers()}
            />
          </div>
        }
      />

      {loadError ? (
        <div
          data-testid='x-science-mcp-load-error'
          role='status'
          className='settings-load-error-panel flex items-center gap-10px text-12px text-danger-6'
        >
          <span className='min-w-0 flex-1'>{t('settings.xScienceMcpPartialLoadError')}</span>
          <Button size='mini' loading={loading} onClick={() => void loadServers()}>
            {t('common.retry')}
          </Button>
        </div>
      ) : null}

      <div className='mcp-library-scroll' data-testid='mcp-library-scroll' ref={scrollRef}>
        <Spin loading={loading} className='w-full'>
          <div className='x-science-mcp-grid' data-testid='x-science-mcp-grid'>
            {visiblePageServers.map((item) => (
              <McpConnectorCard
                key={item.id}
                server={item}
                custom={customById.get(item.id) ?? null}
                busy={mutatingServerId === item.id}
                onToggle={(server, enabled) =>
                  void mutate(server.id, () => setXScienceMcpConnectorEnabled(server.id, enabled)).catch(
                    ignoreHandledMutationError
                  )
                }
                onPermissions={setPermissionServer}
                onConfigure={setCredentialServer}
                onDisconnect={(server) =>
                  void mutate(server.id, () => disconnectXScienceMcpConnector(server.id)).catch(
                    ignoreHandledMutationError
                  )
                }
                onAttachAll={(server) =>
                  void mutate(server.id, () => attachXScienceMcpServerToAllAgents(server.id)).catch(
                    ignoreHandledMutationError
                  )
                }
                onDetachAll={(server) =>
                  void mutate(server.id, () => detachXScienceMcpServerFromAllAgents(server.id)).catch(
                    ignoreHandledMutationError
                  )
                }
                onEdit={(custom) => {
                  setEditingServer(custom);
                  setEditorVisible(true);
                }}
                onDelete={removeCustomServer}
              />
            ))}
          </div>
          {!loading && !loadError && visibleServers.length === 0 ? (
            <Empty
              description={t(
                servers.length === 0 && !search && filter === 'all'
                  ? 'settings.xScienceMcpEmpty'
                  : 'settings.xScienceMcpNoMatches'
              )}
            />
          ) : null}
        </Spin>
      </div>

      <McpConnectorEditorModal
        visible={editorVisible}
        server={editingServer}
        onCancel={() => {
          setEditorVisible(false);
          setEditingServer(null);
        }}
        onSubmit={saveCustomServer}
      />
      <McpPermissionsModal server={permissionServer} onCancel={() => setPermissionServer(null)} />
      {currentCredentialServer ? (
        <McpConnectorConfigurationModal
          key={currentCredentialServer.id}
          server={currentCredentialServer}
          statusUnavailable={loadError}
          onCancel={() => setCredentialServer(null)}
          onSaveKey={async (apiKey) => {
            await mutate(currentCredentialServer.id, () =>
              configureXScienceMcpConnectorAPIKey(currentCredentialServer.id, apiKey)
            );
          }}
          onAuthorize={() => authorize(currentCredentialServer)}
          onRefresh={loadServers}
          onPermissions={() => {
            setPermissionServer(currentCredentialServer);
            setCredentialServer(null);
          }}
        />
      ) : null}
      <Modal
        visible={browseView !== null}
        title={t('settings.xScienceMcpAddConnector')}
        footer={null}
        onCancel={() => setBrowseView(null)}
        unmountOnExit
        className='mcp-library-browser'
        getPopupContainer={() => scrollRef.current?.closest<HTMLElement>('.settings-page-wrapper') ?? document.body}
        style={{ width: 'min(1100px, calc(100vw - 32px))' }}
      >
        <Tabs
          aria-label={t('settings.xScienceMcpAddConnector')}
          activeTab={browseView ?? 'optional'}
          onChange={(key) => setBrowseView(key as ConnectorBrowseView)}
          destroyOnHide
        >
          <Tabs.TabPane key='optional' title={t('settings.xScienceMcpOptionalTab')}>
            <McpOptionalPanel onInstalled={loadServers} />
          </Tabs.TabPane>
          <Tabs.TabPane key='marketplace' title={t('settings.xScienceMcpMarketplaceTab')}>
            <McpMarketplacePanel onInstalled={loadServers} />
          </Tabs.TabPane>
        </Tabs>
      </Modal>
    </div>
  );
};

function normalizeAuthorizationUrl(value: string): string {
  const url = new URL(value);
  if (url.protocol !== 'https:' || url.username || url.password)
    throw new Error('Authorization URL must use HTTPS without user information');
  return url.toString();
}
