import { Button, Empty, Input, Message, Spin, Tag } from '@arco-design/web-react';
import { Link, Refresh, Search } from '@icon-park/react';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  createXScienceCustomMcpServer,
  type XScienceCustomMcpServerInput,
} from '@/renderer/services/xScienceCapabilities';
import { resolveXScienceMcpDescription } from '@/renderer/services/mcp/xScienceMcpDescriptions';
import { searchMcpMarketplace, type McpMarketplaceEntry } from '@/renderer/services/mcp/mcpMarketplace';
import { sanitizeExternalHttpUrl } from '@/renderer/utils/platform';

type Props = {
  onInstalled: () => Promise<void>;
};

const QUICK_SEARCHES = [
  { labelKey: 'settings.xScienceMcpMarketplaceQuickBiomedical', query: 'bio' },
  { labelKey: 'settings.xScienceMcpMarketplaceQuickPubMed', query: 'pubmed' },
  { labelKey: 'settings.xScienceMcpMarketplaceQuickChemistry', query: 'chem' },
  { labelKey: 'settings.xScienceMcpMarketplaceQuickClinical', query: 'clinical' },
  { labelKey: 'settings.xScienceMcpMarketplaceQuickProtein', query: 'protein' },
] as const;

export const McpMarketplacePanel: React.FC<Props> = ({ onInstalled }) => {
  const { t } = useTranslation();
  const [query, setQuery] = useState('bio');
  const [entries, setEntries] = useState<McpMarketplaceEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const [pendingId, setPendingId] = useState<string | null>(null);
  const generationRef = useRef(0);

  const runSearch = useCallback(
    async (nextQuery = query) => {
      const generation = ++generationRef.current;
      setLoading(true);
      setError(false);
      try {
        const result = await searchMcpMarketplace(nextQuery);
        if (generation !== generationRef.current) return;
        setEntries(result.entries);
      } catch (reason) {
        if (generation !== generationRef.current) return;
        console.error('Failed to load MCP marketplace:', reason);
        setEntries([]);
        setError(true);
      } finally {
        if (generation === generationRef.current) setLoading(false);
      }
    },
    [query]
  );

  useEffect(() => {
    void runSearch('bio');
    return () => {
      generationRef.current += 1;
    };
  }, [runSearch]);

  const install = useCallback(
    async (entry: McpMarketplaceEntry) => {
      if (!entry.remoteUrl || !entry.transport || entry.authRequired || pendingId) return;
      setPendingId(entry.id);
      try {
        const input: XScienceCustomMcpServerInput = {
          name: connectorName(entry),
          description: `${entry.title} · ${entry.description}`,
          url: entry.remoteUrl,
          transport: entry.transport,
        };
        await createXScienceCustomMcpServer(input);
        Message.success(t('settings.xScienceMcpMarketplaceInstalled'));
        await onInstalled();
      } catch (reason) {
        console.error('Failed to install MCP marketplace connector:', reason);
        Message.error(t('settings.xScienceMcpMarketplaceInstallFailed'));
      } finally {
        setPendingId(null);
      }
    },
    [onInstalled, pendingId, t]
  );

  return (
    <div className='flex flex-col gap-14px' data-testid='x-science-mcp-marketplace'>
      <div className='border-y border-arco-2 py-12px'>
        <div className='flex items-start gap-10px'>
          <span className='mt-1px flex size-28px shrink-0 items-center justify-center rounded-6px bg-fill-2 text-t-secondary'>
            <Link size={15} />
          </span>
          <div className='min-w-0'>
            <div className='text-14px font-650 text-t-primary'>{t('settings.xScienceMcpMarketplaceTitle')}</div>
            <div className='mt-3px max-w-760px text-12px leading-5 text-t-secondary'>
              {t('settings.xScienceMcpMarketplaceDescription')}
            </div>
          </div>
        </div>
        <div className='mt-10px flex flex-wrap gap-x-14px gap-y-6px text-12px'>
          <a
            className='text-t-secondary underline underline-offset-3px'
            href='https://registry.modelcontextprotocol.io/docs'
            target='_blank'
            rel='noreferrer'
          >
            {t('settings.xScienceMcpMarketplaceOfficialRegistry')}
          </a>
          <a
            className='text-t-secondary underline underline-offset-3px'
            href='https://smithery.ai/'
            target='_blank'
            rel='noreferrer'
          >
            {t('settings.xScienceMcpMarketplaceSmithery')}
          </a>
        </div>
        <div className='mt-10px rounded-6px border border-warning-3 bg-warning-1 px-12px py-9px text-12px leading-5 text-t-secondary'>
          {t('settings.xScienceMcpMarketplaceSecurity')}
        </div>
      </div>

      <div className='flex flex-wrap items-center gap-8px border-b border-arco-2 pb-12px'>
        <Input
          value={query}
          allowClear
          className='min-w-220px flex-1 sm:max-w-420px'
          prefix={<Search size={14} />}
          data-testid='x-science-mcp-marketplace-search'
          placeholder={t('settings.xScienceMcpMarketplaceSearch')}
          onChange={setQuery}
          onPressEnter={() => void runSearch()}
        />
        <Button
          type='primary'
          loading={loading}
          data-testid='x-science-mcp-marketplace-search-submit'
          onClick={() => void runSearch()}
        >
          {t('settings.xScienceMcpMarketplaceSearchButton')}
        </Button>
        <Button
          type='text'
          icon={<Refresh size={14} />}
          loading={loading}
          aria-label={t('common.refresh')}
          onClick={() => void runSearch()}
        />
        <div className='flex w-full flex-wrap gap-6px pt-2px'>
          {QUICK_SEARCHES.map((item) => (
            <Button
              key={item.query}
              size='mini'
              type={query === item.query ? 'primary' : 'secondary'}
              onClick={() => {
                setQuery(item.query);
                void runSearch(item.query);
              }}
            >
              {t(item.labelKey)}
            </Button>
          ))}
        </div>
      </div>

      {error ? (
        <div className='flex items-center justify-between gap-10px border-b border-danger-3 pb-10px text-12px text-danger-6'>
          <span>{t('settings.xScienceMcpMarketplaceLoadFailed')}</span>
          <Button size='mini' onClick={() => void runSearch()}>
            {t('common.retry')}
          </Button>
        </div>
      ) : null}

      <Spin loading={loading} className='w-full'>
        {entries.length > 0 ? (
          <div
            className='x-science-mcp-grid grid grid-cols-1 items-stretch gap-10px sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4'
            role='list'
            data-testid='x-science-mcp-marketplace-grid'
          >
            {entries.map((entry) => (
              <MarketplaceEntryCard
                key={entry.id}
                entry={entry}
                pending={pendingId === entry.id}
                onInstall={() => void install(entry)}
              />
            ))}
          </div>
        ) : !loading && !error ? (
          <Empty description={t('settings.xScienceMcpMarketplaceEmpty')} />
        ) : null}
      </Spin>
    </div>
  );
};

const MarketplaceEntryCard: React.FC<{
  entry: McpMarketplaceEntry;
  pending: boolean;
  onInstall: () => void;
}> = ({ entry, pending, onInstall }) => {
  const { i18n, t } = useTranslation();
  const canInstall = Boolean(entry.remoteUrl && entry.transport && !entry.authRequired);
  const registryUrl = sanitizeExternalHttpUrl(entry.registryUrl);
  const connectorId = connectorName(entry);
  const description = resolveXScienceMcpDescription(connectorId, entry.description, i18n.language);
  return (
    <div
      role='listitem'
      data-testid={`x-science-mcp-marketplace-entry-${normalizeTestId(entry.id)}`}
      className='x-science-mcp-card flex min-w-0 flex-col border border-arco-2 bg-fill-1 rd-8px transition-colors hover:border-arco-3 hover:bg-fill-2'
    >
      <div className='x-science-mcp-card__top flex min-w-0 items-start'>
        <div className='x-science-mcp-card__icon settings-list-icon flex size-32px shrink-0 items-center justify-center rounded-6px'>
          <Link size='16' />
        </div>
        <div className='x-science-mcp-card__identity min-w-0 flex-1'>
          <div className='x-science-mcp-card__title-row'>
            <span className='min-w-0 text-14px font-650 text-t-primary'>{entry.title}</span>
          </div>
          <div className='x-science-mcp-card__badges'>
            <Tag size='small' color='arcoblue'>
              {t('settings.xScienceMcpMarketplaceOfficialRegistry')}
            </Tag>
            {entry.version ? <Tag size='small'>v{entry.version}</Tag> : null}
          </div>
        </div>
        <Tag size='small'>{t('settings.xScienceMcpMarketplaceNotInstalled')}</Tag>
      </div>
      <p className='x-science-mcp-card__description' title={entry.description}>
        {description}
      </p>
      <div className='x-science-mcp-card__metadata flex flex-wrap items-center'>
        <span className='text-11px text-t-tertiary'>{entry.provider}</span>
        {entry.transport ? <Tag size='small'>{transportLabel(entry.transport)}</Tag> : null}
        <Tag size='small' color={entry.authRequired ? 'orangered' : 'green'}>
          {entry.authRequired
            ? t('settings.xScienceMcpMarketplaceAuthRequired')
            : t('settings.xScienceMcpMarketplaceNoAuth')}
        </Tag>
      </div>
      <div
        className='x-science-mcp-usage grid grid-cols-2 text-11px text-t-tertiary'
        title={`${t('settings.skillsSettings.usageNever')} · ${t('settings.skillsSettings.usageCount', { count: 0 })}`}
      >
        <span className='min-w-0 truncate'>{t('settings.skillsSettings.usageNever')}</span>
        <span className='truncate text-right tabular-nums'>
          {t('settings.skillsSettings.usageCount', { count: 0 })}
        </span>
      </div>
      <div className='x-science-mcp-card__actions flex flex-wrap items-center'>
        <Button
          size='mini'
          type='text'
          loading={pending}
          disabled={!canInstall || pending}
          title={canInstall ? undefined : t('settings.xScienceMcpMarketplaceManualSetup')}
          onClick={onInstall}
        >
          {canInstall ? t('settings.xScienceMcpMarketplaceInstall') : t('settings.xScienceMcpMarketplaceViewSource')}
        </Button>
        {registryUrl ? (
          <a
            className='text-11px text-t-tertiary underline underline-offset-3px'
            href={registryUrl}
            target='_blank'
            rel='noreferrer'
          >
            {t('settings.xScienceMcpMarketplaceDetails')}
          </a>
        ) : null}
      </div>
    </div>
  );
};

function connectorName(entry: McpMarketplaceEntry): string {
  const suffix = entry.name.split('/').pop() || entry.name;
  const normalized = suffix
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return (normalized || 'mcp-server').slice(0, 48);
}

function normalizeTestId(value: string): string {
  return value.replace(/[^a-zA-Z0-9_-]/g, '-');
}

function transportLabel(value: 'sse' | 'streamable_http'): string {
  return value === 'sse' ? 'SSE' : 'Streamable HTTP';
}
