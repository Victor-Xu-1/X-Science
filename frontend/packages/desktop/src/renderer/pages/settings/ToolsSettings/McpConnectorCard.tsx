import { Button, Dropdown, Menu } from '@arco-design/web-react';
import { Delete, Edit, MoreOne } from '@icon-park/react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type {
  XScienceMcpServer,
  XScienceCustomMcpServer,
  XScienceMcpUsage,
} from '@/renderer/services/xScienceCapabilities';
import { resolveXScienceMcpDescription } from '@/renderer/services/mcp/xScienceMcpDescriptions';
import { McpConnectorVisualMark, resolveMcpConnectorVisual } from './mcpConnectorVisuals';
import { resolveConnectorDomain } from './connectorDomains';
import { connectorConfigurationState } from './mcpConnectorConfiguration';

export const McpConnectorCard: React.FC<{
  server: XScienceMcpServer;
  custom: XScienceCustomMcpServer | null;
  busy: boolean;
  onToggle: (server: XScienceMcpServer, enabled: boolean) => void;
  onPermissions: (server: XScienceMcpServer) => void;
  onConfigure: (server: XScienceMcpServer) => void;
  onDisconnect: (server: XScienceMcpServer) => void;
  onAttachAll: (server: XScienceMcpServer) => void;
  onDetachAll: (server: XScienceMcpServer) => void;
  onEdit: (server: XScienceCustomMcpServer) => void;
  onDelete: (server: XScienceCustomMcpServer) => void;
}> = ({
  server,
  custom,
  busy,
  onToggle,
  onPermissions,
  onConfigure,
  onDisconnect,
  onAttachAll,
  onDetachAll,
  onEdit,
  onDelete,
}) => {
  const { i18n, t } = useTranslation();
  const connected = server.enabled && server.connectionStatus === 'connected';
  const authorized = isAuthorized(server.authState);
  const keyDisconnectAvailable = server.apiKeyConfigurable && server.apiKeyConfigured && !server.oauthSupported;
  const upstreams = server.upstreams.map(readUpstreamName).filter((name): name is string => Boolean(name));
  const localizedDescription = server.description
    ? resolveXScienceMcpDescription(server.name, server.description, i18n.language, server.description_i18n)
    : custom?.url;
  const displayDescription = localizedDescription?.trim();
  const connectorVisual = resolveMcpConnectorVisual(server.name, server.displayName);
  const connectionState = !server.enabled ? 'disabled' : connected ? 'connected' : 'attention';
  const connectionLabel = t(`settings.mcpConfiguration.states.${connectorConfigurationState(server)}`);
  const usageDetails = formatMcpUsageDetails(server.usage, i18n.language, t);
  const usageSummary = `${usageDetails.lastUsed} · ${usageDetails.count}`;
  const domainLabel = t(`settings.xScienceMcpDomains.${resolveConnectorDomain(server, custom !== null)}`);
  // Keep the enable switch visible while the overflow menu always exposes the
  // connector's primary action, including permissions for unauthenticated MCPs.
  const metaSummary = `${domainLabel} · ${usageSummary}`;
  const primaryActionKind =
    server.authRequired || server.oauthSupported || server.apiKeyConfigurable ? 'configure' : 'permissions';
  const actionMenu = (
    <Menu
      onClickMenuItem={(key) => {
        if (key === 'primary') {
          if (primaryActionKind === 'configure') onConfigure(server);
          else onPermissions(server);
        } else if (key === 'permissions') onPermissions(server);
        else if (key === 'disconnect') onDisconnect(server);
        else if (key === 'attach') {
          if (server.attachedAgents.length) onDetachAll(server);
          else onAttachAll(server);
        } else if (key === 'edit' && custom) onEdit(custom);
        else if (key === 'delete' && custom) onDelete(custom);
      }}
    >
      <Menu.Item key='primary' data-testid={`x-science-mcp-${primaryActionKind}-${normalizeTestId(server.name)}`}>
        {t(primaryActionKind === 'configure' ? 'settings.xScienceMcpConfigure' : 'settings.xScienceMcpPermissions')}
      </Menu.Item>
      {primaryActionKind !== 'permissions' ? (
        <Menu.Item key='permissions' data-testid={`x-science-mcp-permissions-${normalizeTestId(server.name)}`}>
          {t('settings.xScienceMcpPermissions')}
        </Menu.Item>
      ) : null}
      {server.oauthSupported && authorized ? (
        <Menu.Item key='disconnect' data-testid={`x-science-mcp-disconnect-${normalizeTestId(server.name)}`}>
          {t('settings.xScienceMcpDisconnect')}
        </Menu.Item>
      ) : null}
      {keyDisconnectAvailable ? (
        <Menu.Item key='disconnect' data-testid={`x-science-mcp-disconnect-${normalizeTestId(server.name)}`}>
          {t('settings.xScienceMcpDisconnect')}
        </Menu.Item>
      ) : null}
      {custom ? (
        <>
          <Menu.Item key='attach'>
            {server.attachedAgents.length ? t('settings.xScienceMcpDetachAll') : t('settings.xScienceMcpAttachAll')}
          </Menu.Item>
          <Menu.Item key='edit' data-testid={`x-science-mcp-edit-${normalizeTestId(server.name)}`}>
            <span className='flex items-center gap-7px'>
              <Edit size='13' />
              {t('common.edit')}
            </span>
          </Menu.Item>
          <Menu.Item key='delete' data-testid={`x-science-mcp-delete-${normalizeTestId(server.name)}`}>
            <span className='flex items-center gap-7px text-danger-6'>
              <Delete size='13' />
              {t('common.delete')}
            </span>
          </Menu.Item>
        </>
      ) : null}
    </Menu>
  );
  return (
    <article
      data-testid={`x-science-mcp-${normalizeTestId(server.name)}`}
      aria-label={`${server.displayName}. ${connectionLabel}. ${authStateLabel(server.authState, t)}. ${upstreams.join(', ')}. ${usageSummary}`}
      data-mcp-tone={connectorVisual.tone}
      data-mcp-visual={connectorVisual.glyph}
      data-mcp-connection-state={connectionState}
      data-mcp-domain={resolveConnectorDomain(server, custom !== null)}
      data-mcp-usage-summary={usageSummary}
      className='x-science-mcp-card settings-library-card flex min-w-0 flex-col border border-arco-2 bg-fill-1 transition-colors'
    >
      <div className='x-science-mcp-card__top settings-library-card__heading flex min-w-0 items-center'>
        <div className='x-science-mcp-card__icon settings-library-card__icon flex shrink-0 items-center justify-center'>
          <McpConnectorVisualMark visual={connectorVisual} />
        </div>
        <div className='x-science-mcp-card__identity settings-library-card__title-slot min-w-0 flex-1'>
          <div className='x-science-mcp-card__title-row'>
            <span className='settings-library-card__title min-w-0' title={server.displayName}>
              {server.displayName}
            </span>
          </div>
        </div>
      </div>
      <p className='x-science-mcp-card__description settings-library-card__description' title={displayDescription}>
        {displayDescription}
      </p>
      <div className='x-science-mcp-card__footer settings-library-card__footer'>
        <span className='x-science-mcp-card__meta settings-library-card__meta' title={metaSummary}>
          <span className='x-science-mcp-card__domain'>{domainLabel}</span>
          <span>{usageDetails.lastUsed}</span>
          <span>{usageDetails.count}</span>
        </span>
        <span className='settings-library-card__control x-science-mcp-card__action-cluster flex shrink-0 items-center'>
          <Dropdown droplist={actionMenu} trigger='click' position='br' getPopupContainer={() => document.body}>
            <Button
              size='small'
              type='text'
              shape='circle'
              className='x-science-mcp-card__more'
              data-testid={`x-science-mcp-more-${normalizeTestId(server.name)}`}
              aria-label={`${t('common.more')} · ${server.displayName}`}
              icon={<MoreOne size='15' />}
            />
          </Dropdown>
          <button
            type='button'
            role='switch'
            aria-checked={server.enabled}
            aria-busy={busy}
            aria-label={t('settings.xScienceMcpToggleEnabled', { name: server.displayName })}
            title={usageSummary}
            disabled={busy}
            className='x-science-mcp-card__status-control settings-library-card__control'
            data-state={connectionState}
            data-testid={`x-science-mcp-enabled-${normalizeTestId(server.name)}`}
            onClick={() => onToggle(server, !server.enabled)}
          >
            <span className='x-science-mcp-card__status-dot' aria-hidden='true' />
            <span>{connectionLabel}</span>
          </button>
        </span>
      </div>
    </article>
  );
};

function formatMcpUsageDetails(
  usage: XScienceMcpUsage | undefined,
  language: string | undefined,
  t: ReturnType<typeof useTranslation>['t']
): { lastUsed: string; count: string } {
  const lastUsed = usage
    ? usage.lastUsedAt
      ? t('settings.skillsSettings.usageLastUsed', {
          date: formatMcpUsageDate(usage.lastUsedAt, language),
        })
      : t('settings.skillsSettings.usageNever')
    : t('settings.skillsSettings.usageLastUsed', { date: '—' });
  const callCount = usage
    ? t('settings.skillsSettings.usageCount', { count: usage.invocationCount })
    : t('settings.skillsSettings.usageCountUnknown');
  return { lastUsed, count: callCount };
}

function formatMcpUsageDate(value: string, language: string | undefined): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return value;
  const locale = language?.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US';
  return new Intl.DateTimeFormat(locale, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(timestamp));
}

function isAuthorized(authState: string): boolean {
  return authState === 'connected' || authState === 'authorized' || authState === 'authenticated';
}

function authStateLabel(authState: string, t: ReturnType<typeof useTranslation>['t']): string {
  if (authState === 'not-required') return t('settings.xScienceMcpNoAuth');
  if (isAuthorized(authState)) return t('settings.xScienceMcpConnected');
  if (authState === 'required' || authState === 'unauthorized') return t('settings.xScienceMcpAuthRequired');
  return t('settings.xScienceMcpAuthUnknown');
}

function readUpstreamName(value: unknown): string | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const name = (value as Record<string, unknown>).name;
  return typeof name === 'string' && name ? name : null;
}

function normalizeTestId(value: string): string {
  return value.replace(/[:/\s<>"'|?*]/g, '-');
}
