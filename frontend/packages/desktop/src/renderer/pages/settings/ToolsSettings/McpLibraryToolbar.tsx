import { Button, Dropdown, Input, Menu } from '@arco-design/web-react';
import { Down, Plus, Refresh, Search } from '@icon-park/react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import SettingsPageHeader from '../components/SettingsPageHeader';
import SettingsLibraryTabHeader from '../components/SettingsLibraryTabHeader';
import SettingsLibrarySearch from '../components/SettingsLibrarySearch';
import SettingsLibraryFilterSelect from '../components/SettingsLibraryFilterSelect';
import type { ConnectorDomainId } from './connectorDomains';

export type ConnectorFilter = 'all' | 'connected' | 'needs-attention' | 'custom';
export type ConnectorBrowseView = 'optional' | 'marketplace';

interface Props {
  count: number;
  visibleCount: number;
  customCount: number;
  search: string;
  onSearch: (value: string) => void;
  filter: ConnectorFilter;
  onFilter: (value: ConnectorFilter) => void;
  /** Merged-page domain filter: the single control that tab header keeps. */
  domainFilter: ConnectorDomainId;
  onDomainFilter: (value: ConnectorDomainId) => void;
  domainOptions: { id: ConnectorDomainId; count: number }[];
  onCreate: () => void;
  onBrowse: (view: ConnectorBrowseView) => void;
  onReconcile: () => void;
  loading: boolean;
  reconciling: boolean;
  health: React.ReactNode;
  /** When true, renders the merged-page one-line compact header. */
  compactHeader?: boolean;
}

/** The installed library stays mounted while users browse additional connectors. */
export function McpLibraryToolbar(props: Props) {
  const { t } = useTranslation();
  const addAction = (
    <Dropdown
      trigger='click'
      position='br'
      droplist={
        <Menu
          onClickMenuItem={(key) => (key === 'custom' ? props.onCreate() : props.onBrowse(key as ConnectorBrowseView))}
        >
          <Menu.Item key='custom'>{t('settings.xScienceMcpCustom')}</Menu.Item>
          <Menu.Item key='optional'>{t('settings.xScienceMcpOptionalTab')}</Menu.Item>
          <Menu.Item key='marketplace'>{t('settings.xScienceMcpMarketplaceTab')}</Menu.Item>
        </Menu>
      }
    >
      <Button type='primary' icon={<Plus size={15} />} data-testid='x-science-mcp-add'>
        {t('settings.xScienceMcpAddConnector')}
        <Down size={14} />
      </Button>
    </Dropdown>
  );
  const maintenanceActions = (
    <>
      <div className='mcp-library-health'>{props.health}</div>
      <Button
        icon={<Refresh size={14} />}
        loading={props.reconciling}
        disabled={props.loading}
        data-testid='x-science-mcp-reconcile'
        onClick={props.onReconcile}
      >
        {t('settings.xScienceMcpReconcile')}
      </Button>
    </>
  );
  return (
    <>
      {props.compactHeader ? (
        <SettingsLibraryTabHeader
          title={t('settings.tools')}
          count={props.visibleCount}
          search={
            <SettingsLibrarySearch
              label={t('settings.xScienceMcpSearch')}
              value={props.search}
              onChange={props.onSearch}
              data-testid='x-science-mcp-search'
            />
          }
          filters={
            <SettingsLibraryFilterSelect
              aria-label={t('settings.xScienceMcpDomain')}
              data-testid='x-science-mcp-filter'
              value={props.domainFilter}
              onChange={(value) => props.onDomainFilter(value as ConnectorDomainId)}
            >
              {props.domainOptions.map((option) => (
                <option key={option.id} value={option.id}>
                  {t(`settings.xScienceMcpDomains.${option.id}`)} ({option.count})
                </option>
              ))}
            </SettingsLibraryFilterSelect>
          }
          actions={addAction}
        />
      ) : (
        <SettingsPageHeader
          data-testid='tools-header'
          title={
            <>
              {t('settings.tools')} <span className='mcp-library-total'>{props.count}</span>
            </>
          }
          actions={addAction}
        />
      )}
      <div
        className={props.compactHeader ? 'mcp-library-maintenance' : 'mcp-library-toolbar'}
        role={props.compactHeader ? 'group' : 'search'}
        aria-label={t('settings.xScienceMcpConnectors')}
      >
        {!props.compactHeader && (
          <>
            <div className='mcp-library-search'>
              <Input
                value={props.search}
                allowClear
                prefix={<Search size={15} />}
                data-testid='x-science-mcp-search'
                placeholder={t('settings.xScienceMcpSearch')}
                aria-label={t('settings.xScienceMcpSearch')}
                onChange={props.onSearch}
              />
            </div>
            <select
              className='mcp-library-select'
              value={props.filter}
              data-testid='x-science-mcp-filter'
              aria-label={t('settings.skillsSettings.filters')}
              onChange={(event) => props.onFilter(event.target.value as ConnectorFilter)}
            >
              <option value='all'>
                {t('settings.xScienceMcpAll')} ({props.count})
              </option>
              <option value='connected'>{t('settings.xScienceMcpConnected')}</option>
              <option value='needs-attention'>{t('settings.xScienceMcpNeedsAttention')}</option>
              <option value='custom'>
                {t('settings.xScienceMcpCustom')} ({props.customCount})
              </option>
            </select>
          </>
        )}
        {maintenanceActions}
      </div>
    </>
  );
}
